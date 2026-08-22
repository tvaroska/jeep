// Package epubsource implements the ingest.Source seam for EPUB files using a
// pure-Go (CGO-free) stack: archive/zip for the container, encoding/xml for
// OPF/NCX parsing, and golang.org/x/net/html for XHTML/nav parsing. It produces
// an *ingest.Document — a section tree with per-spine-document prose and embedded
// figure blocks — that ingest.Structure consumes unchanged. The heavy ePub
// dependencies live ONLY in this adapter so that internal/ingest stays
// dependency-free.
//
// Text is stored RAW: visible XHTML text is extracted as-is, with runs of
// whitespace collapsed (HTML layout artifact). No ligature/de-glue repair is
// applied — that is the S1-ab-6 LLM "prepare for listening" stage's job (see
// DECISIONS.md).
//
// TOC parsing follows the EPUB3 → EPUB2 fallback: prefer the nav document
// (properties~="nav"), else the NCX. When neither is present/parseable, fall
// back to one top-level section per spine document (a more useful fallback than
// PDF's single blob, since the spine gives natural chapter boundaries).
package epubsource

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"path"
	"strings"

	"golang.org/x/net/html"

	"github.com/tvaroska/jeep/internal/ingest"
)

// Source loads an EPUB into an *ingest.Document. The zero value is usable; a nil
// Log falls back to slog.Default(). Diagnostics go to Log (stderr) so stdout
// stays clean for agent consumption.
type Source struct {
	Log *slog.Logger
}

var _ ingest.Source = (*Source)(nil)

// Load reads path and returns its section tree. See package docs for the model.
func (s *Source) Load(path string) (*ingest.Document, error) {
	log := s.Log
	if log == nil {
		log = slog.Default()
	}

	zr, index, err := openEpub(path)
	if err != nil {
		return nil, fmt.Errorf("epubsource: open: %w", err)
	}
	defer zr.Close()

	opfPath, err := readContainer(index)
	if err != nil {
		return nil, fmt.Errorf("epubsource: container: %w", err)
	}

	opfData, err := parseOPF(index, opfPath, log)
	if err != nil {
		return nil, fmt.Errorf("epubsource: OPF: %w", err)
	}

	// Extract prose + images per spine document.
	docBlocks := make(map[int][]ingest.Block)
	for i, spineDoc := range opfData.spine {
		docBlocks[i] = extractDoc(index, opfData.manifest, spineDoc, log)
	}

	// Build TOC: prefer nav, else NCX, else nil (triggers per-spine fallback).
	var toc []*tocNode
	if opfData.navPath != "" {
		toc, err = parseNav(index, opfData.navPath, log)
		if err != nil {
			log.Warn("epubsource: nav parse failed; trying NCX", "path", path, "err", err)
			toc = nil
		}
	}
	if toc == nil && opfData.ncxPath != "" {
		toc, err = parseNCX(index, opfData.ncxPath, log)
		if err != nil {
			log.Warn("epubsource: NCX parse failed; using per-spine fallback", "path", path, "err", err)
			toc = nil
		}
	}

	sections := buildSections(toc, opfData.spine, docBlocks, index, log)

	return &ingest.Document{
		Meta:     opfData.meta,
		Sections: sections,
	}, nil
}

// Load is a convenience wrapper: (&Source{}).Load(path).
func Load(path string) (*ingest.Document, error) { return (&Source{}).Load(path) }

// openEpub opens the zip and builds a name→file index for O(1) lookups.
func openEpub(path string) (*zip.ReadCloser, map[string]*zip.File, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, nil, err
	}
	index := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		index[f.Name] = f
	}
	return zr, index, nil
}

// readContainer parses META-INF/container.xml and returns the OPF path.
func readContainer(index map[string]*zip.File) (string, error) {
	f := index["META-INF/container.xml"]
	if f == nil {
		return "", fmt.Errorf("no META-INF/container.xml")
	}
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	var container struct {
		Rootfiles struct {
			Rootfile []struct {
				FullPath string `xml:"full-path,attr"`
			} `xml:"rootfile"`
		} `xml:"rootfiles"`
	}
	if err := xml.NewDecoder(rc).Decode(&container); err != nil {
		return "", err
	}
	if len(container.Rootfiles.Rootfile) == 0 {
		return "", fmt.Errorf("no rootfile in container.xml")
	}
	return container.Rootfiles.Rootfile[0].FullPath, nil
}

// opfData holds parsed OPF metadata, manifest, and spine.
type opfData struct {
	meta     ingest.Meta
	manifest map[string]manifestItem
	spine    []string       // absolute zip paths, in spine order
	navPath  string         // absolute zip path to the nav XHTML
	ncxPath  string         // absolute zip path to the NCX
}

type manifestItem struct {
	href       string // absolute zip path
	mediaType  string
	properties string
}

// parseOPF parses the OPF and returns metadata, manifest, spine, nav, and NCX paths.
func parseOPF(index map[string]*zip.File, opfPath string, log *slog.Logger) (opfData, error) {
	f := index[opfPath]
	if f == nil {
		return opfData{}, fmt.Errorf("OPF not found: %s", opfPath)
	}
	rc, err := f.Open()
	if err != nil {
		return opfData{}, err
	}
	defer rc.Close()

	var pkg struct {
		Metadata struct {
			Title   string `xml:"title"`
			Creator string `xml:"creator"`
		} `xml:"metadata"`
		Manifest struct {
			Items []struct {
				ID         string `xml:"id,attr"`
				Href       string `xml:"href,attr"`
				MediaType  string `xml:"media-type,attr"`
				Properties string `xml:"properties,attr"`
			} `xml:"item"`
		} `xml:"manifest"`
		Spine struct {
			TOC      string `xml:"toc,attr"`
			Itemrefs []struct {
				Idref string `xml:"idref,attr"`
			} `xml:"itemref"`
		} `xml:"spine"`
	}
	if err := xml.NewDecoder(rc).Decode(&pkg); err != nil {
		return opfData{}, err
	}

	opfBase := path.Dir(opfPath)
	manifest := make(map[string]manifestItem, len(pkg.Manifest.Items))
	var navPath, ncxPath string

	for _, item := range pkg.Manifest.Items {
		href, err := url.PathUnescape(item.Href)
		if err != nil {
			log.Warn("epubsource: manifest href unescape failed; using raw", "href", item.Href, "err", err)
			href = item.Href
		}
		absPath := path.Clean(path.Join(opfBase, href))
		manifest[item.ID] = manifestItem{
			href:       absPath,
			mediaType:  item.MediaType,
			properties: item.Properties,
		}
		if strings.Contains(item.Properties, "nav") {
			navPath = absPath
		}
		if item.MediaType == "application/x-dtbncx+xml" {
			ncxPath = absPath
		}
	}

	// Build spine: resolve each idref to its absolute zip path.
	spine := make([]string, 0, len(pkg.Spine.Itemrefs))
	for _, ref := range pkg.Spine.Itemrefs {
		item, ok := manifest[ref.Idref]
		if !ok {
			log.Warn("epubsource: spine idref not in manifest; skipping", "idref", ref.Idref)
			continue
		}
		spine = append(spine, item.href)
	}

	if len(spine) == 0 {
		return opfData{}, fmt.Errorf("OPF spine is empty")
	}

	// If spine toc= references an NCX and we haven't found one via media-type, resolve it.
	if ncxPath == "" && pkg.Spine.TOC != "" {
		if item, ok := manifest[pkg.Spine.TOC]; ok {
			ncxPath = item.href
		}
	}

	meta := ingest.Meta{
		Title:  pkg.Metadata.Title,
		Author: pkg.Metadata.Creator,
		Path:   opfPath,
	}

	return opfData{
		meta:     meta,
		manifest: manifest,
		spine:    spine,
		navPath:  navPath,
		ncxPath:  ncxPath,
	}, nil
}

// tocNode is the TOC tree node (title, href resolved to abs zip path, children).
type tocNode struct {
	title    string
	hrefAbs  string
	children []*tocNode
}

// parseNav parses the EPUB3 nav XHTML and returns the TOC tree.
func parseNav(index map[string]*zip.File, navPath string, log *slog.Logger) ([]*tocNode, error) {
	f := index[navPath]
	if f == nil {
		return nil, fmt.Errorf("nav file not found: %s", navPath)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	doc, err := html.Parse(rc)
	if err != nil {
		return nil, err
	}

	navBase := path.Dir(navPath)

	// Find the <nav epub:type="toc"> element (or any <nav> with "toc" in epub:type).
	var navNode *html.Node
	var findNav func(*html.Node)
	findNav = func(n *html.Node) {
		if navNode != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "nav" {
			for _, attr := range n.Attr {
				if strings.Contains(attr.Key, "type") && strings.Contains(attr.Val, "toc") {
					navNode = n
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findNav(c)
		}
	}
	findNav(doc)

	if navNode == nil {
		return nil, fmt.Errorf("no <nav> with epub:type~=toc found")
	}

	// Parse the nested <ol>/<li> structure.
	var parseOL func(*html.Node) []*tocNode
	parseOL = func(ol *html.Node) []*tocNode {
		var nodes []*tocNode
		for c := ol.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode || c.Data != "li" {
				continue
			}
			// Extract <a> title and href.
			var a *html.Node
			for d := c.FirstChild; d != nil; d = d.NextSibling {
				if d.Type == html.ElementNode && d.Data == "a" {
					a = d
					break
				}
			}
			if a == nil {
				continue
			}
			var title, href string
			for _, attr := range a.Attr {
				if attr.Key == "href" {
					href = attr.Val
				}
			}
			// Title = text content of <a>.
			var extractText func(*html.Node) string
			extractText = func(n *html.Node) string {
				if n.Type == html.TextNode {
					return n.Data
				}
				var sb strings.Builder
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					sb.WriteString(extractText(c))
				}
				return sb.String()
			}
			title = strings.TrimSpace(extractText(a))

			// Resolve href relative to nav file's dir.
			hrefUnesc, err := url.PathUnescape(href)
			if err != nil {
				log.Warn("epubsource: nav href unescape failed; using raw", "href", href, "err", err)
				hrefUnesc = href
			}
			hrefAbs := path.Clean(path.Join(navBase, hrefUnesc))

			node := &tocNode{title: title, hrefAbs: hrefAbs}

			// Look for nested <ol> in this <li> = children.
			for d := c.FirstChild; d != nil; d = d.NextSibling {
				if d.Type == html.ElementNode && d.Data == "ol" {
					node.children = parseOL(d)
					break
				}
			}
			nodes = append(nodes, node)
		}
		return nodes
	}

	// Find the first <ol> inside the nav.
	var ol *html.Node
	for c := navNode.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "ol" {
			ol = c
			break
		}
		// Sometimes the <ol> is deeper; do a shallow search.
		var findOL func(*html.Node) *html.Node
		findOL = func(n *html.Node) *html.Node {
			if n.Type == html.ElementNode && n.Data == "ol" {
				return n
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if res := findOL(c); res != nil {
					return res
				}
			}
			return nil
		}
		if ol = findOL(c); ol != nil {
			break
		}
	}
	if ol == nil {
		return nil, fmt.Errorf("no <ol> found in nav")
	}

	return parseOL(ol), nil
}

// parseNCX parses the EPUB2 NCX and returns the TOC tree.
func parseNCX(index map[string]*zip.File, ncxPath string, log *slog.Logger) ([]*tocNode, error) {
	f := index[ncxPath]
	if f == nil {
		return nil, fmt.Errorf("NCX file not found: %s", ncxPath)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	var ncx struct {
		NavMap struct {
			NavPoints []navPoint `xml:"navPoint"`
		} `xml:"navMap"`
	}
	if err := xml.NewDecoder(rc).Decode(&ncx); err != nil {
		return nil, err
	}

	ncxBase := path.Dir(ncxPath)

	var convert func(navPoint) *tocNode
	convert = func(np navPoint) *tocNode {
		hrefUnesc, err := url.PathUnescape(np.Content.Src)
		if err != nil {
			log.Warn("epubsource: NCX href unescape failed; using raw", "href", np.Content.Src, "err", err)
			hrefUnesc = np.Content.Src
		}
		hrefAbs := path.Clean(path.Join(ncxBase, hrefUnesc))
		node := &tocNode{
			title:   np.NavLabel.Text,
			hrefAbs: hrefAbs,
		}
		for _, child := range np.NavPoints {
			node.children = append(node.children, convert(child))
		}
		return node
	}

	var toc []*tocNode
	for _, np := range ncx.NavMap.NavPoints {
		toc = append(toc, convert(np))
	}
	return toc, nil
}

type navPoint struct {
	NavLabel struct {
		Text string `xml:"text"`
	} `xml:"navLabel"`
	Content struct {
		Src string `xml:"src,attr"`
	} `xml:"content"`
	NavPoints []navPoint `xml:"navPoint"`
}

// extractDoc parses an XHTML spine document and returns its prose + image blocks.
func extractDoc(index map[string]*zip.File, manifest map[string]manifestItem, xhtmlPath string, log *slog.Logger) []ingest.Block {
	f := index[xhtmlPath]
	if f == nil {
		log.Warn("epubsource: spine doc not in zip; skipping", "path", xhtmlPath)
		return nil
	}
	rc, err := f.Open()
	if err != nil {
		log.Warn("epubsource: open spine doc failed; skipping", "path", xhtmlPath, "err", err)
		return nil
	}
	defer rc.Close()

	doc, err := html.Parse(rc)
	if err != nil {
		log.Warn("epubsource: parse XHTML failed; skipping", "path", xhtmlPath, "err", err)
		return nil
	}

	xhtmlBase := path.Dir(xhtmlPath)
	var blocks []ingest.Block
	var textBuf strings.Builder

	flushText := func() {
		if txt := strings.TrimSpace(textBuf.String()); txt != "" {
			blocks = append(blocks, ingest.Block{Kind: ingest.BlockProse, Text: txt, Page: 0})
		}
		textBuf.Reset()
	}

	var walk func(*html.Node, bool)
	walk = func(n *html.Node, inScript bool) {
		// Skip <script> and <style> subtrees.
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
			inScript = true
		}
		if n.Type == html.TextNode && !inScript {
			// Accumulate visible text; collapse whitespace.
			txt := n.Data
			txt = strings.Join(strings.Fields(txt), " ")
			if txt != "" {
				if textBuf.Len() > 0 {
					textBuf.WriteString(" ")
				}
				textBuf.WriteString(txt)
			}
		}
		// <img src="...">
		if n.Type == html.ElementNode && n.Data == "img" {
			var src string
			for _, attr := range n.Attr {
				if attr.Key == "src" {
					src = attr.Val
					break
				}
			}
			if src != "" {
				extractImage(src, xhtmlBase, index, manifest, &blocks, log, &textBuf, flushText)
			}
		}
		// SVG <image xlink:href="..." /> or <image href="..." />
		if n.Type == html.ElementNode && n.Data == "image" {
			var src string
			for _, attr := range n.Attr {
				if attr.Key == "href" || strings.Contains(attr.Key, "href") {
					src = attr.Val
					break
				}
			}
			if src != "" {
				extractImage(src, xhtmlBase, index, manifest, &blocks, log, &textBuf, flushText)
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inScript)
		}
	}

	walk(doc, false)
	flushText()
	return blocks
}

// extractImage resolves an image href, reads the bytes, and appends a BlockFigure.
func extractImage(src, xhtmlBase string, index map[string]*zip.File, manifest map[string]manifestItem, blocks *[]ingest.Block, log *slog.Logger, textBuf *strings.Builder, flushText func()) {
	// Skip external URLs and data URIs.
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "//") || strings.HasPrefix(src, "data:") {
		return
	}

	srcUnesc, err := url.PathUnescape(src)
	if err != nil {
		log.Warn("epubsource: image src unescape failed; using raw", "src", src, "err", err)
		srcUnesc = src
	}

	// Strip fragment.
	if idx := strings.Index(srcUnesc, "#"); idx >= 0 {
		srcUnesc = srcUnesc[:idx]
	}

	imgAbs := path.Clean(path.Join(xhtmlBase, srcUnesc))
	f := index[imgAbs]
	if f == nil {
		log.Warn("epubsource: image not in zip; skipping", "src", src, "abs", imgAbs)
		return
	}
	rc, err := f.Open()
	if err != nil {
		log.Warn("epubsource: open image failed; skipping", "path", imgAbs, "err", err)
		return
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		log.Warn("epubsource: read image bytes failed; skipping", "path", imgAbs, "err", err)
		return
	}
	if len(data) == 0 {
		return
	}

	// Flush accumulated prose before the figure.
	flushText()

	// Determine MIME type from manifest, else infer from extension.
	mimeType := "application/octet-stream"
	for _, item := range manifest {
		if item.href == imgAbs {
			mimeType = item.mediaType
			break
		}
	}
	if mimeType == "application/octet-stream" {
		mimeType = mimeForExt(imgAbs)
	}

	*blocks = append(*blocks, ingest.Block{
		Kind:     ingest.BlockFigure,
		Data:     data,
		MimeType: mimeType,
		Page:     0,
	})
}

// buildSections converts the TOC tree into ingest.Section nodes and assigns each
// section its own spine-index span's blocks via pre-order DFS.
//
// The algorithm mirrors pdfsource's page-span DFS, substituting 0-based spine
// index for page number. Each TOC node's href resolves to a spine index; the
// node's own span is [start, nextStart-1], where nextStart is the next node's
// start in pre-order (first child if any, else next sibling, else nearest
// ancestor's next sibling); the last node runs to len(spine)-1.
//
// Front matter before the first TOC entry (spine indices < order[0].start) is
// DROPPED (mirrors PDF; ingest.Structure would drop it anyway).
//
// When there is NO usable TOC (toc == nil), fall back to one top-level section
// per spine document (Level 1), with title from XHTML <title> or first heading,
// else "Section N".
func buildSections(toc []*tocNode, spine []string, docBlocks map[int][]ingest.Block, index map[string]*zip.File, log *slog.Logger) []*ingest.Section {
	if toc == nil || len(toc) == 0 {
		// Per-spine fallback.
		var sections []*ingest.Section
		for i, spineDoc := range spine {
			title := extractTitle(index, spineDoc, i+1, log)
			sections = append(sections, &ingest.Section{
				Title:  title,
				Level:  1,
				Blocks: docBlocks[i],
			})
		}
		return sections
	}

	// Build a map from clean abs path to spine index for TOC href resolution.
	spineIndex := make(map[string]int, len(spine))
	for i, p := range spine {
		spineIndex[p] = i
	}

	// Convert the TOC tree, recording (section, start spine index) in pre-order.
	type node struct {
		sec   *ingest.Section
		start int
	}
	var order []node
	var convert func(*tocNode, int) *ingest.Section
	convert = func(tn *tocNode, level int) *ingest.Section {
		sec := &ingest.Section{Title: tn.title, Level: level}
		// Resolve href to spine index: strip fragment, look up.
		hrefClean := tn.hrefAbs
		if idx := strings.Index(hrefClean, "#"); idx >= 0 {
			hrefClean = hrefClean[:idx]
		}
		start, ok := spineIndex[hrefClean]
		if !ok {
			// Unresolved href: inherit previous node's start (empty span).
			if len(order) > 0 {
				start = order[len(order)-1].start
			} else {
				start = 0
			}
			log.Warn("epubsource: TOC href not in spine; inheriting previous start", "href", tn.hrefAbs)
		}
		order = append(order, node{sec: sec, start: start})
		for _, child := range tn.children {
			sec.Children = append(sec.Children, convert(child, level+1))
		}
		return sec
	}

	roots := make([]*ingest.Section, 0, len(toc))
	for _, tn := range toc {
		roots = append(roots, convert(tn, 1))
	}

	// Assign spans and fill blocks.
	for i, nd := range order {
		start := nd.start
		end := len(spine) - 1
		if i+1 < len(order) {
			end = order[i+1].start - 1
		}
		for idx := start; idx <= end && idx < len(spine); idx++ {
			nd.sec.Blocks = append(nd.sec.Blocks, docBlocks[idx]...)
		}
	}

	return roots
}

// extractTitle reads an XHTML doc's <title> element, or the first <h1>/<h2>,
// else "Section N".
func extractTitle(index map[string]*zip.File, xhtmlPath string, n int, log *slog.Logger) string {
	f := index[xhtmlPath]
	if f == nil {
		return fmt.Sprintf("Section %d", n)
	}
	rc, err := f.Open()
	if err != nil {
		log.Warn("epubsource: open for title extraction failed", "path", xhtmlPath, "err", err)
		return fmt.Sprintf("Section %d", n)
	}
	defer rc.Close()

	doc, err := html.Parse(rc)
	if err != nil {
		log.Warn("epubsource: parse for title extraction failed", "path", xhtmlPath, "err", err)
		return fmt.Sprintf("Section %d", n)
	}

	var titleText, headingText string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if n.Data == "title" && titleText == "" {
				var extractText func(*html.Node) string
				extractText = func(nd *html.Node) string {
					if nd.Type == html.TextNode {
						return nd.Data
					}
					var sb strings.Builder
					for c := nd.FirstChild; c != nil; c = c.NextSibling {
						sb.WriteString(extractText(c))
					}
					return sb.String()
				}
				titleText = strings.TrimSpace(extractText(n))
			}
			if (n.Data == "h1" || n.Data == "h2") && headingText == "" {
				var extractText func(*html.Node) string
				extractText = func(nd *html.Node) string {
					if nd.Type == html.TextNode {
						return nd.Data
					}
					var sb strings.Builder
					for c := nd.FirstChild; c != nil; c = c.NextSibling {
						sb.WriteString(extractText(c))
					}
					return sb.String()
				}
				headingText = strings.TrimSpace(extractText(n))
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	if titleText != "" {
		return titleText
	}
	if headingText != "" {
		return headingText
	}
	return fmt.Sprintf("Section %d", n)
}

// mimeForExt infers a MIME type from the file extension.
func mimeForExt(name string) string {
	ext := path.Ext(name)
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}
