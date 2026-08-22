// Package htmlsource implements the ingest.Source seam for HTML/web-article
// files using golang.org/x/net/html (pure-Go, CGO-free). It produces an
// *ingest.Document — a section tree with prose and code blocks — that
// ingest.Structure consumes unchanged. The parser applies readability-style
// main-content selection (<article>, else <main>, else <body>) and strips
// boilerplate subtrees (<nav>, <header>, <footer>, <aside>, <script>, <style>,
// <form>, role="navigation"). Headings (<h1>..<h6>) define sections; <pre> and
// standalone <code> blocks become BlockCode.
//
// Text is stored RAW: visible HTML text is extracted with whitespace collapse
// (runs of \n / \t / space → single space, HTML layout artifact). No
// ligature/de-glue repair is applied — that is the S1-ab-6 LLM "prepare for
// listening" stage's job (see DECISIONS.md).
//
// Images (remote http/https or //) are skipped (no network fetch). data: URIs
// are also skipped (v1 limitation: images are out of scope for web articles).
package htmlsource

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"golang.org/x/net/html"

	"github.com/tvaroska/jeep/internal/ingest"
)

// Source loads an HTML file into an *ingest.Document. The zero value is usable;
// a nil Log falls back to slog.Default(). Diagnostics go to Log (stderr) so
// stdout stays clean for agent consumption.
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

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("htmlsource: open %q: %w", path, err)
	}
	defer f.Close()

	doc, err := html.Parse(f)
	if err != nil {
		return nil, fmt.Errorf("htmlsource: parse %q: %w", path, err)
	}

	// Find main content: <article>, else <main>, else <body>, else document root.
	root := findMainContent(doc)

	// Extract headings and content.
	items := extractHeadings(root, log)
	sections := ingest.SectionsFromHeadings(items)

	// Meta.Title = <title> element text, else first heading.
	title := extractTitle(doc)
	if title == "" && len(items) > 0 {
		title = items[0].Title
	}

	return &ingest.Document{
		Meta: ingest.Meta{
			Title: title,
			Path:  path,
		},
		Sections: sections,
	}, nil
}

// Load is a convenience wrapper: (&Source{}).Load(path).
func Load(path string) (*ingest.Document, error) { return (&Source{}).Load(path) }

// findMainContent locates the first <article>, else <main>, else <body>, else
// the document root, for readability-style main-content extraction.
func findMainContent(n *html.Node) *html.Node {
	var article, main, body *html.Node
	var walk func(*html.Node)
	walk = func(nd *html.Node) {
		if nd.Type == html.ElementNode {
			switch nd.Data {
			case "article":
				if article == nil {
					article = nd
				}
			case "main":
				if main == nil {
					main = nd
				}
			case "body":
				if body == nil {
					body = nd
				}
			}
		}
		for c := nd.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)

	if article != nil {
		return article
	}
	if main != nil {
		return main
	}
	if body != nil {
		return body
	}
	return n
}

// extractTitle reads the <title> element's text content.
func extractTitle(n *html.Node) string {
	var titleText string
	var walk func(*html.Node)
	walk = func(nd *html.Node) {
		if titleText != "" {
			return
		}
		if nd.Type == html.ElementNode && nd.Data == "title" {
			titleText = textContent(nd)
		}
		for c := nd.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(titleText)
}

// extractHeadings walks the root and returns a flat, ordered list of heading
// items. Skips boilerplate subtrees (<nav>, <header>, <footer>, <aside>,
// <script>, <style>, <form>, role="navigation"). <h1>..<h6> open new heading
// items, <pre> and standalone <code> become BlockCode, other visible text
// becomes prose.
func extractHeadings(root *html.Node, log *slog.Logger) []ingest.HeadingItem {
	var items []ingest.HeadingItem
	var proseBuffer strings.Builder

	// Start with a preamble item (will be discarded if empty and a heading appears).
	currentItem := &ingest.HeadingItem{Level: 1, Title: ""}

	flushProse := func() {
		if txt := strings.TrimSpace(proseBuffer.String()); txt != "" {
			currentItem.Blocks = append(currentItem.Blocks, ingest.Block{Kind: ingest.BlockProse, Text: txt})
		}
		proseBuffer.Reset()
	}

	var walk func(*html.Node, bool)
	walk = func(n *html.Node, skip bool) {
		// Skip boilerplate subtrees.
		if n.Type == html.ElementNode {
			tag := n.Data
			if tag == "script" || tag == "style" || tag == "nav" || tag == "header" ||
				tag == "footer" || tag == "aside" || tag == "form" {
				skip = true
			}
			// Also skip elements with role="navigation".
			for _, attr := range n.Attr {
				if attr.Key == "role" && strings.Contains(attr.Val, "navigation") {
					skip = true
					break
				}
			}
		}

		if skip {
			return
		}

		// Heading: flush prose, emit the current item if it has blocks, start new item.
		if n.Type == html.ElementNode && len(n.Data) == 2 && n.Data[0] == 'h' &&
			n.Data[1] >= '1' && n.Data[1] <= '6' {
			flushProse()
			// Emit the preamble if it has blocks.
			if len(currentItem.Blocks) > 0 {
				items = append(items, *currentItem)
			}
			// Start a new heading item.
			level := int(n.Data[1] - '0')
			title := textContent(n)
			currentItem = &ingest.HeadingItem{Level: level, Title: title}
			return // don't walk children (title already extracted)
		}

		// <pre>: extract code block (preserve inner text, no whitespace collapse).
		if n.Type == html.ElementNode && n.Data == "pre" {
			flushProse()
			code := rawTextContent(n)
			if strings.TrimSpace(code) != "" {
				currentItem.Blocks = append(currentItem.Blocks, ingest.Block{Kind: ingest.BlockCode, Text: code})
			}
			return // don't walk children
		}

		// Standalone <code> (not inside <pre>): also treat as BlockCode.
		if n.Type == html.ElementNode && n.Data == "code" {
			// Check if parent is <pre>; if so, skip (already handled above).
			if n.Parent != nil && n.Parent.Type == html.ElementNode && n.Parent.Data == "pre" {
				// Already handled by <pre> case.
			} else {
				flushProse()
				code := textContent(n)
				if strings.TrimSpace(code) != "" {
					currentItem.Blocks = append(currentItem.Blocks, ingest.Block{Kind: ingest.BlockCode, Text: code})
				}
				return
			}
		}

		// Text node: accumulate prose.
		if n.Type == html.TextNode {
			txt := strings.Join(strings.Fields(n.Data), " ")
			if txt != "" {
				if proseBuffer.Len() > 0 {
					proseBuffer.WriteString(" ")
				}
				proseBuffer.WriteString(txt)
			}
		}

		// Recurse.
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, skip)
		}
	}

	walk(root, false)
	flushProse()

	// Emit the final item (either the last heading or the preamble).
	if len(currentItem.Blocks) > 0 || len(items) == 0 {
		items = append(items, *currentItem)
	}

	return items
}

// textContent extracts the collapsed text content of n and its descendants.
func textContent(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(nd *html.Node) {
		if nd.Type == html.TextNode {
			sb.WriteString(nd.Data)
			sb.WriteString(" ")
		}
		for c := nd.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(sb.String()), " ")
}

// rawTextContent extracts the text content of n without whitespace collapse
// (for <pre> blocks where newlines and spacing are significant).
func rawTextContent(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(nd *html.Node) {
		if nd.Type == html.TextNode {
			sb.WriteString(nd.Data)
		}
		for c := nd.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}
