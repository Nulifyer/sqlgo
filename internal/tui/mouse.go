package tui

import "time"

// doubleClickWindow is how long after a press the next press on the same
// coordinate counts as a second click. Picked to match the default on
// most platforms without feeling sluggish in a terminal.
const doubleClickWindow = 400 * time.Millisecond

// clickTracker turns a stream of MouseMsg press events into click counts
// (1 = single, 2 = double, 3 = triple). A click increments the count
// when the same button fires on the same (x, y) cell inside
// doubleClickWindow; otherwise the counter resets to 1.
type clickTracker struct {
	btn   MouseButton
	x, y  int
	last  time.Time
	count int
}

// bump records a press and returns the resulting click count. Callers
// should only invoke this for MouseActionPress on a pressable button.
// Returns 0 for actions/buttons that shouldn't produce a click count.
func (t *clickTracker) bump(msg MouseMsg) int {
	if msg.Action != MouseActionPress {
		return 0
	}
	now := time.Now()
	if t.btn == msg.Button && t.x == msg.X && t.y == msg.Y && now.Sub(t.last) <= doubleClickWindow {
		t.count++
	} else {
		t.count = 1
	}
	t.btn = msg.Button
	t.x = msg.X
	t.y = msg.Y
	t.last = now
	return t.count
}
