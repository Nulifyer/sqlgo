package tui

import (
	"strings"

	"github.com/Nulifyer/sqlgo/internal/tui/widget"
)

// renameLayer is a small modal overlay that edits the title of a query
// tab. Invoked by Ctrl+R or by a double-click on the query tab strip. Enter
// commits, Esc cancels. Empty/whitespace input is treated as cancel so a
// user who clears the field and presses Enter doesn't end up with a
// blank tab label.
type renameLayer struct {
	idx   int
	input *input
}

func newRenameLayer(idx int, seed string) *renameLayer {
	return &renameLayer{idx: idx, input: newInput(seed)}
}

func (rl *renameLayer) Draw(a *app, c *cellbuf) {
	r := widget.CenterDialog(a.term.width, a.term.height, widget.DialogOpts{
		PrefW: 48, PrefH: 5, MinW: 24, MinH: 5, Margin: dialogMargin,
	})
	widget.DrawDialog(c, r, "Rename query tab", true)

	innerCol := r.Col + 2
	c.WriteAt(r.Row+1, innerCol, "Name:")
	valCol := innerCol + 6
	maxVal := r.W - 6 - 4
	if maxVal < 1 {
		maxVal = 1
	}
	drawInput(c, rl.input, r.Row+1, valCol, maxVal)

	c.WriteAt(r.Row+3, innerCol, truncate("Enter=save  Esc=cancel", r.W-4))
}

func (rl *renameLayer) HandleKey(a *app, k Key) {
	switch k.Kind {
	case KeyEsc:
		a.popLayer()
		return
	case KeyEnter:
		name := strings.TrimSpace(rl.input.String())
		if name != "" {
			m := a.mainLayerPtr()
			if rl.idx >= 0 && rl.idx < len(m.sessions) {
				m.sessions[rl.idx].title = name
			}
		}
		a.popLayer()
		return
	}
	rl.input.Handle(k)
}

func (rl *renameLayer) Hints(a *app) string {
	_ = a
	return joinHints("type=name", "Enter=save", "Esc=cancel")
}
