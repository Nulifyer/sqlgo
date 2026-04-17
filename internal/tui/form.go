package tui

import (
	"context"
	"strconv"
	"strings"

	"github.com/Nulifyer/sqlgo/internal/config"
	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/tui/widget"
)

// formField aliases widget.FormField so tests and probe code that
// poke at fields keep compiling while domain code reads idiomatically.
type formField = widget.FormField

// connForm is the add/edit form. Layout: fixed core fields, then
// engine-specific options, then SSH block. Tab/Up/Down to move,
// Enter/Ctrl+S to save. Driver cycle rebuilds the engine block.
type connForm struct {
	title        string
	originalName string // edit target; passed to SaveConnection as oldName

	fixed        []widget.FormField
	driverNames  []string // from db.Registered(); drives the Driver picker
	driverIdx    int
	driverChosen bool // false for new forms until the driver picker commits
	engine       []widget.FormField
	ssh          []widget.FormField

	// "Other..." flow: user picks dialect + transport independently.
	profileName   string
	transportName string

	active int
	status string
}

// portString returns the string form of a default port, or "" when
// the port is 0 (no default for that driver).
func portString(p int) string {
	if p == 0 {
		return ""
	}
	return strconv.Itoa(p)
}

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
		names = []string{"mssql"}
	}
	names = append(names, "other")
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
	f.fixed = make([]widget.FormField, coreCount)
	f.fixed[coreName] = widget.FormField{Label: "Name", Input: widget.NewInput("")}
	f.fixed[coreDriver] = widget.FormField{
		Label:        "Driver",
		Input:        widget.NewInput(spec.driver),
		SwallowInput: true,
		Display: func() string {
			if !f.driverChosen {
				return "[ select driver... ]"
			}
			lbl := engineSpecFor(f.driverNames[f.driverIdx]).label
			if f.profileName != "" && f.transportName != "" {
				lbl = f.profileName + " / " + f.transportName
			}
			return "[ " + lbl + " ]"
		},
	}
	f.fixed[coreHost] = widget.FormField{Label: "Host", Input: widget.NewInput("")}
	f.fixed[corePort] = widget.FormField{Label: "Port", Input: widget.NewInput("")}
	f.fixed[coreUser] = widget.FormField{Label: "User", Input: widget.NewInput("")}
	f.fixed[corePassword] = widget.FormField{Label: "Password", Input: widget.NewInput(""), Mask: true}
	f.fixed[coreDatabase] = widget.FormField{Label: "Database", Input: widget.NewInput("")}
	if chosen {
		f.fixed[coreHost].Input.SetString("localhost")
		f.fixed[corePort].Input.SetString(strconv.Itoa(spec.defaultPort))
		f.fixed[coreUser].Input.SetString(spec.defaultUser)
		f.engine = buildEngineFields(spec, nil)
	}
	f.ssh = buildSSHFields(config.SSHTunnel{})

	if c != nil {
		f.originalName = c.Name
		f.profileName = c.Profile
		f.transportName = c.Transport
		f.fixed[coreName].Input.SetString(c.Name)
		f.fixed[coreDriver].Input.SetString(c.Driver)
		f.fixed[coreHost].Input.SetString(c.Host)
		f.fixed[corePort].Input.SetString(strconv.Itoa(c.Port))
		f.fixed[coreUser].Input.SetString(c.User)
		f.fixed[corePassword].Input.SetString(c.Password)
		f.fixed[coreDatabase].Input.SetString(c.Database)
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

// buildEngineFields turns spec.fields into widget.FormFields pre-filled
// from opts. Cycler values are copied through.
func buildEngineFields(spec engineSpec, opts map[string]string) []widget.FormField {
	out := make([]widget.FormField, 0, len(spec.fields))
	for _, opt := range spec.fields {
		in := widget.NewInput("")
		if opts != nil {
			if v, ok := opts[opt.key]; ok {
				in.SetString(v)
			}
		}
		out = append(out, widget.FormField{
			Label:  opt.label,
			Input:  in,
			Mask:   opt.mask,
			Values: opt.values,
		})
	}
	return out
}

// buildSSHFields is the SSH block. Password/key both shown;
// key wins at dial time if set.
func buildSSHFields(t config.SSHTunnel) []widget.FormField {
	port := ""
	if t.Port != 0 {
		port = strconv.Itoa(t.Port)
	}
	return []widget.FormField{
		{Label: "SSH host", Input: widget.NewInput(t.Host)},
		{Label: "SSH port", Input: widget.NewInput(port)},
		{Label: "SSH user", Input: widget.NewInput(t.User)},
		{Label: "SSH pass", Input: widget.NewInput(t.Password), Mask: true},
		{Label: "SSH key", Input: widget.NewInput(t.KeyPath)},
	}
}

// allFields returns all fields flat: fixed, engine, ssh.
// Until a driver is picked only the driver row is visible so the rest
// of the form can't be edited against unknown defaults.
func (f *connForm) allFields() []*widget.FormField {
	if !f.driverChosen {
		return []*widget.FormField{&f.fixed[coreDriver]}
	}
	out := make([]*widget.FormField, 0, len(f.fixed)+len(f.engine)+len(f.ssh))
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

// onDriverRow reports whether the active field is the driver selector
// row, regardless of whether other fields are currently gated.
func (f *connForm) onDriverRow() bool {
	ff := f.activeField()
	return ff != nil && ff == &f.fixed[coreDriver]
}

// isFieldRequired reports whether a form field must be non-empty
// per the active driver's engineSpec. Used to render the "*" marker
// and kept in sync with toConnection's validation.
func (f *connForm) isFieldRequired(field *widget.FormField) bool {
	spec := engineSpecFor(f.fixed[coreDriver].Input.String())
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

func (f *connForm) activeField() *widget.FormField {
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
	return ff != nil && ff.IsCycler()
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
		f.fixed[coreDriver].Input.SetString(spec.driver)
		if f.fixed[coreHost].Input.String() == "" {
			f.fixed[coreHost].Input.SetString("localhost")
		}
		if f.fixed[corePort].Input.String() == "" {
			f.fixed[corePort].Input.SetString(portString(spec.defaultPort))
		}
		if f.fixed[coreUser].Input.String() == "" {
			f.fixed[coreUser].Input.SetString(spec.defaultUser)
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
			prior[opt.key] = f.engine[i].Input.String()
		}
	}
	f.driverIdx = newIdx
	newSpec := engineSpecFor(f.driverNames[newIdx])
	f.engine = buildEngineFields(newSpec, prior)
	f.fixed[coreDriver].Input.SetString(newSpec.driver)
	priorPort := portString(priorSpec.defaultPort)
	if f.fixed[corePort].Input.String() == priorPort || f.fixed[corePort].Input.String() == "" || f.fixed[corePort].Input.String() == "0" {
		f.fixed[corePort].Input.SetString(portString(newSpec.defaultPort))
	}
	if f.fixed[coreUser].Input.String() == priorSpec.defaultUser {
		f.fixed[coreUser].Input.SetString(newSpec.defaultUser)
	}
}

// toConnection validates the form into a config.Connection.
func (f *connForm) toConnection() (config.Connection, error) {
	name := strings.TrimSpace(f.fixed[coreName].Input.String())
	driver := strings.TrimSpace(f.fixed[coreDriver].Input.String())
	host := strings.TrimSpace(f.fixed[coreHost].Input.String())
	portStr := strings.TrimSpace(f.fixed[corePort].Input.String())
	user := strings.TrimSpace(f.fixed[coreUser].Input.String())
	password := f.fixed[corePassword].Input.String()
	database := strings.TrimSpace(f.fixed[coreDatabase].Input.String())

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
		Name:      name,
		Driver:    driver,
		Host:      host,
		Port:      port,
		User:      user,
		Password:  password,
		Database:  database,
		Profile:   f.profileName,
		Transport: f.transportName,
	}

	// Engine-specific options collapse into the Options map. Empty
	// values are dropped so save doesn't churn the JSON. Options
	// flagged required block save when blank.
	opts := map[string]string{}
	for i, opt := range spec.fields {
		if i >= len(f.engine) {
			break
		}
		v := strings.TrimSpace(f.engine[i].Input.String())
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
		Host:     strings.TrimSpace(f.ssh[0].Input.String()),
		User:     strings.TrimSpace(f.ssh[2].Input.String()),
		Password: f.ssh[3].Input.String(),
		KeyPath:  strings.TrimSpace(f.ssh[4].Input.String()),
	}
	if s := strings.TrimSpace(f.ssh[1].Input.String()); s != "" {
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

// handle returns (c, submit) where submit=true means persist c.
// Keyboard behavior is delegated to widget.Form; the domain-specific
// bits left here are driver-row Enter (picker trigger, defensive no-op
// since formLayer intercepts it upstream) and submit validation.
func (f *connForm) handle(k Key) (config.Connection, bool) {
	// Defensive: formLayer should have intercepted Enter on the driver
	// row and pushed the picker before routing here.
	if f.onDriverRow() && k.Kind == KeyEnter {
		return config.Connection{}, false
	}
	wf := widget.Form{Fields: f.allFields(), Active: f.active}
	submit := wf.HandleKey(k)
	f.active = wf.Active
	if !submit {
		return config.Connection{}, false
	}
	c, err := f.toConnection()
	if err != nil {
		f.status = err.Error()
		return config.Connection{}, false
	}
	return c, true
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
	r := rect{Row: row, Col: col, W: boxW, H: boxH}
	s.FillRect(r)
	drawFrame(s, r, f.title, true)

	innerCol := col + 2
	innerW := boxW - 4
	fieldTop := row + 2
	bodyH := boxH - 4

	wf := widget.Form{Fields: fields, Active: f.active}
	wf.Draw(s, rect{Row: fieldTop, Col: innerCol, W: innerW, H: bodyH}, widget.FormDrawOpts{
		LabelW:       labelW,
		ActiveFG:     colorBorderFocused,
		InactiveFG:   colorTitleUnfocused,
		RequiredFunc: func(ff *widget.FormField) bool { return f.isFieldRequired(ff) },
	})

	if f.status != "" {
		lines := wrapText(f.status, innerW)
		if len(lines) > 4 {
			lines = lines[:4]
		}
		s.SetFg(colorBorderFocused)
		startRow := r.Row + r.H - 1 - len(lines)
		for i, line := range lines {
			s.WriteAt(startRow+i, innerCol, line)
		}
		s.ResetStyle()
	}
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
	// Driver Row: Enter opens the searchable picker instead of
	// saving. Any non-chosen state forces this path so the form
	// can't be submitted without a driver.
	if fl.f.onDriverRow() && k.Kind == KeyEnter {
		a.pushLayer(newDriverPickerLayer(fl.f.driverNames, func(name string) {
			if name == "other" {
				openOtherPicker(a, func(profile, transport string) {
					fl.f.profileName = profile
					fl.f.transportName = transport
					fl.f.setDriver("other")
					if t, ok := db.GetTransport(transport); ok {
						if fl.f.fixed[corePort].Input.String() == "" || fl.f.fixed[corePort].Input.String() == "0" {
							fl.f.fixed[corePort].Input.SetString(portString(t.DefaultPort))
						}
					}
				})
				return
			}
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
		"Ctrl+T=test-net",
		"Ctrl+L=test-auth",
		"Esc=cancel",
	)
}
