package widget

import "github.com/Nulifyer/sqlgo/internal/tui/term"

// FormField is one row in a Form. Four render modes, picked in this
// order:
//   - Display != nil       -> non-editable display string (picker-trigger rows)
//   - len(Values) > 0      -> cycler, rendered as "< val >" (Left/Right cycles)
//   - Mask                 -> password input (DrawInputMasked)
//   - otherwise            -> plain input (DrawInput)
//
// SwallowInput=true prevents Form.HandleKey from forwarding Enter /
// printable keys to the underlying Input. Use it for picker-trigger rows
// where the caller intercepts Enter before delegating to the Form.
type FormField struct {
	Label        string
	Input        *Input
	Mask         bool
	Values       []string
	Required     bool
	SwallowInput bool
	Display      func() string
}

// IsCycler reports whether the field is a constrained-value cycler.
func (ff *FormField) IsCycler() bool { return len(ff.Values) > 0 }

// CycleField steps a cycler by delta with wrap-around. Unknown current
// values drop to Values[0] on the first press so a hand-edited JSON with
// a typo recovers cleanly instead of locking the user out.
func CycleField(ff *FormField, delta int) {
	if !ff.IsCycler() {
		return
	}
	cur := ff.Input.String()
	idx := -1
	for i, v := range ff.Values {
		if v == cur {
			idx = i
			break
		}
	}
	if idx < 0 {
		ff.Input.SetString(ff.Values[0])
		return
	}
	n := len(ff.Values)
	next := (idx + delta + n) % n
	ff.Input.SetString(ff.Values[next])
}

// Form is a labeled-field form with cycler support and scroll-into-view
// rendering. It doesn't know about save, validation, or domain types --
// the caller handles submit by reading fields via ActiveField or indexing
// into Fields, then converting to whatever it wants.
type Form struct {
	Fields []*FormField
	Active int
}

// ActiveField returns the currently focused field, or nil if Active is
// out of range (happens when Fields is gated down to a subset).
func (f *Form) ActiveField() *FormField {
	if f.Active < 0 || f.Active >= len(f.Fields) {
		return nil
	}
	return f.Fields[f.Active]
}

func (f *Form) Next() {
	if len(f.Fields) == 0 {
		return
	}
	f.Active = (f.Active + 1) % len(f.Fields)
}

func (f *Form) Prev() {
	if len(f.Fields) == 0 {
		return
	}
	f.Active = (f.Active - 1 + len(f.Fields)) % len(f.Fields)
}

// HandleKey runs the standard form bindings: Tab/Down next, BackTab/Up
// prev, Left/Right cycle on cycler fields, Ctrl+S submit, Enter submit on
// editable fields. Printable input on cyclers / Display / SwallowInput
// fields is consumed with no effect. Input characters are otherwise
// forwarded to the active Input.
//
// Returns submit=true when the caller should validate and persist. The
// caller decides what "persist" means; HandleKey never mutates anything
// other than f.Active and ff.Input.
func (f *Form) HandleKey(k term.Key) (submit bool) {
	switch k.Kind {
	case term.KeyTab, term.KeyDown:
		f.Next()
		return false
	case term.KeyBackTab, term.KeyUp:
		f.Prev()
		return false
	case term.KeyLeft:
		ff := f.ActiveField()
		if ff != nil && ff.IsCycler() {
			CycleField(ff, -1)
			return false
		}
	case term.KeyRight:
		ff := f.ActiveField()
		if ff != nil && ff.IsCycler() {
			CycleField(ff, 1)
			return false
		}
	case term.KeyEnter:
		ff := f.ActiveField()
		if ff != nil && ff.SwallowInput {
			return false
		}
		return true
	}
	if k.Ctrl && k.Rune == 's' {
		return true
	}
	ff := f.ActiveField()
	if ff == nil {
		return false
	}
	if ff.IsCycler() || ff.SwallowInput || ff.Display != nil {
		return false
	}
	ff.Input.Handle(k)
	return false
}

// FormDrawOpts configures Form.Draw. Colors are passed in because the
// widget package is theme-agnostic -- the caller supplies its palette.
//
// RequiredFunc, when non-nil, overrides the per-field Required flag.
// Use it when requiredness is dynamic (e.g. depends on a driver picked
// elsewhere in the form) and you don't want to mutate FormField.Required
// on every change.
type FormDrawOpts struct {
	LabelW       int
	ActiveFG     int
	InactiveFG   int
	RequiredMark string
	RequiredFunc func(ff *FormField) bool
}

// Draw renders one row per field inside r, scrolling so the active
// field stays visible. r is the field area only -- caller owns frame,
// title, status line, etc.
func (f *Form) Draw(c *term.Cellbuf, r term.Rect, opts FormDrawOpts) {
	labelW := opts.LabelW
	if labelW <= 0 {
		labelW = 20
	}
	reqMark := opts.RequiredMark
	if reqMark == "" {
		reqMark = " *"
	}
	bodyH := r.H
	if bodyH <= 0 {
		return
	}

	scroll := 0
	if f.Active >= bodyH {
		scroll = f.Active - bodyH + 1
	}
	if scroll < 0 {
		scroll = 0
	}

	y := 0
	for i, ff := range f.Fields {
		if i < scroll {
			continue
		}
		if y >= bodyH {
			break
		}
		lineRow := r.Row + y

		required := ff.Required
		if opts.RequiredFunc != nil {
			required = opts.RequiredFunc(ff)
		}
		label := ff.Label
		if required {
			label += reqMark
		}
		label += ":"
		if i == f.Active {
			c.SetFg(opts.ActiveFG)
		} else {
			c.SetFg(opts.InactiveFG)
		}
		c.WriteAt(lineRow, r.Col, padRight(label, labelW))
		c.ResetStyle()

		vCol := r.Col + labelW + 2
		maxVal := r.W - labelW - 2
		if maxVal < 1 {
			maxVal = 1
		}

		switch {
		case ff.Display != nil:
			writeTailSlice(c, lineRow, vCol, ff.Display(), maxVal)
		case ff.IsCycler():
			display := ff.Input.String()
			if display == "" {
				display = "(default)"
			}
			writeTailSlice(c, lineRow, vCol, "< "+display+" >", maxVal)
		case ff.Mask:
			if i == f.Active {
				DrawInputMasked(c, ff.Input, lineRow, vCol, maxVal)
			} else {
				DrawInputMaskedNoCursor(c, ff.Input, lineRow, vCol, maxVal)
			}
		default:
			if i == f.Active {
				DrawInput(c, ff.Input, lineRow, vCol, maxVal)
			} else {
				DrawInputNoCursor(c, ff.Input, lineRow, vCol, maxVal)
			}
		}
		y++
	}
}

func writeTailSlice(c *term.Cellbuf, row, col int, s string, maxW int) {
	rs := []rune(s)
	if len(rs) > maxW {
		rs = rs[len(rs)-maxW:]
	}
	c.WriteAt(row, col, string(rs))
}

func padRight(s string, w int) string {
	rs := []rune(s)
	for len(rs) < w {
		rs = append(rs, ' ')
	}
	return string(rs)
}
