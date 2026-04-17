package term

// Rect is the geometry type for TUI panels. All coordinates are 1-based
// and inclusive on both edges. Borders are drawn ON the rect edges, so
// panel content lives at Row+1..Row+H-2.
type Rect struct {
	Row, Col int
	W, H     int
}

// Contains reports whether the given 1-based (row, col) cell is inside
// this rect's bounding box (borders included). Used by mouse hit tests.
func (r Rect) Contains(row, col int) bool {
	return row >= r.Row && row < r.Row+r.H && col >= r.Col && col < r.Col+r.W
}
