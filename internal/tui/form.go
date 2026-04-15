package tui

import (
	"context"
	"strconv"
	"strings"

	"github.com/Nulifyer/sqlgo/internal/config"
	"github.com/Nulifyer/sqlgo/internal/db"
)

// connForm is the add/edit form. Layout: fixed core fields, then
// engine-specific options, then SSH block. Tab/Up/Down to move,
// Enter/Ctrl+S to save. Driver cycle rebuilds the engine block.
type connForm struct {
	title        string
	originalName string // edit target; passed to SaveConnection as oldName

	fixed        []formField
	driverNames  []string // from db.Registered(); drives the Driver picker
	driverIdx    int
	driverChosen bool // false for new forms until the driver picker commits
	engine       []formField
	ssh          []formField

	active int
	status string
}

type formField struct {
	label string
	in    *input
	mask  bool
	// values, when non-nil, constrains the field to a fixed set.
	// Renders as a cycler; Left/Right steps; typing is swallowed.
	// Imported out-of-set values jump to values[0] on first press.
	values []string
}

func (ff *formField) isCycler() bool { return len(ff.values) > 0 }

// Fixed field indices.
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
	names := db.Registered()
	if len(names) == 0 {
		// Fallback so the form still renders if no drivers are registered
		// (tests, mis-wired builds). Matches the pre-C1 default.
		names = []string{"mssql"}
	}
	chosen := c != nil && c.Driver != ""
	driver := ""
	if chosen {
		driver = c.Driver
	}
	idx := 0
	var spec engineSpec
	if chosen {
		idx = driverIndex(names, driver)
		spec = engineSpecFor(names[idx])
	}

	f := &connForm{
		title:        title,
		driverNames:  names,
		driverIdx:    idx,
		driverChosen: chosen,
	}
	f.fixed = make([]formField, coreCount)
	f.fixed[coreName] = formField{label: "Name", in: newInput("")}
	f.fixed[coreDriver] = formField{label: "Driver", in: newInput(spec.driver)}
	f.fixed[coreHost] = formField{label: "Host", in: newInput("")}
	f.fixed[corePort] = formField{label: "Port", in: newInput("")}
	f.fixed[coreUser] = formField{label: "User", in: newInput("")}
	f.fixed[corePassword] = formField{label: "Password", in: newInput(""), mask: true}
	f.fixed[coreDatabase] = formField{label: "Database", in: newInput("")}
	if chosen {
		f.fixed[coreHost].in.SetString("localhost")
		f.fixed[corePort].in.SetString(strconv.Itoa(spec.defaultPort))
		f.fixed[coreUser].in.SetString(spec.defaultUser)
		f.engine = buildEngineFields(spec, nil)
	}
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
	// New forms start on the driver row so the picker is one keypress away.
	// With !chosen, allFields() collapses to a single driver row at index 0.
	if !chosen {
		f.active = 0
	}
	return f
}

// buildEngineFields turns spec.fields into formFields pre-filled
// from opts. Cycler values are copied through.
func buildEngineFields(spec engineSpec, opts map[string]string) []formField {
	out := make([]formField, 0, len(spec.fields))
	for _, opt := range spec.fields {
		in := newInput("")
		if opts != nil {
			if v, ok := opts[opt.key]; ok {
				in.SetString(v)
			}
		}
		out = append(out, formField{
			label:  opt.label,
			in:     in,
			mask:   opt.mask,
			values: opt.values,
		})
	}
	return out
}

// buildSSHFields is the SSH block. Password/key both shown;
// key wins at dial time if set.
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

// allFields returns all fields flat: fixed, engine, ssh.
// Until a driver is picked only the driver row is visible so the rest
// of the form can't be edited against unknown defaults.
func (f *connForm) allFields() []*formField {
	if !f.driverChosen {
		return []*formField{&f.fixed[coreDriver]}
	}
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

func (f *connForm) fixedLen() int {
	if !f.driverChosen {
		return 1
	}
	return len(f.fixed)
}
func (f *connForm) engineLen() int {
	if !f.driverChosen {
		return 0
	}
	return len(f.engine)
}
func (f *connForm) sshLen() int {
	if !f.driverChosen {
		return 0
	}
	return len(f.ssh)
}

// onDriverRow reports whether the active field is the driver selector
// row, regardless of whether other fields are currently gated.
func (f *connForm) onDriverRow() bool {
	ff := f.activeField()
	return ff != nil && ff == &f.fixed[coreDriver]
}

// isFieldRequired reports whether a form field must be non-empty
// per the active driver's engineSpec. Used to render the "*" marker
// and kept in sync with toConnection's validation.
func (f *connForm) isFieldRequired(field *formField) bool {
	spec := engineSpecFor(f.fixed[coreDriver].in.String())
	for i := range f.fixed {
		if &f.fixed[i] == field {
			return spec.coreRequired(i)
		}
	}
	for i := range f.engine {
		if &f.engine[i] == field {
			if i < len(spec.fields) {
				return spec.fields[i].required
			}
			return false
		}
	}
	return false
}

func (f *connForm) activeField() *formField {
	fields := f.allFields()
	if f.active < 0 || f.active >= len(fields) {
		return nil
	}
	return fields[f.active]
}

// onEngineCycler: active field is a constrained engine option.
// Driver row handled separately by onDriverRow (picker, not cycler).
func (f *connForm) onEngineCycler() bool {
	if f.onDriverRow() {
		return false
	}
	ff := f.activeField()
	return ff != nil && ff.isCycler()
}

// cycleFieldValue steps a cycler by delta with wrap-around.
// Unknown current values drop to values[0] on first press.
func cycleFieldValue(ff *formField, delta int) {
	if !ff.isCycler() {
		return
	}
	cur := ff.in.String()
	idx := -1
	for i, v := range ff.values {
		if v == cur {
			idx = i
			break
		}
	}
	if idx < 0 {
		ff.in.SetString(ff.values[0])
		return
	}
	n := len(ff.values)
	next := (idx + delta + n) % n
	ff.in.SetString(ff.values[next])
}

// setDriver applies an absolute driver choice from the picker. On the
// first pick it fills in defaults and flips driverChosen so the rest
// of the form renders; on a later re-pick it behaves like cycleDriver
// to preserve user-typed values.
func (f *connForm) setDriver(name string) {
	newIdx := driverIndex(f.driverNames, name)
	if !f.driverChosen {
		spec := engineSpecFor(f.driverNames[newIdx])
		f.driverIdx = newIdx
		f.driverChosen = true
		f.fixed[coreDriver].in.SetString(spec.driver)
		if f.fixed[coreHost].in.String() == "" {
			f.fixed[coreHost].in.SetString("localhost")
		}
		if f.fixed[corePort].in.String() == "" {
			f.fixed[corePort].in.SetString(strconv.Itoa(spec.defaultPort))
		}
		if f.fixed[coreUser].in.String() == "" {
			f.fixed[coreUser].in.SetString(spec.defaultUser)
		}
		f.engine = buildEngineFields(spec, nil)
		return
	}
	if newIdx == f.driverIdx {
		return
	}
	f.cycleDriver(newIdx - f.driverIdx)
}

// cycleDriver swaps the engine spec, rebuilds engine fields
// preserving shared-key values, and resets port/user to the new
// defaults only if they still match the prior defaults.
func (f *connForm) cycleDriver(delta int) {
	n := len(f.driverNames)
	if n == 0 {
		return
	}
	newIdx := (f.driverIdx + delta + n) % n
	if newIdx == f.driverIdx {
		return
	}
	prior := map[string]string{}
	priorSpec := engineSpecFor(f.driverNames[f.driverIdx])
	for i, opt := range priorSpec.fields {
		if i < len(f.engine) {
			prior[opt.key] = f.engine[i].in.String()
		}
	}
	f.driverIdx = newIdx
	newSpec := engineSpecFor(f.driverNames[newIdx])
	f.engine = buildEngineFields(newSpec, prior)
	f.fixed[coreDriver].in.SetString(newSpec.driver)
	priorPort := strconv.Itoa(priorSpec.defaultPort)
	if f.fixed[corePort].in.String() == priorPort || f.fixed[corePort].in.String() == "" || f.fixed[corePort].in.String() == "0" {
		f.fixed[corePort].in.SetString(strconv.Itoa(newSpec.defaultPort))
	}
	if f.fixed[coreUser].in.String() == priorSpec.defaultUser {
		f.fixed[coreUser].in.SetString(newSpec.defaultUser)
	}
}

// toConnection validates the form into a config.Connection.
func (f *connForm) toConnection() (config.Connection, error) {
	name := strings.TrimSpace(f.fixed[coreName].in.String())
	driver := strings.TrimSpace(f.fixed[coreDriver].in.String())
	host := strings.TrimSpace(f.fixed[coreHost].in.String())
	portStr := strings.TrimSpace(f.fixed[corePort].in.String())
	user := strings.TrimSpace(f.fixed[coreUser].in.String())
	password := f.fixed[corePassword].in.String()
	database := strings.TrimSpace(f.fixed[coreDatabase].in.String())

	spec := engineSpecFor(driver)
	// Required-field validation per driver. Password is not trimmed
	// (leading/trailing whitespace can be meaningful) — only empty
	// strings fail. Iterate in declaration order so the first missing
	// field named is also the first visible one on the form.
	values := [coreCount]string{
		coreName:     name,
		coreDriver:   driver,
		coreHost:     host,
		corePort:     portStr,
		coreUser:     user,
		corePassword: password,
		coreDatabase: database,
	}
	for idx := 0; idx < coreCount; idx++ {
		if !spec.coreRequired(idx) {
			continue
		}
		if values[idx] == "" {
			return config.Connection{}, errSimple(coreLabels[idx] + " is required")
		}
	}
	port := 0
	if portStr != "" {
		p, err := strconv.Atoi(portStr)
		if err != nil || p < 0 || p > maxTCPPort {
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
	// values are dropped so save doesn't churn the JSON. Options
	// flagged required block save when blank.
	opts := map[string]string{}
	for i, opt := range spec.fields {
		if i >= len(f.engine) {
			break
		}
		v := strings.TrimSpace(f.engine[i].in.String())
		if opt.required && v == "" {
			return config.Connection{}, errSimple(opt.label + " is required")
		}
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
		if err != nil || p < 1 || p > maxTCPPort {
			return config.Connection{}, errSimple("ssh port must be 1..65535")
		}
		ssh.Port = p
	}
	if ssh.Host != "" && ssh.Port == 0 {
		ssh.Port = defaultSSHPort
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

// handle returns (c, submit) where submit=true means persist c.
func (f *connForm) handle(k Key) (config.Connection, bool) {
	switch k.Kind {
	case KeyTab, KeyDown:
		f.nextField()
		return config.Connection{}, false
	case KeyBackTab, KeyUp:
		f.prevField()
		return config.Connection{}, false
	case KeyLeft:
		if f.onEngineCycler() {
			cycleFieldValue(f.activeField(), -1)
			return config.Connection{}, false
		}
	case KeyRight:
		if f.onEngineCycler() {
			cycleFieldValue(f.activeField(), 1)
			return config.Connection{}, false
		}
	case KeyEnter:
		// Driver row Enter is intercepted by formLayer to push the picker;
		// if we get it here something else routed the key, no-op.
		if f.onDriverRow() {
			return config.Connection{}, false
		}
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
	// Driver row (picker) and cycler rows swallow printable chars.
	if f.onDriverRow() || f.onEngineCycler() {
		return config.Connection{}, false
	}
	fields := f.allFields()
	if f.active >= 0 && f.active < len(fields) {
		fields[f.active].in.handle(k)
	}
	return config.Connection{}, false
}

func (f *connForm) draw(s *cellbuf, termW, termH int) {
	labelW := 20
	valueW := 44
	boxW := labelW + valueW + 6
	if boxW > termW-dialogMargin {
		boxW = termW - dialogMargin
	}

	fields := f.allFields()
	boxH := len(fields) + 8 // one row per field + chrome
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

	// Scroll active field into view on short terminals.
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
		label := field.label
		if f.isFieldRequired(field) {
			label += " *"
		}
		label += ":"
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
		isDriverRow := field == &f.fixed[coreDriver]
		if isDriverRow {
			if f.driverChosen {
				val = "[ " + engineSpecFor(f.driverNames[f.driverIdx]).label + " ]"
			} else {
				val = "[ select driver... ]"
			}
		} else if field.isCycler() {
			display := val
			if display == "" {
				display = "(default)"
			}
			val = "< " + display + " >"
		}

		vCol := innerCol + labelW + 2
		maxVal := innerW - labelW - 2
		if maxVal < 1 {
			maxVal = 1
		}
		rs := []rune(val)
		// Scroll the visible window so the cursor stays in view. For
		// driver/cycler rows there's no editing cursor, so just tail-slice.
		editable := !isDriverRow && !field.isCycler()
		cursorOffset := 0
		if editable {
			cur := field.in.cur
			if cur > len(rs) {
				cur = len(rs)
			}
			start := 0
			if len(rs) > maxVal {
				// Keep cursor visible: shift window so cur sits inside [start, start+maxVal].
				start = cur - maxVal
				if start < 0 {
					start = 0
				}
				if start+maxVal > len(rs) {
					start = len(rs) - maxVal
				}
				rs = rs[start : start+maxVal]
			}
			cursorOffset = cur - start
		} else if len(rs) > maxVal {
			rs = rs[len(rs)-maxVal:]
		}
		s.writeAt(lineRow, vCol, string(rs))

		if i == f.active && editable {
			cursorCol := vCol + cursorOffset
			if cursorCol > vCol+maxVal {
				cursorCol = vCol + maxVal
			}
			s.placeCursor(lineRow, cursorCol)
		}
		y++
	}

	if f.status != "" {
		lines := wrapText(f.status, innerW)
		if len(lines) > 4 {
			lines = lines[:4]
		}
		s.setFg(colorBorderFocused)
		startRow := r.row + r.h - 1 - len(lines)
		for i, line := range lines {
			s.writeAt(startRow+i, innerCol, line)
		}
		s.resetStyle()
	}
}

func padRightString(s string, w int) string {
	for len([]rune(s)) < w {
		s += " "
	}
	return s
}

// simpleErr is a string-only error type. Avoids importing errors
// or fmt.Errorf for the form's one-line messages.
type simpleErr string

func (e simpleErr) Error() string { return string(e) }

func errSimple(s string) error { return simpleErr(s) }

// formLayer adapts connForm to Layer.
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
	// Driver row: Enter opens the searchable picker instead of
	// saving. Any non-chosen state forces this path so the form
	// can't be submitted without a driver.
	if fl.f.onDriverRow() && k.Kind == KeyEnter {
		a.pushLayer(newDriverPickerLayer(fl.f.driverNames, func(name string) {
			fl.f.setDriver(name)
		}))
		return
	}
	// Pre-handle probe hotkeys before the form consumes the key. Only
	// fire once a driver is chosen so the rest of the form actually has
	// host/port to probe.
	if fl.f.driverChosen && k.Ctrl && k.Kind == KeyRune {
		switch k.Rune {
		case 't':
			fl.startTestNetwork(a)
			return
		case 'l':
			fl.startTestAuth(a)
			return
		}
	}
	c, submit := fl.f.handle(k)
	if !submit {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), storeReadTimeout)
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

// Hints: Save only shown when toConnection parses.
func (fl *formLayer) Hints(a *app) string {
	_ = a
	_, canSave := fl.f.toConnection()
	driver := ""
	cycler := ""
	if fl.f.onDriverRow() {
		driver = "Enter=pick driver"
	} else if fl.f.onEngineCycler() {
		cycler = "Lt/Rt=cycle"
	}
	return joinHints(
		"Tab/Dn=next",
		"Shift+Tab/Up=prev",
		driver,
		cycler,
		hintIf(canSave == nil && fl.f.driverChosen, "Ctrl+S=save"),
		hintIf(fl.f.driverChosen, "Ctrl+T=test-net"),
		hintIf(canSave == nil && fl.f.driverChosen, "Ctrl+L=test-auth"),
		"Esc=cancel",
	)
}
