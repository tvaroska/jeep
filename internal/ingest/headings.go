package ingest

// HeadingItem is a flat heading-delimited unit: a heading of some level plus
// the content Blocks under it, before the next heading of any level. Adapters
// (mdsource, htmlsource) reduce their format to a flat, ordered slice of these
// items, then call SectionsFromHeadings to build the nested Section tree.
type HeadingItem struct {
	Level  int     // 1-based; 1 = top heading (# / <h1>)
	Title  string
	Blocks []Block
}

// SectionsFromHeadings assembles a flat, ordered list of heading items into a
// nested Section tree using a level stack: each item becomes a child of the
// nearest preceding item with a strictly smaller Level, else a new root.
//
// The algorithm handles skipped levels gracefully (h1 then h3 → h3 nested
// under h1). Returns nil for an empty or nil input.
func SectionsFromHeadings(items []HeadingItem) []*Section {
	if len(items) == 0 {
		return nil
	}

	var roots []*Section
	var stack []*Section // level-stack of open sections

	for _, item := range items {
		sec := &Section{
			Title:  item.Title,
			Level:  item.Level,
			Blocks: item.Blocks,
		}

		// Pop the stack while the top's level >= item.Level (sibling or lower).
		for len(stack) > 0 && stack[len(stack)-1].Level >= item.Level {
			stack = stack[:len(stack)-1]
		}

		// If the stack is now empty, this is a new root; else it's a child of
		// the top (which has a strictly smaller level).
		if len(stack) == 0 {
			roots = append(roots, sec)
		} else {
			parent := stack[len(stack)-1]
			parent.Children = append(parent.Children, sec)
		}

		// Push this section onto the stack for future children.
		stack = append(stack, sec)
	}

	return roots
}
