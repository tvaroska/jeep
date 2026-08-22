// Package mdsource implements the ingest.Source seam for Markdown and
// plain-text files using a pure stdlib line scanner. It produces an
// *ingest.Document — a section tree with prose and code blocks — that
// ingest.Structure consumes unchanged. The Markdown parser is intentionally
// minimal: ATX headings only (no Setext), fenced code blocks, and prose runs.
// Images (![alt](url)) are NOT extracted (v1 limitation: no local bytes;
// narration is prose+code).
//
// Text is stored RAW: no ligature/de-glue repair. That is the S1-ab-6 LLM
// "prepare for listening" stage's job (see DECISIONS.md).
package mdsource

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/tvaroska/jeep/internal/ingest"
)

// Source loads a Markdown or plain-text file into an *ingest.Document. The
// zero value is usable; a nil Log falls back to slog.Default(). Diagnostics
// go to Log (stderr) so stdout stays clean for agent consumption.
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

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("mdsource: read %q: %w", path, err)
	}

	items := parse(string(data))
	sections := ingest.SectionsFromHeadings(items)

	// Meta.Title = first level-1 heading text if present, else "".
	var title string
	for _, item := range items {
		if item.Level == 1 && item.Title != "" {
			title = item.Title
			break
		}
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

// parse scans the markdown text and returns a flat, ordered list of heading
// items. If the document has content before the first heading (or no headings
// at all), a single top-level HeadingItem{Level:1, Title:"", Blocks: <preamble>}
// is prepended.
func parse(text string) []ingest.HeadingItem {
	var items []ingest.HeadingItem
	var proseBuffer strings.Builder
	inFence := false
	fenceMarker := ""
	var codeBuffer strings.Builder

	// Start with a preamble item (will be discarded if empty and a heading appears).
	currentItem := &ingest.HeadingItem{Level: 1, Title: ""}

	flushProse := func() {
		if txt := strings.TrimSpace(proseBuffer.String()); txt != "" {
			currentItem.Blocks = append(currentItem.Blocks, ingest.Block{Kind: ingest.BlockProse, Text: txt})
		}
		proseBuffer.Reset()
	}

	flushCode := func() {
		if txt := codeBuffer.String(); txt != "" {
			currentItem.Blocks = append(currentItem.Blocks, ingest.Block{Kind: ingest.BlockCode, Text: txt})
		}
		codeBuffer.Reset()
	}

	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := scanner.Text()

		// Check for fence toggle (opening or closing).
		trimmed := strings.TrimSpace(line)
		if !inFence && (strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")) {
			// Opening fence.
			flushProse()
			inFence = true
			if strings.HasPrefix(trimmed, "```") {
				fenceMarker = "```"
			} else {
				fenceMarker = "~~~"
			}
			continue
		}
		if inFence && strings.HasPrefix(trimmed, fenceMarker) {
			// Closing fence.
			flushCode()
			inFence = false
			fenceMarker = ""
			continue
		}

		// Inside a fence, accumulate verbatim (# is not a heading).
		if inFence {
			if codeBuffer.Len() > 0 {
				codeBuffer.WriteString("\n")
			}
			codeBuffer.WriteString(line)
			continue
		}

		// ATX heading detection: ^(#{1,6})\s+(.*)
		if strings.HasPrefix(line, "#") {
			i := 0
			for i < len(line) && line[i] == '#' {
				i++
			}
			if i >= 1 && i <= 6 && i < len(line) && (line[i] == ' ' || line[i] == '\t') {
				// Found a heading.
				flushProse()
				// Emit the preamble if it has blocks.
				if len(currentItem.Blocks) > 0 {
					items = append(items, *currentItem)
				}
				// Start a new heading item.
				level := i
				titleRaw := strings.TrimSpace(line[i:])
				// Strip trailing #.
				title := strings.TrimRight(titleRaw, "#")
				title = strings.TrimSpace(title)
				currentItem = &ingest.HeadingItem{Level: level, Title: title}
				continue
			}
		}

		// Prose line.
		if proseBuffer.Len() > 0 {
			proseBuffer.WriteString("\n")
		}
		proseBuffer.WriteString(line)
	}

	// Flush any remaining prose or code at EOF.
	if inFence {
		// Unclosed fence: treat accumulated code as a block.
		flushCode()
	}
	flushProse()

	// Emit the final item (either the last heading or the preamble).
	if len(currentItem.Blocks) > 0 || len(items) == 0 {
		items = append(items, *currentItem)
	}

	return items
}
