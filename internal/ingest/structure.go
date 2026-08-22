package ingest

import "strings"

// Chapter is a narration unit derived from the Section tree. Sections holds the
// constituent sections in reading order; a chapter's content is each listed
// section's Blocks plus that section's entire descendant subtree, concatenated.
type Chapter struct {
	Title    string
	Level    int
	Sections []*Section
}

// defaultSkipTitles: front/back-matter titles that are navigation/boilerplate and
// produce useless audio. Matched case-insensitively against a title prefix (see
// isBoilerplate). Deliberately narrow: Foreword/Preface/About the Authors are real
// content and are NOT skipped. (Ported verbatim from audi internal/pdfx.)
var defaultSkipTitles = []string{
	"cover",
	"honeycomb",
	"copyright",
	"table of contents",
	"contents",
	"index",
	"colophon",
}

func isBoilerplate(title string) bool {
	t := strings.ToLower(strings.TrimSpace(title))
	for _, s := range defaultSkipTitles {
		if t == s || strings.HasPrefix(t, s) {
			return true
		}
	}
	return false
}

// Structure groups a Document's Section tree into narration chapters.
//
//   - Each top-level section (doc.Sections) defines a section unit.
//   - Boilerplate sections (Cover/Copyright/Contents/Index/Colophon/...) are
//     dropped entirely, subtree included.
//   - A section with >= 2 immediate children is EXPANDED into one chapter per
//     child (finer, commute-sized granularity). The first child's chapter also
//     absorbs the parent section's own intro Blocks, via a synthetic intro-shim
//     section, so no content is lost.
//   - A section with < 2 children is kept whole as one chapter.
//
// Returns nil for a document with no sections.
func Structure(doc *Document) []Chapter {
	if doc == nil || len(doc.Sections) == 0 {
		return nil
	}
	var chapters []Chapter
	for _, sec := range doc.Sections {
		if sec == nil || isBoilerplate(sec.Title) {
			continue
		}
		children := sec.Children
		if len(children) < 2 {
			chapters = append(chapters, Chapter{
				Title:    sec.Title,
				Level:    sec.Level,
				Sections: []*Section{sec},
			})
			continue
		}
		// Expand into children. First child absorbs the parent's own intro blocks.
		for j, ch := range children {
			c := Chapter{Title: ch.Title, Level: ch.Level, Sections: []*Section{ch}}
			if j == 0 && len(sec.Blocks) > 0 {
				intro := &Section{Title: "", Level: sec.Level, Blocks: sec.Blocks}
				c.Sections = []*Section{intro, ch}
			}
			chapters = append(chapters, c)
		}
	}
	return chapters
}
