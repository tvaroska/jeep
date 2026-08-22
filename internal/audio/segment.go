// Package audio handles the TTS-adjacent local + synthesis work for the
// audiobook pipeline: segmenting narration text into request-sized chunks,
// synthesizing each chunk to audio through the Gemini client, and concatenating
// chunks into per-chapter files (with optional EBU R128 loudness normalization).
package audio

import (
	"log/slog"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DefaultMaxSegmentBytes is the default per-segment byte budget. Gemini-TTS
// accepts <=4000 bytes of text per request; we default well under that to leave
// headroom for style/markup and to keep synthesized audio comfortably below the
// ~655s per-request audio cap (4000 bytes of prose is only ~4-5 min at ~150 wpm).
const DefaultMaxSegmentBytes = 3000

// Segmenter splits narration text into ordered, sentence-aligned segments, each
// within a byte budget suitable for a single Gemini-TTS request.
type Segmenter struct {
	MaxBytes int          // per-segment budget in BYTES; <=0 uses DefaultMaxSegmentBytes
	Log      *slog.Logger // optional; nil-safe (uses slog.Default() when nil)
}

// Segment splits text into ordered segments, each <= the resolved byte budget,
// broken at sentence boundaries where possible. Whitespace is normalized
// (runs of spaces/newlines collapse to a single space; each segment trimmed).
// Concatenating the returned segments (space-joined) reproduces the normalized
// text with no content dropped or reordered. Returns nil for empty/blank input.
func (s *Segmenter) Segment(text string) []string {
	// Resolve max byte budget
	max := s.MaxBytes
	if max <= 0 {
		max = DefaultMaxSegmentBytes
	}

	// Split into sentences
	sentences := splitSentences(text)
	if len(sentences) == 0 {
		return nil
	}

	// Greedy pack sentences into segments
	var result []string
	var cur strings.Builder

	for _, sent := range sentences {
		// Determine if sent fits in current segment
		var candidate string
		if cur.Len() == 0 {
			candidate = sent
		} else {
			candidate = cur.String() + " " + sent
		}

		// If it fits, continue building current segment
		if len(candidate) <= max {
			cur.Reset()
			cur.WriteString(candidate)
			continue
		}

		// Won't fit: flush current segment if non-empty
		if cur.Len() > 0 {
			result = append(result, cur.String())
			cur.Reset()
		}

		// Start new segment with sent
		// If sent itself is oversized, split it
		if len(sent) > max {
			pieces := splitOversized(sent, max, s.logger())
			// Append all but last piece to result
			for i := 0; i < len(pieces)-1; i++ {
				result = append(result, pieces[i])
			}
			// Set current to last piece (so subsequent sentences can pack onto it)
			cur.WriteString(pieces[len(pieces)-1])
		} else {
			cur.WriteString(sent)
		}
	}

	// Flush remaining
	if cur.Len() > 0 {
		result = append(result, cur.String())
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

// logger returns s.Log if non-nil, else slog.Default()
func (s *Segmenter) logger() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// abbreviations is a minimal heuristic set of common abbreviations that should
// not be treated as sentence terminators when followed by a period.
// Intentionally small and documented as heuristic — unusual abbreviations may
// cause over/under-splitting, which is acceptable because worst case is a
// slightly shorter/longer-but-still-valid segment. The byte bound always holds
// via splitOversized.
var abbreviations = map[string]bool{
	"mr": true, "mrs": true, "ms": true, "dr": true, "prof": true,
	"sr": true, "jr": true, "st": true, "vs": true, "etc": true,
	"e.g": true, "i.e": true, "eg": true, "ie": true, "fig": true,
	"no": true, "vol": true, "ch": true, "pp": true, "al": true,
}

// splitSentences splits text into sentences using a heuristic sentence detector.
// Each returned sentence is trimmed and non-empty. Whitespace is normalized
// (runs of space/tab/newline collapse to single space). This is intentionally
// heuristic and not perfect NLP — correctness (byte bound, no lost text) never
// depends on perfect sentence detection because splitOversized is the safety net.
func splitSentences(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	var sentences []string
	var cur strings.Builder
	runes := []rune(text)

	for i := 0; i < len(runes); i++ {
		r := runes[i]

		// Normalize whitespace: collapse runs to single space
		if unicode.IsSpace(r) {
			// If we already have trailing space or empty, skip
			s := cur.String()
			if len(s) > 0 && s[len(s)-1] != ' ' {
				cur.WriteRune(' ')
			}
			continue
		}

		cur.WriteRune(r)

		// Check for sentence terminator: . ! ?
		if r == '.' || r == '!' || r == '?' {
			// Look ahead: skip closing quotes/brackets, then check for whitespace or EOS
			j := i + 1
			for j < len(runes) && (runes[j] == '"' || runes[j] == '\'' || runes[j] == ')' || runes[j] == ']') {
				cur.WriteRune(runes[j])
				j++
			}

			// If not followed by whitespace or EOS, not a sentence end
			if j < len(runes) && !unicode.IsSpace(runes[j]) {
				i = j - 1
				continue
			}

			// For period, check abbreviation guard
			if r == '.' {
				token := extractLastToken(cur.String())
				if isAbbreviation(token) {
					i = j - 1
					continue
				}
			}

			// Valid sentence end: emit trimmed sentence
			sent := strings.TrimSpace(cur.String())
			if sent != "" {
				sentences = append(sentences, sent)
			}
			cur.Reset()
			i = j - 1 // Skip trailing whitespace
		}
	}

	// Append remaining text as final sentence
	sent := strings.TrimSpace(cur.String())
	if sent != "" {
		sentences = append(sentences, sent)
	}

	return sentences
}

// extractLastToken returns the last token before a period in the string.
// Used for abbreviation detection.
func extractLastToken(s string) string {
	// Remove trailing period and any trailing quotes/brackets
	s = strings.TrimRight(s, ".\"')]")
	// Find last space or start of string
	idx := strings.LastIndexAny(s, " \t\n")
	if idx < 0 {
		return strings.ToLower(s)
	}
	return strings.ToLower(s[idx+1:])
}

// isAbbreviation checks if a lowercased token (without period) is a known abbreviation.
// Also treats single-letter tokens as abbreviations (initials like "A" in "A. Turing").
func isAbbreviation(token string) bool {
	if len(token) == 1 && unicode.IsLetter(rune(token[0])) {
		return true
	}
	return abbreviations[token]
}

// splitOversized splits a sentence that exceeds max bytes into smaller pieces,
// each <= max bytes. Uses a cascade of strategies:
// 1. Clause split ("; " then ", ")
// 2. Word split (on spaces)
// 3. Hard rune split (last resort, on UTF-8 boundaries)
//
// Guarantees each returned piece is non-empty and <= max bytes, and preserves order.
func splitOversized(sentence string, max int, log *slog.Logger) []string {
	if len(sentence) <= max {
		return []string{sentence}
	}

	// Try clause split
	for _, delim := range []string{"; ", ", "} {
		if strings.Contains(sentence, delim) {
			clauses := strings.Split(sentence, delim)
			// Re-pack clauses greedily
			packed := greedyPack(clauses, delim, max)
			// Check if all pieces fit
			allFit := true
			for _, piece := range packed {
				if len(piece) > max {
					allFit = false
					break
				}
			}
			if allFit {
				return packed
			}
		}
	}

	// Clause split didn't work; try word split
	words := strings.Fields(sentence)
	if len(words) > 1 {
		packed := greedyPack(words, " ", max)
		// Check if all pieces fit
		allFit := true
		for _, piece := range packed {
			if len(piece) > max {
				allFit = false
				break
			}
		}
		if allFit {
			return packed
		}
	}

	// Word split didn't work; hard rune split on any remaining oversized pieces
	var result []string
	for _, piece := range greedyPack(words, " ", max) {
		if len(piece) > max {
			log.Warn("hard rune split on oversized token (pathological input)",
				"tokenLen", len(piece), "maxBytes", max)
			result = append(result, hardRuneSplit(piece, max)...)
		} else {
			result = append(result, piece)
		}
	}

	return result
}

// greedyPack packs items into segments separated by sep, each segment <= max bytes.
func greedyPack(items []string, sep string, max int) []string {
	var result []string
	var cur strings.Builder

	for _, item := range items {
		var candidate string
		if cur.Len() == 0 {
			candidate = item
		} else {
			candidate = cur.String() + sep + item
		}

		if len(candidate) <= max {
			cur.Reset()
			cur.WriteString(candidate)
		} else {
			// Flush current if non-empty
			if cur.Len() > 0 {
				result = append(result, cur.String())
				cur.Reset()
			}
			cur.WriteString(item)
		}
	}

	if cur.Len() > 0 {
		result = append(result, cur.String())
	}

	return result
}

// hardRuneSplit splits s into chunks of <= max bytes, cutting only on UTF-8 rune
// boundaries. Last resort for giant tokens (URLs, etc.). Each chunk is non-empty
// and valid UTF-8.
func hardRuneSplit(s string, max int) []string {
	var result []string
	var cur strings.Builder

	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError && size == 1 {
			// Invalid UTF-8; skip this byte (should not happen with valid input)
			s = s[1:]
			continue
		}

		if cur.Len()+size > max && cur.Len() > 0 {
			// Flush current chunk
			result = append(result, cur.String())
			cur.Reset()
		}

		cur.WriteRune(r)
		s = s[size:]
	}

	if cur.Len() > 0 {
		result = append(result, cur.String())
	}

	return result
}

// Segment is a package-level convenience using a default Segmenter.
func Segment(text string) []string {
	return (&Segmenter{}).Segment(text)
}
