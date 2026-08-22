package epubsource

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tvaroska/jeep/internal/ingest"
)

// countSections returns the recursive count of all sections in the tree.
func countSections(secs []*ingest.Section) int {
	n := 0
	for _, s := range secs {
		n++
		n += countSections(s.Children)
	}
	return n
}

// maxLevel returns the deepest Level in the tree.
func maxLevel(secs []*ingest.Section) int {
	m := 0
	for _, s := range secs {
		if s.Level > m {
			m = s.Level
		}
		if c := maxLevel(s.Children); c > m {
			m = c
		}
	}
	return m
}

// walkSections calls fn for every section in pre-order.
func walkSections(secs []*ingest.Section, fn func(*ingest.Section)) {
	for _, s := range secs {
		fn(s)
		walkSections(s.Children, fn)
	}
}

// writeEpub creates an EPUB zip at path with the given files. The png parameter
// supplies the bytes for any "images/fig1.png" entry if present. Returns the path.
func writeEpub(t *testing.T, dir string, files map[string]string, pngData []byte) string {
	t.Helper()
	path := filepath.Join(dir, "test.epub")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	// Write mimetype FIRST (stored, uncompressed) per spec.
	w, err := zw.CreateHeader(&zip.FileHeader{
		Name:   "mimetype",
		Method: zip.Store,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("application/epub+zip")); err != nil {
		t.Fatal(err)
	}

	// Write the rest.
	for name, content := range files {
		if strings.HasSuffix(name, "fig1.png") && pngData != nil {
			w, err := zw.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write(pngData); err != nil {
				t.Fatal(err)
			}
		} else {
			w, err := zw.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte(content)); err != nil {
				t.Fatal(err)
			}
		}
	}
	return path
}

// generatePNG creates a 1×1 RGBA PNG (a real decodable image).
func generatePNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func TestLoad_EPUB3Nav_YieldsSectionTreeWithTitles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`,
		"OEBPS/content.opf": `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Test Book</dc:title>
    <dc:creator>Test Author</dc:creator>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="ch2" href="ch2.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="ch1"/>
    <itemref idref="ch2"/>
  </spine>
</package>`,
		"OEBPS/nav.xhtml": `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<body>
  <nav epub:type="toc">
    <ol>
      <li><a href="ch1.xhtml">Chapter One</a>
        <ol>
          <li><a href="ch2.xhtml">Section 1.1</a></li>
        </ol>
      </li>
    </ol>
  </nav>
</body>
</html>`,
		"OEBPS/ch1.xhtml": `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>Chapter One</title></head>
<body><p>Chapter one prose here.</p></body>
</html>`,
		"OEBPS/ch2.xhtml": `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>Section 1.1</title></head>
<body><p>Section 1.1 prose here.</p></body>
</html>`,
	}
	path := writeEpub(t, dir, files, nil)

	doc, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if doc.Meta.Title != "Test Book" {
		t.Errorf("Meta.Title = %q, want Test Book", doc.Meta.Title)
	}
	if doc.Meta.Author != "Test Author" {
		t.Errorf("Meta.Author = %q, want Test Author", doc.Meta.Author)
	}
	if got := len(doc.Sections); got != 1 {
		t.Fatalf("top-level sections = %d, want 1", got)
	}
	if doc.Sections[0].Title != "Chapter One" {
		t.Errorf("section[0].Title = %q, want Chapter One", doc.Sections[0].Title)
	}
	if doc.Sections[0].Level != 1 {
		t.Errorf("section[0].Level = %d, want 1", doc.Sections[0].Level)
	}
	if got := len(doc.Sections[0].Children); got != 1 {
		t.Fatalf("section[0].Children = %d, want 1", got)
	}
	if doc.Sections[0].Children[0].Title != "Section 1.1" {
		t.Errorf("section[0].Children[0].Title = %q, want Section 1.1", doc.Sections[0].Children[0].Title)
	}
	if doc.Sections[0].Children[0].Level != 2 {
		t.Errorf("section[0].Children[0].Level = %d, want 2", doc.Sections[0].Children[0].Level)
	}
	if got := maxLevel(doc.Sections); got != 2 {
		t.Errorf("maxLevel = %d, want 2", got)
	}

	// Verify prose is mapped to sections.
	var ch1Prose, sec11Prose bool
	for _, b := range doc.Sections[0].Blocks {
		if b.Kind == ingest.BlockProse && strings.Contains(b.Text, "Chapter one prose") {
			ch1Prose = true
		}
	}
	for _, b := range doc.Sections[0].Children[0].Blocks {
		if b.Kind == ingest.BlockProse && strings.Contains(b.Text, "Section 1.1 prose") {
			sec11Prose = true
		}
	}
	if !ch1Prose {
		t.Errorf("expected 'Chapter one prose' in section[0].Blocks")
	}
	if !sec11Prose {
		t.Errorf("expected 'Section 1.1 prose' in section[0].Children[0].Blocks")
	}

	// T2 acceptance: exercise ingest.Structure on this doc.
	chapters := ingest.Structure(doc)
	if len(chapters) == 0 {
		t.Fatalf("Structure yielded no chapters")
	}
	// The top-level section has >= 1 child (nested TOC), so Structure expands it.
	// Verify at least one chapter has non-empty prose.
	var hasNonEmptyProse bool
	for _, ch := range chapters {
		for _, sec := range ch.Sections {
			for _, b := range sec.Blocks {
				if b.Kind == ingest.BlockProse && strings.TrimSpace(b.Text) != "" {
					hasNonEmptyProse = true
					break
				}
			}
			if hasNonEmptyProse {
				break
			}
		}
		if hasNonEmptyProse {
			break
		}
	}
	if !hasNonEmptyProse {
		t.Errorf("ingest.Structure produced chapters with no non-empty prose Blocks")
	}
}

func TestLoad_EmbeddedImage_YieldsFigureBlock(t *testing.T) {
	dir := t.TempDir()
	pngData := generatePNG()
	files := map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`,
		"OEBPS/content.opf": `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Image Test</dc:title>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="img1" href="images/fig1.png" media-type="image/png"/>
  </manifest>
  <spine>
    <itemref idref="ch1"/>
  </spine>
</package>`,
		"OEBPS/nav.xhtml": `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<body>
  <nav epub:type="toc">
    <ol>
      <li><a href="ch1.xhtml">Chapter with Image</a></li>
    </ol>
  </nav>
</body>
</html>`,
		"OEBPS/ch1.xhtml": `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>Chapter with Image</title></head>
<body>
  <p>Prose before image.</p>
  <img src="images/fig1.png" alt="figure"/>
  <p>Prose after image.</p>
</body>
</html>`,
		"OEBPS/images/fig1.png": "", // placeholder; pngData supplied
	}
	path := writeEpub(t, dir, files, pngData)

	doc, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var figures int
	var proseBeforeFigure, figureBlock, proseAfterFigure bool
	walkSections(doc.Sections, func(s *ingest.Section) {
		for i, b := range s.Blocks {
			if b.Kind == ingest.BlockFigure {
				figures++
				figureBlock = true
				if len(b.Data) == 0 {
					t.Errorf("figure block has empty Data")
				}
				if !strings.HasPrefix(b.MimeType, "image/") {
					t.Errorf("figure block MimeType = %q, want image/*", b.MimeType)
				}
				// Check that a prose block precedes it.
				if i > 0 && s.Blocks[i-1].Kind == ingest.BlockProse {
					proseBeforeFigure = true
				}
			}
			if b.Kind == ingest.BlockProse {
				if strings.Contains(b.Text, "Prose before image") {
					// Next block should be figure if we're seeing reading order.
					// We'll check proseBeforeFigure after the loop.
				}
				if strings.Contains(b.Text, "Prose after image") {
					proseAfterFigure = true
				}
			}
		}
	})

	if figures == 0 {
		t.Fatalf("expected at least one BlockFigure, got none")
	}
	if !figureBlock {
		t.Errorf("expected a BlockFigure with non-empty Data and image/* MimeType")
	}
	if !proseBeforeFigure {
		t.Errorf("expected a BlockProse before the BlockFigure (reading-order interleave)")
	}
	if !proseAfterFigure {
		t.Errorf("expected prose after the image")
	}
}

func TestLoad_NCXFallback_YieldsSectionTree(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`,
		"OEBPS/content.opf": `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>NCX Test</dc:title>
  </metadata>
  <manifest>
    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
    <item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="ch2" href="ch2.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine toc="ncx">
    <itemref idref="ch1"/>
    <itemref idref="ch2"/>
  </spine>
</package>`,
		"OEBPS/toc.ncx": `<?xml version="1.0"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <navMap>
    <navPoint>
      <navLabel><text>Chapter A</text></navLabel>
      <content src="ch1.xhtml"/>
      <navPoint>
        <navLabel><text>Sub A.1</text></navLabel>
        <content src="ch2.xhtml"/>
      </navPoint>
    </navPoint>
  </navMap>
</ncx>`,
		"OEBPS/ch1.xhtml": `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<body><p>Chapter A prose.</p></body>
</html>`,
		"OEBPS/ch2.xhtml": `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<body><p>Sub A.1 prose.</p></body>
</html>`,
	}
	path := writeEpub(t, dir, files, nil)

	doc, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := len(doc.Sections); got != 1 {
		t.Fatalf("top-level sections = %d, want 1", got)
	}
	if doc.Sections[0].Title != "Chapter A" {
		t.Errorf("section[0].Title = %q, want Chapter A", doc.Sections[0].Title)
	}
	if got := len(doc.Sections[0].Children); got != 1 {
		t.Fatalf("section[0].Children = %d, want 1", got)
	}
	if doc.Sections[0].Children[0].Title != "Sub A.1" {
		t.Errorf("section[0].Children[0].Title = %q, want Sub A.1", doc.Sections[0].Children[0].Title)
	}

	// Verify NCX-sourced hierarchy.
	if got := maxLevel(doc.Sections); got != 2 {
		t.Errorf("maxLevel = %d, want 2 (NCX nested navPoint)", got)
	}

	// Verify prose is mapped.
	var chAProse, subA1Prose bool
	for _, b := range doc.Sections[0].Blocks {
		if b.Kind == ingest.BlockProse && strings.Contains(b.Text, "Chapter A prose") {
			chAProse = true
		}
	}
	for _, b := range doc.Sections[0].Children[0].Blocks {
		if b.Kind == ingest.BlockProse && strings.Contains(b.Text, "Sub A.1 prose") {
			subA1Prose = true
		}
	}
	if !chAProse {
		t.Errorf("expected 'Chapter A prose' in section[0].Blocks")
	}
	if !subA1Prose {
		t.Errorf("expected 'Sub A.1 prose' in section[0].Children[0].Blocks")
	}
}

func TestLoad_NoTOC_PerSpineFallback(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`,
		"OEBPS/content.opf": `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>No TOC Test</dc:title>
  </metadata>
  <manifest>
    <item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="ch2" href="ch2.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="ch1"/>
    <itemref idref="ch2"/>
  </spine>
</package>`,
		"OEBPS/ch1.xhtml": `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>First Doc</title></head>
<body><p>First doc prose.</p></body>
</html>`,
		"OEBPS/ch2.xhtml": `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<body><h1>Second Heading</h1><p>Second doc prose.</p></body>
</html>`,
	}
	path := writeEpub(t, dir, files, nil)

	doc, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Per-spine fallback: one top-level section per spine doc.
	if got := len(doc.Sections); got != 2 {
		t.Fatalf("top-level sections = %d, want 2 (per-spine fallback)", got)
	}
	if doc.Sections[0].Level != 1 {
		t.Errorf("section[0].Level = %d, want 1", doc.Sections[0].Level)
	}
	if doc.Sections[1].Level != 1 {
		t.Errorf("section[1].Level = %d, want 1", doc.Sections[1].Level)
	}

	// Title extraction: first from <title>, else <h1>/<h2>, else "Section N".
	if doc.Sections[0].Title != "First Doc" {
		t.Errorf("section[0].Title = %q, want First Doc (from <title>)", doc.Sections[0].Title)
	}
	if doc.Sections[1].Title != "Second Heading" {
		t.Errorf("section[1].Title = %q, want Second Heading (from <h1>)", doc.Sections[1].Title)
	}

	// Verify prose is per-doc.
	var prose1, prose2 bool
	for _, b := range doc.Sections[0].Blocks {
		if b.Kind == ingest.BlockProse && strings.Contains(b.Text, "First doc prose") {
			prose1 = true
		}
	}
	for _, b := range doc.Sections[1].Blocks {
		if b.Kind == ingest.BlockProse && strings.Contains(b.Text, "Second doc prose") {
			prose2 = true
		}
	}
	if !prose1 {
		t.Errorf("expected 'First doc prose' in section[0].Blocks")
	}
	if !prose2 {
		t.Errorf("expected 'Second doc prose' in section[1].Blocks")
	}
}

func TestLoad_HrefWithFragmentAndSubdir(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`,
		"OEBPS/content.opf": `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Fragment Test</dc:title>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="ch1" href="text/ch1.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="ch1"/>
  </spine>
</package>`,
		"OEBPS/nav.xhtml": `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<body>
  <nav epub:type="toc">
    <ol>
      <li><a href="text/ch1.xhtml#part2">Chapter Fragment</a></li>
    </ol>
  </nav>
</body>
</html>`,
		"OEBPS/text/ch1.xhtml": `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<body><p>Chapter with fragment prose.</p></body>
</html>`,
	}
	path := writeEpub(t, dir, files, nil)

	doc, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Verify the fragment was stripped and the subdir was resolved correctly.
	if got := len(doc.Sections); got != 1 {
		t.Fatalf("top-level sections = %d, want 1", got)
	}
	if doc.Sections[0].Title != "Chapter Fragment" {
		t.Errorf("section[0].Title = %q, want Chapter Fragment", doc.Sections[0].Title)
	}

	// Verify prose is mapped (fragment stripping + subdir resolution worked).
	var prose bool
	for _, b := range doc.Sections[0].Blocks {
		if b.Kind == ingest.BlockProse && strings.Contains(b.Text, "Chapter with fragment prose") {
			prose = true
		}
	}
	if !prose {
		t.Errorf("expected 'Chapter with fragment prose' in section[0].Blocks (fragment strip + subdir resolution)")
	}
}
