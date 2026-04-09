package tui

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/Nulifyer/sqlgo/internal/config"
)

// connForm is the add/edit form for a single Connection. Fields are
// laid out one per row inside a centered box; Tab / Shift+Tab / Up /
// Down move between fields, Enter on the last field (or Ctrl+S
// anywhere) saves.
//
// Field layout is composed at runtime: the fixed core fields
// (Name/Driver/Host/Port/User/Password/Database) sit on top, followed
// by engine-specific options (from engineSpecs), followed by the SSH
// tunnel block. Changing the Driver cycler rebuilds the engine-specific
// section while preserving the values of any matching keys.
type connForm struct {
	title        string
	originalName string // non-empty on edit; passed to SaveConnection as oldName

	fixed     []formField // Name..Database core fields
	driverIdx int         // index into engineSpecs for the current driver
	engine    []formField // engine-specific options (one per engineSpec.fields entry)
	ssh       []formField // SSH tunnel fields

	active int // index across the flattened field list (fixedLen + engineLen + sshLen)
	status string
}

type formField struct {
	label string
	in    *input
	mask  bool
}

// fixed core field indices. Kept as constants so the toConnection
// reader doesn't hard-code magic numbers.
const (
	coreName = iota
	coreDriver
	coreHost
	corePort
	coreUser
	corePassword
	coreDatabase
	coreCount
)

func newConnForm(title string, c *config.Connection) *connForm {
	// Pick a starting spec from c (edit) or the first registered entry
	// (add). This drives defaultPort / defaultUser and which
	// engine-specific fields exist.
	driver := "mssql"
	if c != nil && c.Driver != "" {
		driver = c.Driver
	}
	idx := engineSpecIndex(driver)
	spec := engineSpecs[idx]

	f := &connForm{
		title:     title,
		driverIdx: idx,
	}
	f.fixed = make([]formField, coreCount)
	f.fixed[coreName] = formField{label: "Name", in: newInput("")}
	f.fixed[coreDriver] = formField{label: "Driver", in: newInput(spec.driver)}
	f.fixed[coreHost] = formField{label: "Host", in: newInput("localhost")}
	f.fixed[corePort] = formField{label: "Port", in: newInput(strconv.Itoa(spec.defaultPort))}
	f.fixed[coreUser] = formField{label: "User", in: newInput(spec.defaultUser)}
	f.fixed[corePassword] = formField{label: "Password", in: newInput(""), mask: true}
	f.fixed[coreDatabase] = formField{label: "Database", in: newInput("")}
	f.engine = buildEngineFields(spec, nil)
	f.ssh = buildSSHFields(config.SSHTunnel{})

	if c != nil {
		f.originalName = c.Name
		f.fixed[coreName].in.SetString(c.Name)
		f.fixed[coreDriver].in.SetString(c.Driver)
		f.fixed[coreHost].in.SetString(c.Host)
		f.fixed[corePort].in.SetString(strconv.Itoa(c.Port))
		f.fixed[coreUser].in.SetString(c.User)
		f.fixed[corePassword].in.SetString(c.Password)
		f.fixed[coreDatabase].in.SetString(c.Database)
		f.engine = buildEngineFields(spec, c.Options)
		f.ssh = buildSSHFields(c.SSH)
	}
	return f
}

// buildEngineFields turns the spec's declared options into formFields,
// pre-filling each value from the provided options map.
func buildEngineFields(spec engineSpec, opts map[string]string) []formField {
	out := make([]formField, 0, len(spec.fields))
	for _, opt := range spec.fields {
		in := newInput("")
		if opts != nil {
			if v, ok := opts[opt.key]; ok {
				in.SetString(v)
			}
		}
		out = append(out, formField{label: opt.label, in: in, mask: opt.mask})
	}
	return out
}

// buildSSHFields is the SSH tunnel block. Password and key path are
// mutually exclusive at dial time (key wins) but the form surfaces both
// so the user can store either.
func buildSSHFields(t config.SSHTunnel) []formField {
	port := ""
	if t.Port != 0 {
		port = strconv.Itoa(t.Port)
	}
	return []formField{
		{label: "SSH host", in: newInput(t.Host)},
		{label: "SSH port", in: newInput(port)},
		{label: "SSH user", in: newInput(t.User)},
		{label: "SSH pass", in: newInput(t.Password), mask: true},
		{label: "SSH key", in: newInput(t.KeyPath)},
	}
}

// allFields returns every field in rendering order: fixed core first,
// then engine options, then SSH tunnel. Used by navigation and the
// draw loop so the active index maps 1:1 to a single flat list.
func (f *connForm) allFields() []*formField {
	out := make([]*formField, 0, len(f.fixed)+len(f.engine)+len(f.ssh))
	for i := range f.fixed {
		out = append(out, &f.fixed[i])
	}
	for i := range f.engine {
		out = append(out, &f.engine[i])
	}
	for i := range f.ssh {
		out = append(out, &f.ssh[i])
	}
	return out
}

// fixedLen / engineLen / sshLen give the sub-list sizes the navigation
// code uses to decide whether the cursor is on the Driver cycler.
func (f *connForm) fixedLen() int  { return len(f.fixed) }
func (f *connForm) engineLen() int { return len(f.engine) }
func (f *connForm) sshLen() int    { return len(f.ssh) }

// onDriverCycler reports whether the active field is the Driver row,
// which has special cycler behavior (Left/Right change the selected
// engine and rebuild the engine-specific block).
func (f *connForm) onDriverCycler() bool {
	return f.active == coreDriver
}

// cycleDriver replaces the current engine spec with the one at idx,
// rebuilds the engine-specific field block preserving any values that
// share keys across engines, and updates the Driver input.
func (f *connForm) cycleDriver(delta int) {
	n := len(engineSpecs)
	if n == 0 {
		return
	}
	newIdx := (f.driverIdx + delta + n) % n
	if newIdx == f.driverIdx {
		return
	}
	// Capture current option values into a map so any keys shared
	// across engine specs (e.g. a hypothetical "charset" on two
	// drivers) round-trip without the user retyping.
	prior := map[string]string{}
	priorSpec := engineSpecs[f.driverIdx]
	for i, opt := range priorSpec.fields {
		if i < len(f.engine) {
			prior[opt.key] = f.engine[i].in.String()
		}
	}
	f.driverIdx = newIdx
	newSpec := engineSpecs[newIdx]
	f.engine = buildEngineFields(newSpec, prior)
	f.fixed[coreDriver].in.SetString(newSpec.driver)
	// Reset port/user to the new engine's defaults only if they still
	// match the prior engine's defaults -- preserves any custom value
	// the user typed.
	priorPort := strconv.Itoa(priorSpec.defaultPort)
	if f.fixed[corePort].in.String() == priorPort || f.fixed[corePort].in.String() == "" || f.fixed[corePort].in.String() == "0" {
		f.fixed[corePort].in.SetString(strconv.Itoa(newSpec.defaultPort))
	}
	if f.fixed[coreUser].in.String() == priorSpec.defaultUser {
		f.fixed[coreUser].in.SetString(newSpec.defaultUser)
	}
}

// toConnection reads the form values into a config.Connection. Returns
// an error with a human-readable reason if required fields are missing
// or the port isn't a number.
func (f *connForm) toConnection() (config.Connection, error) {
	name := strings.TrimSpace(f.fixed[coreName].in.String())
	driver := strings.TrimSpace(f.fixed[coreDriver].in.String())
	host := strings.TrimSpace(f.fixed[coreHost].in.String())
	portStr := strings.TrimSpace(f.fixed[corePort].in.String())
	user := strings.TrimSpace(f.fixed[coreUser].in.String())
	password := f.fixed[corePassword].in.String()
	database := strings.TrimSpace(f.fixed[coreDatabase].in.String())

	if name == "" {
		return config.Connection{}, errSimple("name is required")
	}
	if driver == "" {
		return config.Connection{}, errSimple("driver is required")
	}
	// sqlite uses a file path in Database and has no host/port, so we
	// relax the host requirement when the current engine explicitly
	// declares defaultPort == 0.
	spec := engineSpecFor(driver)
	if host == "" && spec.defaultPort != 0 {
		return config.Connection{}, errSimple("host is required")
	}
	port := 0
	if portStr != "" {
		p, err := strconv.Atoi(portStr)
		if err != nil || p < 0 || p > 65535 {
			return config.Connection{}, errSimple("port must be 0..65535")
		}
		port = p
	}

	out := config.Connection{
		Name:     name,
		Driver:   driver,
		Host:     host,
		Port:     port,
		User:     user,
		Password: password,
		Database: database,
	}

	// Engine-specific options collapse into the Options map. Empty
	// values are dropped so save doesn't churn the JSON.
	opts := map[string]string{}
	for i, opt := range spec.fields {
		if i >= len(f.engine) {
			break
		}
		v := strings.TrimSpace(f.engine[i].in.String())
		if v != "" {
			opts[opt.key] = v
		}
	}
	if len(opts) > 0 {
		out.Options = opts
	}

	// SSH tunnel.
	ssh := config.SSHTunnel{
		Host:     strings.TrimSpace(f.ssh[0].in.String()),
		User:     strings.TrimSpace(f.ssh[2].in.String()),
		Password: f.ssh[3].in.String(),
		KeyPath:  strings.TrimSpace(f.ssh[4].in.String()),
	}
	if s := strings.TrimSpace(f.ssh[1].in.String()); s != "" {
		p, err := strconv.Atoi(s)
		if err != nil || p < 1 || p > 65535 {
			return config.Connection{}, errSimple("ssh port must be 1..65535")
		}
		ssh.Port = p
	}
	if ssh.Host != "" && ssh.Port == 0 {
		ssh.Port = 22
	}
	out.SSH = ssh

	return out, nil
}

func (f *connForm) nextField() {
	n := f.fixedLen() + f.engineLen() + f.sshLen()
	f.active = (f.active + 1) % n
}

func (f *connForm) prevField() {
	n := f.fixedLen() + f.engineLen() + f.sshLen()
	f.active = (f.active - 1 + n) % n
}

// handle processes a key in the form. Returns (c, submit) where submit
// is true when the caller should persist the connection.
func (f *connForm) handle(k Key) (config.Connection, bool) {
	switch k.Kind {
	case KeyTab, KeyDown:
		f.nextField()
		return config.Connection{}, false
	case KeyBackTab, KeyUp:
		f.prevField()
		return config.Connection{}, false
	case KeyLeft:
		if f.onDriverCycler() {
			f.cycleDriver(-1)
			return config.Connection{}, false
		}
	case KeyRight:
		if f.onDriverCycler() {
			f.cycleDriver(1)
			return config.Connection{}, false
		}
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
	// Driver cycler swallows printable chars so the user doesn't
	// accidentally type into a non-editable row.
	if f.onDriverCycler() {
		return config.Connection{}, false
	}
	fields := f.allFields()
	if f.active >= 0 && f.active < len(fields) {
		fields[f.active].in.handle(k)
	}
	return config.Connection{}, false
}

func (f *connForm) draw(s *cellbuf, termW, termH int) {
	labelW := 16
	valueW := 44
	boxW := labelW + valueW + 6
	if boxW > termW-4 {
		boxW = termW - 4
	}

	fields := f.allFields()
	// Dynamic height: one row per field + title + SSH section header
	// + status + padding.
	boxH := len(fields) + 8
	if boxH > termH-2 {
		boxH = termH - 2
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
	s.fillRect(r)
	drawFrame(s, r, f.title, true)

	innerCol := col + 2
	innerW := boxW - 4
	fieldTop := row + 2

	// Scroll the visible window so the active field stays in view when
	// the form is taller than the terminal.
	bodyH := boxH - 4
	scroll := 0
	if f.active >= bodyH {
		scroll = f.active - bodyH + 1
	}
	if scroll < 0 {
		scroll = 0
	}

	y := 0
	for i, field := range fields {
		if i < scroll {
			continue
		}
		if y >= bodyH {
			break
		}
		lineRow := fieldTop + y
		label := field.label + ":"
		if i == f.active {
			s.setFg(colorBorderFocused)
		} else {
			s.setFg(colorTitleUnfocused)
		}
		s.writeAt(lineRow, innerCol, padRightString(label, labelW))
		s.resetStyle()

		val := field.in.String()
		if field.mask {
			val = strings.Repeat("*", len([]rune(val)))
		}
		// Driver row renders as "< current >" with cycler hints
		// instead of a plain editable field.
		if i == coreDriver {
			val = "< " + engineSpecs[f.driverIdx].label + " >"
		}

		vCol := innerCol + labelW + 2
		maxVal := innerW - labelW - 2
		if maxVal < 1 {
			maxVal = 1
		}
		rs := []rune(val)
		if len(rs) > maxVal {
			rs = rs[len(rs)-maxVal:]
		}
		s.writeAt(lineRow, vCol, string(rs))

		if i == f.active && i != coreDriver {
			cursorCol := vCol + len(rs)
			if cursorCol > vCol+maxVal {
				cursorCol = vCol + maxVal
			}
			s.placeCursor(lineRow, cursorCol)
		}
		y++
	}

	if f.status != "" {
		s.setFg(colorBorderFocused)
		s.writeAt(r.row+r.h-2, innerCol, truncate(f.status, innerW))
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
	// Persist via the app helper, which routes passwords through the
	// OS keyring when available.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	usedKeyring, err := a.persistConnection(ctx, fl.f.originalName, c)
	if err != nil {
		fl.f.status = "save failed: " + err.Error()
		return
	}
	if err := a.refreshConnections(); err != nil {
		fl.f.status = "refresh failed: " + err.Error()
		return
	}
	a.popLayer()
	if pl, ok := a.topLayer().(*pickerLayer); ok {
		if usedKeyring {
			pl.setStatus("saved (keyring)")
		} else if a.secretsAvailable {
			pl.setStatus("saved (keyring write failed, stored plaintext)")
		} else {
			pl.setStatus("saved (no keyring; plaintext)")
		}
	}
}

// Hints builds the footer hint line for the add/edit form. Save is
// only shown when the fields actually parse; otherwise Enter wouldn't
// advance.
func (fl *formLayer) Hints(a *app) string {
	_ = a
	_, canSave := fl.f.toConnection()
	cycler := ""
	if fl.f.onDriverCycler() {
		cycler = "Lt/Rt=engine"
	}
	return joinHints(
		"Tab/Dn=next",
		"Shift+Tab/Up=prev",
		cycler,
		hintIf(canSave == nil, "Enter/Ctrl+S=save"),
		"Esc=cancel",
	)
}
