package tui

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/Nulifyer/sqlgo/internal/config"
)

// connForm is the add/edit form for a single Connection. Fields are laid
// out one per row inside a centered box; Tab / Shift+Tab / Up / Down move
// between fields, Enter on the last field (or Ctrl+S anywhere) saves.
type connForm struct {
	title  string
	fields []formField
	active int
	status string
	// originalName is the Name of the connection being edited. Empty for
	// adds. On save it's passed to store.SaveConnection as the oldName so
	// a rename propagates atomically.
	originalName string
}

type formField struct {
	label string
	in    *input
	mask  bool
}

// newConnForm builds a form. If c is nil the form is pre-populated with
// dev-friendly defaults that match compose.yaml. On edit, originalName
// is captured so a rename can be passed to store.SaveConnection as the
// oldName.
func newConnForm(title string, c *config.Connection) *connForm {
	f := &connForm{
		title: title,
		fields: []formField{
			{label: "Name", in: newInput("")},
			{label: "Driver", in: newInput("mssql")},
			{label: "Host", in: newInput("localhost")},
			{label: "Port", in: newInput("11433")},
			{label: "User", in: newInput("sa")},
			{label: "Password", in: newInput(""), mask: true},
			{label: "Database", in: newInput("")},
		},
	}
	if c != nil {
		f.originalName = c.Name
		f.fields[0].in.SetString(c.Name)
		f.fields[1].in.SetString(c.Driver)
		f.fields[2].in.SetString(c.Host)
		f.fields[3].in.SetString(strconv.Itoa(c.Port))
		f.fields[4].in.SetString(c.User)
		f.fields[5].in.SetString(c.Password)
		f.fields[6].in.SetString(c.Database)
	}
	return f
}

// toConnection converts the current form values into a config.Connection.
// Returns an error with a human-readable reason if required fields are
// missing or the port isn't a number.
func (f *connForm) toConnection() (config.Connection, error) {
	name := strings.TrimSpace(f.fields[0].in.String())
	driver := strings.TrimSpace(f.fields[1].in.String())
	host := strings.TrimSpace(f.fields[2].in.String())
	portStr := strings.TrimSpace(f.fields[3].in.String())
	user := strings.TrimSpace(f.fields[4].in.String())
	password := f.fields[5].in.String()
	database := strings.TrimSpace(f.fields[6].in.String())

	if name == "" {
		return config.Connection{}, errSimple("name is required")
	}
	if driver == "" {
		return config.Connection{}, errSimple("driver is required")
	}
	if host == "" {
		return config.Connection{}, errSimple("host is required")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return config.Connection{}, errSimple("port must be 1..65535")
	}

	return config.Connection{
		Name:     name,
		Driver:   driver,
		Host:     host,
		Port:     port,
		User:     user,
		Password: password,
		Database: database,
	}, nil
}

func (f *connForm) nextField() {
	f.active = (f.active + 1) % len(f.fields)
}

func (f *connForm) prevField() {
	f.active--
	if f.active < 0 {
		f.active = len(f.fields) - 1
	}
}

// handle processes a key in the form. Returns (saved, submit) where saved
// is the parsed Connection if the user submitted, and submit is true when
// the caller should act on it. On errors the status line is updated and
// submit is false.
func (f *connForm) handle(k Key) (config.Connection, bool) {
	// Navigation / submit keys first.
	switch k.Kind {
	case KeyTab, KeyDown:
		f.nextField()
		return config.Connection{}, false
	case KeyBackTab, KeyUp:
		f.prevField()
		return config.Connection{}, false
	case KeyEnter:
		c, err := f.toConnection()
		if err != nil {
			f.status = err.Error()
			return config.Connection{}, false
		}
		return c, true
	}
	if k.Ctrl && k.Rune == 's' {
		c, err := f.toConnection()
		if err != nil {
			f.status = err.Error()
			return config.Connection{}, false
		}
		return c, true
	}
	// Delegate to the active field.
	f.fields[f.active].in.handle(k)
	return config.Connection{}, false
}

func (f *connForm) draw(s *cellbuf, termW, termH int) {
	labelW := 10
	valueW := 40
	boxW := labelW + valueW + 6
	if boxW > termW-4 {
		boxW = termW - 4
	}
	boxH := len(f.fields) + 8
	if boxH > termH-4 {
		boxH = termH - 4
	}
	row := (termH - boxH) / 2
	col := (termW - boxW) / 2
	if row < 1 {
		row = 1
	}
	if col < 1 {
		col = 1
	}
	r := rect{row: row, col: col, w: boxW, h: boxH}
	// Blank the overlay's footprint so the main view behind it doesn't
	// bleed through on cells this form doesn't explicitly draw to.
	s.fillRect(r)
	drawFrame(s, r, f.title, true)

	innerCol := col + 2
	fieldTop := row + 2

	for i, field := range f.fields {
		lineRow := fieldTop + i
		// Label
		label := field.label + ":"
		if i == f.active {
			s.setFg(colorBorderFocused)
		} else {
			s.setFg(colorTitleUnfocused)
		}
		s.writeAt(lineRow, innerCol, padRightString(label, labelW))
		s.resetStyle()

		// Value. Masked fields render as asterisks.
		val := field.in.String()
		if field.mask {
			val = strings.Repeat("*", len([]rune(val)))
		}
		vCol := innerCol + labelW + 2
		maxVal := valueW
		if maxVal < 1 {
			maxVal = 1
		}
		// Show as much of the value as fits.
		rs := []rune(val)
		if len(rs) > maxVal {
			rs = rs[len(rs)-maxVal:]
		}
		s.writeAt(lineRow, vCol, string(rs))

		if i == f.active {
			// Place cursor at end of visible segment.
			cursorCol := vCol + len(rs)
			if cursorCol > vCol+maxVal {
				cursorCol = vCol + maxVal
			}
			s.placeCursor(lineRow, cursorCol)
		}
	}

	// Transient status line inside the box (e.g. "port must be 1..65535").
	// Key hints live in the bottom footer via Hints().
	if f.status != "" {
		s.setFg(colorBorderFocused)
		status := f.status
		if len(status) > boxW-4 {
			status = status[:boxW-4]
		}
		s.writeAt(r.row+r.h-2, innerCol, status)
		s.resetStyle()
	}
}

func padRightString(s string, w int) string {
	for len([]rune(s)) < w {
		s += " "
	}
	return s
}

// errSimple is a tiny error type so we don't have to import "errors" or
// "fmt.Errorf" for a single-string message.
type simpleErr string

func (e simpleErr) Error() string { return string(e) }

func errSimple(s string) error { return simpleErr(s) }

// formLayer adapts connForm to the Layer interface.
type formLayer struct {
	f *connForm
}

func newFormLayer(title string, c *config.Connection) *formLayer {
	return &formLayer{f: newConnForm(title, c)}
}

func (fl *formLayer) Draw(a *app, c *cellbuf) {
	fl.f.draw(c, a.term.width, a.term.height)
}

func (fl *formLayer) HandleKey(a *app, k Key) {
	if k.Kind == KeyEsc {
		a.popLayer()
		return
	}
	c, submit := fl.f.handle(k)
	if !submit {
		return
	}
	// Persist via the store. Passing originalName as the oldName lets
	// SaveConnection handle a rename atomically (delete-then-insert
	// inside a single tx) so the list never shows two rows mid-save.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.store.SaveConnection(ctx, fl.f.originalName, c); err != nil {
		fl.f.status = "save failed: " + err.Error()
		return
	}
	if err := a.refreshConnections(); err != nil {
		fl.f.status = "refresh failed: " + err.Error()
		return
	}
	// Pop the form and notify the picker underneath.
	a.popLayer()
	if pl, ok := a.topLayer().(*pickerLayer); ok {
		pl.setStatus("saved")
	}
}

// Hints builds the footer hint line for the add/edit form. Save is only
// shown when the fields actually parse; otherwise Enter wouldn't advance.
func (fl *formLayer) Hints(a *app) string {
	_ = a
	_, canSave := fl.f.toConnection()
	return joinHints(
		"Tab/Dn=next",
		"Shift+Tab/Up=prev",
		hintIf(canSave == nil, "Enter/Ctrl+S=save"),
		"Esc=cancel",
	)
}
