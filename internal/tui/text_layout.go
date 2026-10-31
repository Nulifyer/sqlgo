package tui

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/clipperhouse/uax29/v2/graphemes"
)

// textLayout maps rune positions to grapheme boundaries and terminal cells.
// Plain ASCII lines need no mapping arrays, including very long SQL lines.
type textLayout struct {
	text                            string
	ascii                           bool
	boundaries, ends, visual, bytes []int
	// Long Unicode lines build a sparse index as navigation visits them.
	// This avoids allocating four full-size mapping arrays on the UI goroutine.
	sparse bool
	runes  int
	points []layoutPoint
}

type layoutPoint struct{ col, cells, offset int }

const sparseLayoutStride = 128

func newTextLayout(line []rune) *textLayout {
	l := &textLayout{text: string(line), ascii: true}
	for _, r := range line {
		if r < 0x20 || r >= 0x7f {
			l.ascii = false
			break
		}
	}
	if l.ascii {
		return l
	}
	if len(line) > maxSyntaxLineRunes {
		l.sparse, l.runes = true, len(line)
		l.points = []layoutPoint{{}}
		return l
	}
	l.ends = make([]int, len(line)+1)
	l.visual = make([]int, len(line)+1)
	l.bytes = make([]int, len(line)+1)
	col, cells := 0, 0
	it := graphemes.FromString(l.text)
	l.boundaries = append(l.boundaries, 0)
	for it.Next() {
		text := it.Value()
		end := col + utf8.RuneCountInString(text)
		width := stringDisplayWidth(text)
		r := line[col]
		if r == '\t' {
			width = editorTabStop - cells%editorTabStop
		} else if r < 0x20 || r == 0x7f {
			_, width = editorDisplayRuneAt(r, cells)
		}
		for c := col; c < end; c++ {
			l.ends[c] = end
			l.visual[c] = cells
			l.bytes[c] = it.Start()
		}
		col = end
		cells += width
		l.visual[col] = cells
		l.bytes[col] = it.End()
		l.boundaries = append(l.boundaries, col)
	}
	return l
}
func (l *textLayout) next(col int) int {
	if l.ascii {
		return min(col+1, len(l.text))
	}
	if l.sparse {
		_, end := l.sparsePosition(col, false)
		return end.col
	}
	if col >= len(l.ends)-1 {
		return col
	}
	return l.ends[max(0, col)]
}
func (l *textLayout) prev(col int) int {
	if l.ascii {
		return max(0, col-1)
	}
	if l.sparse {
		start, _ := l.sparsePosition(max(0, col-1), false)
		return start.col
	}
	i := sort.SearchInts(l.boundaries, col)
	if i > 0 {
		return l.boundaries[i-1]
	}
	return 0
}
func (l *textLayout) column(col int) int {
	if l.ascii {
		return max(0, col)
	}
	if l.sparse {
		start, _ := l.sparsePosition(col, false)
		return start.cells + max(0, col-l.runes)
	}
	if col >= len(l.visual) {
		return l.visual[len(l.visual)-1] + col - len(l.visual) + 1
	}
	return l.visual[max(0, col)]
}
func (l *textLayout) runeColumn(cells int) int {
	if l.ascii {
		return max(0, min(cells, len(l.text)))
	}
	if l.sparse {
		start, _ := l.sparsePosition(cells, true)
		return start.col
	}
	i := sort.Search(len(l.boundaries), func(i int) bool { return l.column(l.boundaries[i]) > cells })
	if i == 0 {
		return 0
	}
	return l.boundaries[i-1]
}
func (l *textLayout) display(col int) (string, int, int) {
	if l.sparse {
		start, end := l.sparsePosition(col, false)
		r, _ := utf8.DecodeRuneInString(l.text[start.offset:])
		if r == '\t' || r < 0x20 || r == 0x7f {
			text, width := editorDisplayRuneAt(r, start.cells)
			return text, width, end.col
		}
		return l.text[start.offset:end.offset], end.cells - start.cells, end.col
	}
	end := l.next(col)
	visual := l.column(col)
	if l.ascii {
		return l.text[col:end], 1, end
	}
	r, _ := utf8.DecodeRuneInString(l.text[l.bytes[col]:])
	if r == '\t' || r < 0x20 || r == 0x7f {
		text, width := editorDisplayRuneAt(r, visual)
		return text, width, end
	}
	return l.text[l.bytes[col]:l.bytes[end]], l.column(end) - visual, end
}

// Return the grapheme containing a rune/cell position. Checkpoints are always
// grapheme boundaries, so restarting iteration preserves combining sequences.
func (l *textLayout) sparsePosition(target int, byCells bool) (layoutPoint, layoutPoint) {
	coordinate := func(p layoutPoint) int {
		if byCells {
			return p.cells
		}
		return p.col
	}
	i := sort.Search(len(l.points), func(i int) bool { return coordinate(l.points[i]) > target }) - 1
	i = max(0, i)
	start := l.points[i]
	it := graphemes.FromString(l.text[start.offset:])
	base, count := start.offset, 0
	for it.Next() {
		g := it.Value()
		width := stringDisplayWidth(g)
		r, _ := utf8.DecodeRuneInString(g)
		if r == '\t' || r < 0x20 || r == 0x7f {
			_, width = editorDisplayRuneAt(r, start.cells)
		}
		end := layoutPoint{start.col + utf8.RuneCountInString(g), start.cells + width, base + it.End()}
		if coordinate(end) > target {
			return start, end
		}
		start = end
		count++
		if count%sparseLayoutStride == 0 && start.col > l.points[len(l.points)-1].col {
			l.points = append(l.points, start)
		}
	}
	return start, start
}
func (b *buffer) lineLayout(row int) *textLayout {
	if b.layouts == nil {
		b.layouts = make(map[int]*textLayout)
	}
	if l := b.layouts[row]; l != nil {
		return l
	}
	l := newTextLayout(b.lines[row])
	b.layouts[row] = l
	return l
}
func clipEditorLabel(s string, width int) string {
	var out strings.Builder
	it := graphemes.FromString(s)
	used := 0
	for it.Next() {
		g := it.Value()
		w := stringDisplayWidth(g)
		if used+w > width {
			break
		}
		out.WriteString(g)
		used += w
	}
	return out.String()
}
