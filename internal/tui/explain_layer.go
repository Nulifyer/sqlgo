package tui

import "strings"

// explainLayer shows a parsed EXPLAIN tree in a modal overlay.
// Up/Down navigate, Space expands/collapses, 'r' toggles raw
// output, Esc closes.
type explainLayer struct {
	tree      *explainTree
	flat      []explainRow
	collapsed map[*explainNode]bool
	selected  int
	scroll    int
	showRaw   bool
	status    string
}

// explainRow is one displayed line. depth drives indent; node is
// the source node so expand/collapse can toggle its children.
type explainRow struct {
	node     *explainNode
	depth    int
	isDetail bool // true for the dim sub-lines under a node label
}

func newExplainLayer(tree *explainTree) *explainLayer {
	el := &explainLayer{tree: tree}
	el.rebuild(nil)
	return el
}

// rebuild walks the tree honoring each node's collapsed flag and
// produces the flat row slice. collapsed is a set of pointers
// that should NOT have their children rendered. nil set = expand
// everything.
func (el *explainLayer) rebuild(collapsed map[*explainNode]bool) {
	el.flat = el.flat[:0]
	if el.tree == nil || el.tree.root == nil {
		return
	}
	var walk func(n *explainNode, depth int)
	walk = func(n *explainNode, depth int) {
		el.flat = append(el.flat, explainRow{node: n, depth: depth})
		for _, d := range n.details {
			el.flat = append(el.flat, explainRow{
				node:     &explainNode{label: d},
				depth:    depth + 1,
				isDetail: true,
			})
		}
		if collapsed[n] {
			return
		}
		for _, c := range n.children {
			walk(c, depth+1)
		}
	}
	walk(el.tree.root, 0)
	if el.selected >= len(el.flat) {
		el.selected = len(el.flat) - 1
	}
	if el.selected < 0 {
		el.selected = 0
	}
}

func (el *explainLayer) Draw(a *app, c *cellbuf) {
	boxW := 80
	if boxW > a.term.width-4 {
		boxW = a.term.width - dialogMargin
	}
	if boxW < 48 {
		boxW = 48
	}
	boxH := a.term.height - dialogMargin
	if boxH > 28 {
		boxH = 28
	}
	if boxH < 10 {
		boxH = 10
	}
	row := (a.term.height - boxH) / 2
	col := (a.term.width - boxW) / 2
	if row < 1 {
		row = 1
	}
	if col < 1 {
		col = 1
	}
	r := rect{row: row, col: col, w: boxW, h: boxH}
	c.fillRect(r)
	title := "Query Plan"
	if el.showRaw {
		title = "Query Plan [raw]"
	}
	drawFrame(c, r, title, true)

	innerCol := col + 2
	innerW := boxW - 4
	bodyH := boxH - 3

	if el.showRaw {
		el.drawRaw(c, row+1, innerCol, innerW, bodyH)
	} else {
		el.drawTree(c, row+1, innerCol, innerW, bodyH)
	}

	// Status / hint line at the bottom.
	status := el.status
	if status == "" {
		status = "Up/Dn=move  Space=collapse  r=raw  Esc=close"
	}
	c.writeAt(row+boxH-2, innerCol, truncate(status, innerW))
}

func (el *explainLayer) drawTree(c *cellbuf, row, col, w, h int) {
	if len(el.flat) == 0 {
		c.writeAt(row, col, truncate("(no plan)", w))
		return
	}
	if el.selected < el.scroll {
		el.scroll = el.selected
	}
	if el.selected >= el.scroll+h {
		el.scroll = el.selected - h + 1
	}
	if el.scroll < 0 {
		el.scroll = 0
	}
	dim := Style{FG: ansiBrightBlack, BG: ansiDefaultBG}
	sel := Style{FG: ansiDefault, BG: ansiDefaultBG, Attrs: attrReverse}
	for i := 0; i < h; i++ {
		idx := el.scroll + i
		if idx >= len(el.flat) {
			break
		}
		rowEntry := el.flat[idx]
		indent := strings.Repeat("  ", rowEntry.depth)
		line := indent + rowEntry.node.label
		if idx == el.selected {
			padded := line
			for displayWidth(padded) < w {
				padded += " "
			}
			c.writeStyled(row+i, col, truncate(padded, w), sel)
			continue
		}
		style := defaultStyle()
		if rowEntry.isDetail {
			style = dim
		}
		c.writeStyled(row+i, col, truncate(line, w), style)
	}
}

func (el *explainLayer) drawRaw(c *cellbuf, row, col, w, h int) {
	raw := ""
	if el.tree != nil {
		raw = el.tree.raw
	}
	if raw == "" {
		c.writeAt(row, col, truncate("(no raw output)", w))
		return
	}
	lines := strings.Split(raw, "\n")
	if el.scroll < 0 {
		el.scroll = 0
	}
	if el.scroll >= len(lines) {
		el.scroll = len(lines) - 1
	}
	for i := 0; i < h; i++ {
		idx := el.scroll + i
		if idx >= len(lines) {
			break
		}
		c.writeAt(row+i, col, truncate(lines[idx], w))
	}
}

func (el *explainLayer) HandleKey(a *app, k Key) {
	if k.Kind == KeyEsc {
		a.popLayer()
		return
	}
	switch k.Kind {
	case KeyUp:
		el.selected--
		if el.selected < 0 {
			el.selected = 0
		}
	case KeyDown:
		el.selected++
		if el.selected >= len(el.flat) {
			el.selected = len(el.flat) - 1
		}
	case KeyPgUp:
		el.selected -= 10
		if el.selected < 0 {
			el.selected = 0
		}
	case KeyPgDn:
		el.selected += 10
		if el.selected >= len(el.flat) {
			el.selected = len(el.flat) - 1
		}
	case KeyHome:
		el.selected = 0
	case KeyEnd:
		el.selected = len(el.flat) - 1
	case KeyRune:
		switch k.Rune {
		case 'r', 'R':
			el.showRaw = !el.showRaw
			el.scroll = 0
		case ' ':
			el.toggleCollapsed()
		}
	}
}

// toggleCollapsed flips the expanded state of the selected node
// and rebuilds the flat view. Tracked via a map on the layer
// since rebuild() wants a set.
func (el *explainLayer) toggleCollapsed() {
	if el.selected < 0 || el.selected >= len(el.flat) {
		return
	}
	target := el.flat[el.selected].node
	if el.collapsed == nil {
		el.collapsed = map[*explainNode]bool{}
	}
	el.collapsed[target] = !el.collapsed[target]
	el.rebuild(el.collapsed)
}

func (el *explainLayer) Hints(a *app) string {
	_ = a
	return joinHints("Up/Dn=move", "Space=collapse", "r=raw", "Esc=close")
}
