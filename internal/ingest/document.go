// Package ingest defines the format-agnostic document model and the Source seam.
// A Source turns some input (PDF, ePub, markdown, HTML) into a Document: a tree
// of Sections carrying ordered content Blocks. Downstream stages (chapterization,
// describe, TTS pipeline) consume the tree and never see the original format.
package ingest

// BlockKind classifies one ordered piece of a Section's content.
type BlockKind string

const (
	BlockProse   BlockKind = "prose"
	BlockFigure  BlockKind = "figure"
	BlockCaption BlockKind = "caption"
	BlockCode    BlockKind = "code"
	BlockTable   BlockKind = "table"
)

// Block is one ordered unit of content within a Section, in reading order.
// Text carries prose/caption/code/table content; Data/MimeType carry a raster
// image when Kind == BlockFigure. Page is the 1-based source page when the
// adapter can supply it, else 0.
type Block struct {
	Kind     BlockKind
	Text     string // set for prose/caption/code/table; empty for figure
	Data     []byte // raster bytes when Kind == BlockFigure
	MimeType string // e.g. "image/png" when Kind == BlockFigure
	Page     int    // 1-based source page if known; 0 otherwise
}

// Section is a node in the document's structure tree: a titled unit with its own
// ordered content Blocks (its "intro"/body prose that precedes subsections) plus
// ordered child Sections.
type Section struct {
	Title    string
	Level    int // 1-based hierarchy level (1 = top-level)
	Blocks   []Block
	Children []*Section
}

// Meta is document-level metadata.
type Meta struct {
	Title  string
	Author string
	Path   string // source path the Document was loaded from
}

// Document is a format-agnostic ingested document: metadata + a Section tree.
// Sections holds the top-level sections in reading order.
type Document struct {
	Meta     Meta
	Sections []*Section
}

// Source loads an input file into a Document. Each format (PDF, ePub, markdown,
// HTML) provides one implementation; the audiobook pipeline depends only on this
// interface, never on a concrete format. (Adapters land in S1-ab-3/4/12.)
type Source interface {
	Load(path string) (*Document, error)
}
