// Package pdfsource implements the ingest.Source seam for PDF files using a
// pure-Go (CGO-free) stack: pdfcpu for the outline (bookmarks) and embedded
// images, ledongthuc/pdf for page prose. It produces an *ingest.Document — a
// section tree with per-page prose and figure blocks — that ingest.Structure
// consumes unchanged. The heavy PDF dependencies live ONLY in this adapter so
// that internal/ingest stays dependency-free.
//
// Text is stored RAW: ledongthuc's GetPlainText glues line-wrapped words and
// drops some heading ligatures. That is intentional — the S1-ab-6 LLM "prepare
// for listening" stage repairs those artifacts. Do NOT add a deterministic
// de-glue/ligature pass here (a geometry-based attempt failed; see DECISIONS.md).
package pdfsource

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	ledpdf "github.com/ledongthuc/pdf"
	pdfcpuapi "github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"

	"github.com/tvaroska/jeep/internal/ingest"
)

func init() {
	// pdfcpu reads/writes a config dir on first use, which can fail or emit
	// noise in sandboxed/CI environments. "disable" turns that off entirely.
	model.ConfigPath = "disable"
}

// Source loads a PDF into an *ingest.Document. The zero value is usable; a nil
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

	// Authoritative page count from pdfcpu (lenient parser; drives span math).
	numPages, err := pdfcpuapi.PageCountFile(path)
	if err != nil {
		return nil, fmt.Errorf("pdfsource: page count %q: %w", path, err)
	}

	// Prose per 1-based page (indexed 0..N; element 0 unused). Best-effort:
	// ledongthuc has a stricter header parser than pdfcpu and can reject a PDF
	// pdfcpu reads fine — a text-open failure must not sink outline+images.
	pages, err := extractText(path, log)
	if err != nil {
		log.Warn("pdfsource: text extraction unavailable; continuing without prose", "path", path, "err", err)
		pages = make([]string, numPages+1)
	}

	// Figure blocks per 1-based page, in extraction order.
	figures, err := extractImages(path, log)
	if err != nil {
		// Images are best-effort: a book with no extractable images (or a codec
		// pdfcpu chokes on) must still ingest. Log and continue with none.
		log.Warn("pdfsource: image extraction failed; continuing without figures", "path", path, "err", err)
		figures = map[int][]ingest.Block{}
	}

	bms, err := extractBookmarks(path)
	if err != nil || len(bms) == 0 {
		// No outline (or unreadable one): fall back to a single synthetic
		// top-level section holding every page's prose + figures, so a PDF
		// without bookmarks still ingests. Mirrors audi's whole-doc fallback.
		if err != nil {
			log.Warn("pdfsource: no readable outline; using whole-document fallback", "path", path, "err", err)
		}
		root := &ingest.Section{Level: 1}
		for n := 1; n <= numPages; n++ {
			root.Blocks = append(root.Blocks, pageBlocks(n, pages, figures)...)
		}
		return &ingest.Document{
			Meta:     ingest.Meta{Path: path},
			Sections: []*ingest.Section{root},
		}, nil
	}

	sections := buildSections(bms, numPages, pages, figures)
	return &ingest.Document{
		Meta:     ingest.Meta{Path: path},
		Sections: sections,
	}, nil
}

// Load is a convenience wrapper: (&Source{}).Load(path).
func Load(path string) (*ingest.Document, error) { return (&Source{}).Load(path) }

// extractBookmarks returns the top-level pdfcpu bookmark tree. An absent outline
// surfaces as an error or an empty slice; callers treat both as "no outline".
func extractBookmarks(path string) ([]pdfcpu.Bookmark, error) {
	rs, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	return pdfcpuapi.Bookmarks(rs, nil)
}

// extractText returns per-page prose indexed by 1-based page number; element 0
// is unused so callers can index pages[n] directly. GetPlainText is known to
// panic on some malformed content, so each page is guarded — a bad page is
// skipped (empty prose) rather than failing the whole book.
func extractText(path string, log *slog.Logger) ([]string, error) {
	f, r, err := ledpdf.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	numPages := r.NumPage()
	pages := make([]string, numPages+1)
	for n := 1; n <= numPages; n++ {
		pages[n] = plainTextGuarded(r, n, log, path)
	}
	return pages, nil
}

// plainTextGuarded extracts page n's prose, converting a null page or a panic
// into empty prose (logged) instead of aborting extraction.
func plainTextGuarded(r *ledpdf.Reader, n int, log *slog.Logger, path string) (txt string) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Warn("pdfsource: recovered from panic extracting page text", "path", path, "page", n, "panic", rec)
			txt = ""
		}
	}()
	p := r.Page(n)
	if p.V.IsNull() {
		return ""
	}
	s, err := p.GetPlainText(nil)
	if err != nil {
		log.Warn("pdfsource: page text extraction error; skipping page", "path", path, "page", n, "err", err)
		return ""
	}
	return s
}

// extractImages returns figure blocks keyed by 1-based page, in extraction
// order. Image bytes MUST be read inside the digest callback — the reader is not
// valid after it returns.
func extractImages(path string, log *slog.Logger) (map[int][]ingest.Block, error) {
	rs, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer rs.Close()

	figures := map[int][]ingest.Block{}
	digest := func(img model.Image, _ bool, _ int) error {
		data, err := io.ReadAll(img)
		if err != nil {
			log.Warn("pdfsource: reading image bytes; skipping image", "path", path, "page", img.PageNr, "name", img.Name, "err", err)
			return nil
		}
		if len(data) == 0 {
			return nil
		}
		figures[img.PageNr] = append(figures[img.PageNr], ingest.Block{
			Kind:     ingest.BlockFigure,
			Data:     data,
			MimeType: mimeFor(img.FileType),
			Page:     img.PageNr,
		})
		return nil
	}
	// nil selectedPages = all pages.
	if err := pdfcpuapi.ExtractImages(rs, nil, digest, nil); err != nil {
		return nil, err
	}
	return figures, nil
}

// mimeFor maps a pdfcpu image FileType to a MIME type.
func mimeFor(fileType string) string {
	switch strings.ToLower(fileType) {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "tiff", "tif":
		return "image/tiff"
	case "":
		return "application/octet-stream"
	default:
		return "image/" + strings.ToLower(fileType)
	}
}

// buildSections converts the pdfcpu bookmark tree into an ingest.Section tree and
// assigns each section its OWN page span's prose + figures via a pre-order DFS.
//
// pdfcpu gives one start page per node. A node's own span is [startPage,
// nextStart-1], where nextStart is the start page of the NEXT node in pre-order
// DFS (first child if any, else next sibling, else nearest ancestor's next
// sibling); the last node's span runs to numPages. Because DFS pre-order visits
// a node then its children, the gap between a node and its first child is exactly
// that node's intro pages, and a leaf runs to the next sibling/uncle. Two
// bookmarks on the same page yield an empty span [p, p-1] for the earlier node,
// so prose is never double-counted.
//
// Front matter before the first bookmark ([1, firstStart-1]) is DROPPED: it is
// almost always cover/copyright/ToC boilerplate that ingest.Structure would drop
// anyway, and acceptance does not require it.
func buildSections(bms []pdfcpu.Bookmark, numPages int, pages []string, figures map[int][]ingest.Block) []*ingest.Section {
	// Convert the tree, recording a parallel pre-order list of (section, start).
	type node struct {
		sec   *ingest.Section
		start int
	}
	var order []node
	var convert func(bm pdfcpu.Bookmark, level int) *ingest.Section
	convert = func(bm pdfcpu.Bookmark, level int) *ingest.Section {
		sec := &ingest.Section{Title: bm.Title, Level: level}
		order = append(order, node{sec: sec, start: bm.PageFrom})
		for _, kid := range bm.Kids {
			sec.Children = append(sec.Children, convert(kid, level+1))
		}
		return sec
	}

	roots := make([]*ingest.Section, 0, len(bms))
	for _, bm := range bms {
		roots = append(roots, convert(bm, 1))
	}

	// Assign each node its own span [start, end], end = next node's start - 1
	// (or numPages for the last node), then fill blocks page by page.
	for i, nd := range order {
		start := nd.start
		if start < 1 {
			start = 1
		}
		end := numPages
		if i+1 < len(order) {
			end = order[i+1].start - 1
		}
		for n := start; n <= end && n <= numPages; n++ {
			nd.sec.Blocks = append(nd.sec.Blocks, pageBlocks(n, pages, figures)...)
		}
	}
	return roots
}

// pageBlocks returns page n's blocks: a prose block (when the page has non-empty
// trimmed text, storing the RAW text) followed by that page's figure blocks, in
// order — interleaving prose + figures by page to preserve rough reading order.
func pageBlocks(n int, pages []string, figures map[int][]ingest.Block) []ingest.Block {
	var blocks []ingest.Block
	if n >= 0 && n < len(pages) && strings.TrimSpace(pages[n]) != "" {
		blocks = append(blocks, ingest.Block{Kind: ingest.BlockProse, Text: pages[n], Page: n})
	}
	blocks = append(blocks, figures[n]...)
	return blocks
}
