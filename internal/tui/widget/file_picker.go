// Package widget: FilePicker -- unified file browser for Save / Export /
// Open dialogs. Single-level directory listing (no recursive scan), a
// Dir text field with tilde expansion and inline path editing, a
// browse list showing ".." + dirs + files, an Input field (filename
// stem in Save mode, substring filter in Open mode), and an optional
// extension-choice cycler. Navigation uses Tab / Shift+Tab between
// fields (FocusDir -> FocusList -> FocusInput -> FocusExt), matching
// the Windows Save / Open dialog key model adapted to a TUI.
package widget

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Nulifyer/sqlgo/internal/tui/term"
)

// FilePickerMode selects the picker's UX.
//
//	ModeSaveTarget: Dir + browse list (ext-dimmed) + Name + Ext cycler.
//	               Enter on a file row prefills the Name stem and the
//	               matching Ext choice, then focuses the Name field so
//	               the user can confirm / rename before save.
//	ModeOpenSingle: Dir + browse list (ext-hard-filtered) + Search.
//	               Enter on a file row opens it.
//	ModeOpenMulti:  OpenSingle + Space toggles marks on file rows so
//	               Enter opens the marked set (or just the highlighted
//	               row when no marks are set).
type FilePickerMode int

const (
	ModeSaveTarget FilePickerMode = iota
	ModeOpenSingle
	ModeOpenMulti
)

// FilePickerFocus is the sub-widget currently receiving keys. Stored as
// int so callers comparing Focus == 0 still refer to FocusDir.
type FilePickerFocus = int

const (
	FocusDir   FilePickerFocus = 0
	FocusList  FilePickerFocus = 1
	FocusInput FilePickerFocus = 2
	FocusExt   FilePickerFocus = 3
)

// RowKind tags each row in the browse list. RowParent is the ".." entry
// shown when the current directory isn't a filesystem root.
type RowKind int

const (
	RowParent RowKind = iota
	RowDir
	RowFile
)

// FileRow is one entry in the browse list.
type FileRow struct {
	Kind    RowKind
	Name    string
	Abs     string
	ModUnix int64
}

// ExtChoice is one entry in the Ext cycler. Ext includes the leading
// dot (e.g. ".csv"). An empty Ext is the "all files" choice; Label
// overrides the display string (otherwise the dotted extension is used,
// or "All files" when Ext is empty).
type ExtChoice struct {
	Ext   string
	Label string
}

// Display returns the label shown in the Ext row.
func (c ExtChoice) Display() string {
	if c.Label != "" {
		return c.Label
	}
	if c.Ext == "" {
		return "All files"
	}
	return c.Ext
}

// FilePickerOpts configures NewFilePicker.
//
// Dir seeds the Dir field (defaulting to cwd when empty). Name seeds
// the input field (Save-mode filename stem). Choices is the Ext cycler
// set; len<=1 hides the Ext row unless ShowFormat is true. InitialExtIdx
// selects the starting entry in Choices. Exts is an Open-mode hard
// filter on the browse list (ignored in Save mode, where Choices drives
// the dim rule). OnDirChange fires when the resolved base dir changes
// so the caller can dispatch an async ListDir.
type FilePickerOpts struct {
	Mode          FilePickerMode
	Dir           string
	Name          string
	Choices       []ExtChoice
	InitialExtIdx int
	ShowFormat    bool
	Exts          []string
	OnDirChange   func()
}

// FilePicker is a composite file browse + select widget.
type FilePicker struct {
	Mode FilePickerMode

	DirInput  *Input
	NameInput *Input
	Focus     FilePickerFocus

	Choices    []ExtChoice
	ExtIdx     int
	ShowFormat bool

	Exts []string

	Rows     []FileRow
	Filtered []int
	List     ScrollList
	Marked   map[string]bool

	Guard OverwriteGuard

	dirBase     string
	scanErr     string
	onDirChange func()
}

// NewFilePicker builds a picker. Dir defaults to cwd. Save mode starts
// with the filename field focused; Open modes start on the browse list.
func NewFilePicker(opts FilePickerOpts) *FilePicker {
	choices := opts.Choices
	if len(choices) == 0 {
		choices = []ExtChoice{{Ext: ""}}
	}
	idx := opts.InitialExtIdx
	if idx < 0 || idx >= len(choices) {
		idx = 0
	}
	dir := strings.TrimSpace(opts.Dir)
	if dir == "" {
		dir, _ = os.Getwd()
	} else {
		dir = expandTilde(dir)
	}
	fp := &FilePicker{
		Mode:        opts.Mode,
		DirInput:    NewInput(dir),
		NameInput:   NewInput(opts.Name),
		Choices:     choices,
		ExtIdx:      idx,
		ShowFormat:  opts.ShowFormat || len(choices) > 1,
		Exts:        opts.Exts,
		Marked:      map[string]bool{},
		onDirChange: opts.OnDirChange,
	}
	if fp.Mode == ModeSaveTarget {
		fp.Focus = FocusInput
	} else {
		fp.Focus = FocusList
	}
	return fp
}

// -- public helpers --------------------------------------------------

// Choice returns the currently selected ExtChoice.
func (fp *FilePicker) Choice() ExtChoice { return fp.Choices[fp.ExtIdx] }

// Ext returns the currently selected extension (including its dot, or
// empty for the "all files" choice).
func (fp *FilePicker) Ext() string { return fp.Choices[fp.ExtIdx].Ext }

// SetExtIdx jumps to idx if valid and resets the overwrite guard so the
// user must reconfirm any pending overwrite.
func (fp *FilePicker) SetExtIdx(idx int) {
	if idx < 0 || idx >= len(fp.Choices) {
		return
	}
	fp.ExtIdx = idx
	fp.Guard.Reset()
}

// CycleExt advances (back=false) or rewinds (back=true) the Ext cycler.
// No-op when there's only one choice.
func (fp *FilePicker) CycleExt(back bool) {
	if len(fp.Choices) <= 1 {
		return
	}
	if back {
		fp.ExtIdx = (fp.ExtIdx - 1 + len(fp.Choices)) % len(fp.Choices)
	} else {
		fp.ExtIdx = (fp.ExtIdx + 1) % len(fp.Choices)
	}
	fp.Guard.Reset()
}

// Path returns the fully resolved absolute save-target path (Dir + Name
// + Ext). The result is always absolute + cleaned.
func (fp *FilePicker) Path() string {
	dir := fp.resolvedDir()
	name := strings.TrimSpace(fp.NameInput.String()) + fp.Ext()
	joined := filepath.Join(dir, name)
	abs, err := filepath.Abs(joined)
	if err != nil {
		return joined
	}
	return filepath.Clean(abs)
}

// NameText returns the current filename stem (Save mode).
func (fp *FilePicker) NameText() string { return fp.NameInput.String() }

// SearchText returns the current search query (Open modes). Aliases
// NameText so callers can read intent-appropriate code.
func (fp *FilePicker) SearchText() string { return fp.NameInput.String() }

// SetSearch sets the search field (Open modes) and refilters.
func (fp *FilePicker) SetSearch(s string) {
	fp.NameInput.SetString(s)
	fp.Refilter()
}

// DirText returns the raw Dir field value (unresolved).
func (fp *FilePicker) DirText() string { return fp.DirInput.String() }

// ScanBase returns the deepest existing directory along DirInput,
// falling back to cwd. This is the path callers pass to ListDir.
func (fp *FilePicker) ScanBase() string { return fp.resolvedDir() }

// DirBase returns the directory the last applied scan came from.
func (fp *FilePicker) DirBase() string { return fp.dirBase }

// ScanErr returns the last scan error (empty on success).
func (fp *FilePicker) ScanErr() string { return fp.scanErr }

// HasEntries reports whether the filtered browse list is non-empty.
func (fp *FilePicker) HasEntries() bool { return len(fp.Filtered) > 0 }

// SetOnDirChange replaces the dir-change callback (useful when the
// callback captures state -- e.g. an *app -- not available at
// construction time).
func (fp *FilePicker) SetOnDirChange(fn func()) { fp.onDirChange = fn }

// NotifyDirChange is called after any DirInput mutation. Resets the
// overwrite guard. Invokes OnDirChange when the resolved base has
// actually shifted, so rapid typing doesn't kick off one scan per
// keystroke.
func (fp *FilePicker) NotifyDirChange() {
	base := fp.resolvedDir()
	fp.Guard.Reset()
	if base != fp.dirBase {
		fp.dirBase = base
		if fp.onDirChange != nil {
			fp.onDirChange()
		}
	}
}

// ApplyRows installs scanned rows for base. Discarded when base no
// longer matches the current resolved base (user typed past it). Marks
// that no longer correspond to a present row are dropped.
func (fp *FilePicker) ApplyRows(base string, rows []FileRow, scanErr string) {
	if base != fp.resolvedDir() {
		return
	}
	fp.dirBase = base
	fp.scanErr = scanErr
	fp.Rows = rows
	if len(fp.Marked) > 0 {
		present := make(map[string]bool, len(rows))
		for _, r := range rows {
			present[r.Abs] = true
		}
		for k := range fp.Marked {
			if !present[k] {
				delete(fp.Marked, k)
			}
		}
	}
	fp.Refilter()
}

// Refilter rebuilds Filtered from Rows + mode filters. Parent + dir
// rows always show. File rows respect the Open-mode hard Exts filter
// and (Open modes only) the substring search query.
func (fp *FilePicker) Refilter() {
	q := ""
	if fp.Mode != ModeSaveTarget {
		q = strings.ToLower(strings.TrimSpace(fp.NameInput.String()))
	}
	out := make([]int, 0, len(fp.Rows))
	for i, r := range fp.Rows {
		if r.Kind != RowFile {
			out = append(out, i)
			continue
		}
		if fp.Mode != ModeSaveTarget && len(fp.Exts) > 0 {
			if !extInList(r.Name, fp.Exts) {
				continue
			}
		}
		if q != "" && !strings.Contains(strings.ToLower(r.Name), q) {
			continue
		}
		out = append(out, i)
	}
	fp.Filtered = out
	fp.List.Len = len(out)
	if fp.List.Selected >= len(out) {
		fp.List.Selected = 0
	}
	fp.List.Scroll = 0
	fp.List.Clamp()
}

// SelectedRow returns the highlighted browse-list row (any kind).
// ok=false when the list is empty.
func (fp *FilePicker) SelectedRow() (FileRow, bool) {
	i := fp.List.Selected
	if i < 0 || i >= len(fp.Filtered) {
		return FileRow{}, false
	}
	return fp.Rows[fp.Filtered[i]], true
}

// SelectedEntry returns the highlighted row only when it's a file.
// Mirrors the previous open-mode API shape.
func (fp *FilePicker) SelectedEntry() (FileRow, bool) {
	r, ok := fp.SelectedRow()
	if !ok || r.Kind != RowFile {
		return FileRow{}, false
	}
	return r, true
}

// ToggleMark flips the mark on the selected file row (OpenMulti only).
func (fp *FilePicker) ToggleMark() {
	if fp.Mode != ModeOpenMulti {
		return
	}
	r, ok := fp.SelectedEntry()
	if !ok {
		return
	}
	if fp.Marked[r.Abs] {
		delete(fp.Marked, r.Abs)
	} else {
		fp.Marked[r.Abs] = true
	}
}

// MarkedPaths returns abs paths of marked rows in Rows order.
func (fp *FilePicker) MarkedPaths() []string {
	if len(fp.Marked) == 0 {
		return nil
	}
	out := make([]string, 0, len(fp.Marked))
	for _, r := range fp.Rows {
		if r.Kind == RowFile && fp.Marked[r.Abs] {
			out = append(out, r.Abs)
		}
	}
	return out
}

// -- internals -------------------------------------------------------

// expandTilde replaces a leading ~ or ~/ (or ~\ on Windows) with the
// user's home directory. Leaves other inputs unchanged.
func expandTilde(p string) string {
	if p == "" || p[0] != '~' {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	if len(p) >= 2 && (p[1] == '/' || p[1] == '\\') {
		return filepath.Join(home, p[2:])
	}
	return p
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// resolvedDir walks up the typed path until it hits an existing
// directory. This lets half-typed paths show their deepest real parent
// without the user hitting Backspace.
func (fp *FilePicker) resolvedDir() string {
	raw := strings.TrimSpace(fp.DirInput.String())
	if raw == "" {
		cwd, _ := os.Getwd()
		return cwd
	}
	raw = expandTilde(raw)
	abs, err := filepath.Abs(raw)
	if err != nil {
		abs = filepath.Clean(raw)
	}
	for p := abs; ; {
		if isDir(p) {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			cwd, _ := os.Getwd()
			return cwd
		}
		p = parent
	}
}

func extInList(name string, exts []string) bool {
	ext := filepath.Ext(name)
	for _, e := range exts {
		if strings.EqualFold(ext, e) {
			return true
		}
	}
	return false
}

// ListDir reads base as a single directory level and returns a sorted
// row list: ".." (when base isn't a root) + dirs + files, each sorted
// alphabetically (case-insensitive). Extension filtering is the
// picker's job (it may show+dim or hide based on mode), so this
// function returns all entries.
func ListDir(base string) ([]FileRow, error) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}
	var dirs, files []FileRow
	for _, e := range entries {
		abs := filepath.Join(base, e.Name())
		if e.IsDir() {
			dirs = append(dirs, FileRow{Kind: RowDir, Name: e.Name(), Abs: abs})
			continue
		}
		var mod int64
		if info, err := e.Info(); err == nil {
			mod = info.ModTime().Unix()
		}
		files = append(files, FileRow{Kind: RowFile, Name: e.Name(), Abs: abs, ModUnix: mod})
	}
	less := func(a, b FileRow) bool {
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	}
	sort.Slice(dirs, func(i, j int) bool { return less(dirs[i], dirs[j]) })
	sort.Slice(files, func(i, j int) bool { return less(files[i], files[j]) })
	rows := make([]FileRow, 0, len(dirs)+len(files)+1)
	if parent := filepath.Dir(base); parent != base {
		rows = append(rows, FileRow{Kind: RowParent, Name: "..", Abs: parent})
	}
	rows = append(rows, dirs...)
	rows = append(rows, files...)
	return rows, nil
}

// -- Key handling ----------------------------------------------------

// FilePickerKeyResult describes the outcome of HandleKey. Consumed
// indicates the key was handled. SaveRequested fires on Enter-to-save
// (Save mode). OpenRequested fires on Enter-to-open (Open modes).
type FilePickerKeyResult struct {
	Consumed      bool
	SaveRequested bool
	OpenRequested bool
}

// HandleKey routes k by Focus. Tab / Shift+Tab rotate focus through
// Dir -> List -> Input -> (Ext when Choices>1) and back.
func (fp *FilePicker) HandleKey(k term.Key) FilePickerKeyResult {
	switch k.Kind {
	case term.KeyTab:
		fp.cycleFocus(false)
		return FilePickerKeyResult{Consumed: true}
	case term.KeyBackTab:
		fp.cycleFocus(true)
		return FilePickerKeyResult{Consumed: true}
	}
	switch fp.Focus {
	case FocusDir:
		return fp.handleDir(k)
	case FocusList:
		return fp.handleList(k)
	case FocusInput:
		return fp.handleInput(k)
	case FocusExt:
		return fp.handleExt(k)
	}
	return FilePickerKeyResult{}
}

func (fp *FilePicker) focusOrder() []FilePickerFocus {
	order := []FilePickerFocus{FocusDir, FocusList, FocusInput}
	if len(fp.Choices) > 1 {
		order = append(order, FocusExt)
	}
	return order
}

func (fp *FilePicker) cycleFocus(back bool) {
	order := fp.focusOrder()
	cur := 0
	for i, f := range order {
		if f == fp.Focus {
			cur = i
			break
		}
	}
	if back {
		cur = (cur - 1 + len(order)) % len(order)
	} else {
		cur = (cur + 1) % len(order)
	}
	fp.Focus = order[cur]
}

func (fp *FilePicker) handleDir(k term.Key) FilePickerKeyResult {
	switch k.Kind {
	case term.KeyEnter:
		fp.NotifyDirChange()
		fp.Focus = FocusList
		return FilePickerKeyResult{Consumed: true}
	case term.KeyBackspace:
		if fp.dirInputCursorAfterSep() {
			fp.deleteLastPathSegment()
			fp.NotifyDirChange()
			return FilePickerKeyResult{Consumed: true}
		}
	}
	before := fp.DirInput.String()
	if fp.DirInput.Handle(k) {
		if fp.DirInput.String() != before {
			fp.NotifyDirChange()
		}
		return FilePickerKeyResult{Consumed: true}
	}
	return FilePickerKeyResult{}
}

func (fp *FilePicker) handleList(k term.Key) FilePickerKeyResult {
	if fp.List.HandleKey(k) {
		return FilePickerKeyResult{Consumed: true}
	}
	switch k.Kind {
	case term.KeyRune:
		if !k.Ctrl && !k.Alt && k.Rune == ' ' && fp.Mode == ModeOpenMulti {
			fp.ToggleMark()
			return FilePickerKeyResult{Consumed: true}
		}
	case term.KeyEnter:
		return fp.activateSelected()
	}
	return FilePickerKeyResult{}
}

func (fp *FilePicker) activateSelected() FilePickerKeyResult {
	r, ok := fp.SelectedRow()
	if !ok {
		if fp.Mode == ModeSaveTarget {
			return FilePickerKeyResult{Consumed: true, SaveRequested: true}
		}
		return FilePickerKeyResult{Consumed: true, OpenRequested: true}
	}
	switch r.Kind {
	case RowParent, RowDir:
		fp.DirInput.SetString(r.Abs + string(filepath.Separator))
		fp.NotifyDirChange()
		return FilePickerKeyResult{Consumed: true}
	case RowFile:
		if fp.Mode == ModeSaveTarget {
			name := r.Name
			ext := filepath.Ext(name)
			stem := strings.TrimSuffix(name, ext)
			fp.NameInput.SetString(stem)
			if ext != "" {
				for i, c := range fp.Choices {
					if strings.EqualFold(c.Ext, ext) {
						fp.ExtIdx = i
						break
					}
				}
			}
			fp.Guard.Reset()
			fp.Focus = FocusInput
			return FilePickerKeyResult{Consumed: true}
		}
		return FilePickerKeyResult{Consumed: true, OpenRequested: true}
	}
	return FilePickerKeyResult{Consumed: true}
}

func (fp *FilePicker) handleInput(k term.Key) FilePickerKeyResult {
	if k.Kind == term.KeyEnter {
		if fp.Mode == ModeSaveTarget {
			return FilePickerKeyResult{Consumed: true, SaveRequested: true}
		}
		return FilePickerKeyResult{Consumed: true, OpenRequested: true}
	}
	before := fp.NameInput.String()
	if fp.NameInput.Handle(k) {
		if fp.NameInput.String() != before {
			fp.Guard.Reset()
			if fp.Mode != ModeSaveTarget {
				fp.Refilter()
			}
		}
		return FilePickerKeyResult{Consumed: true}
	}
	return FilePickerKeyResult{}
}

func (fp *FilePicker) handleExt(k term.Key) FilePickerKeyResult {
	switch k.Kind {
	case term.KeyUp, term.KeyLeft:
		fp.CycleExt(true)
		return FilePickerKeyResult{Consumed: true}
	case term.KeyDown, term.KeyRight:
		fp.CycleExt(false)
		return FilePickerKeyResult{Consumed: true}
	case term.KeyEnter:
		if fp.Mode == ModeSaveTarget {
			fp.Focus = FocusInput
			return FilePickerKeyResult{Consumed: true}
		}
		return FilePickerKeyResult{Consumed: true, OpenRequested: true}
	}
	return FilePickerKeyResult{}
}

// HandleMouse hit-tests the browse list. A left-click inside the list
// selects the row and pulls focus onto the list so the caller can check
// for double-click activation.
func (fp *FilePicker) HandleMouse(mm term.MouseMsg) (int, bool) {
	idx, ok := fp.List.HandleMouse(mm)
	if ok && idx >= 0 {
		fp.List.Selected = idx
		fp.Focus = FocusList
	}
	return idx, ok
}

func (fp *FilePicker) dirInputCursorAfterSep() bool {
	rs := fp.DirInput.Runes()
	cur := fp.DirInput.Cursor()
	if cur == 0 || cur > len(rs) {
		return false
	}
	prev := rs[cur-1]
	return prev == '/' || prev == '\\'
}

func (fp *FilePicker) deleteLastPathSegment() {
	rs := fp.DirInput.Runes()
	cur := fp.DirInput.Cursor()
	if cur == 0 {
		return
	}
	i := cur - 1
	for i >= 0 && (rs[i] == '/' || rs[i] == '\\') {
		i--
	}
	for i >= 0 && rs[i] != '/' && rs[i] != '\\' {
		i--
	}
	newLen := i + 1
	if newLen < 0 {
		newLen = 0
	}
	out := make([]rune, 0, newLen+(len(rs)-cur))
	out = append(out, rs[:newLen]...)
	out = append(out, rs[cur:]...)
	fp.DirInput.SetString(string(out))
}

// -- Layout + Draw ---------------------------------------------------

// PreferredHeight returns the dialog height this picker wants given
// listRows rows of vertical space for the browse list.
// Chrome overhead: 2 frame rows + 1 dir + 1 sep + 1 sep + 1 input
// + (1 ext when ShowFormat) + 1 status = 7 (or 8 with ext).
func (fp *FilePicker) PreferredHeight(listRows int) int {
	if listRows < 1 {
		listRows = 1
	}
	h := 7 + listRows
	if fp.ShowFormat {
		h++
	}
	return h
}

// DrawOpts configures the Draw pass. FocusedFG colors the active label
// and selected row. DimStyle colors the parent row, ext-mismatched
// files in Save mode, and placeholder text. Truncate is a display-
// width-aware clipper for paths (nil falls back to a rune clipper).
type DrawOpts struct {
	FocusedFG int
	DimStyle  term.Style
	Truncate  func(s string, w int) string
}

// Draw paints the picker inside r. Row layout top-to-bottom:
// Dir | sep | browse-list | sep | Input | (Ext) | status. The caller's
// status line lives at r.Row + r.H - 2; the picker does not paint
// there. The frame itself is the caller's responsibility.
func (fp *FilePicker) Draw(c *term.Cellbuf, r term.Rect, opts DrawOpts) {
	trunc := opts.Truncate
	if trunc == nil {
		trunc = defaultTruncate
	}
	innerCol := r.Col + 2
	innerW := r.W - 4
	if innerW < 1 {
		innerW = 1
	}

	inputLabel := "Name:"
	if fp.Mode != ModeSaveTarget {
		inputLabel = "Find:"
	}
	labelW := len("Name: ")

	// Work out vertical slots from the bottom up so the list flexes.
	statusRow := r.Row + r.H - 2
	bottomFrame := r.Row + r.H - 1
	_ = bottomFrame

	extRow := -1
	bottom := statusRow - 1
	if fp.ShowFormat {
		extRow = bottom
		bottom--
	}
	inputRow := bottom
	bottom--
	sepBelow := bottom
	bottom--
	listEnd := bottom

	// Top-down.
	top := r.Row + 1
	dirRow := top
	sepAbove := dirRow + 1
	listTop := sepAbove + 1

	listH := listEnd - listTop + 1
	if listH < 1 {
		listH = 1
	}

	// Dir row.
	fp.drawLabel(c, dirRow, innerCol, "Dir: ", fp.Focus == FocusDir, opts.FocusedFG)
	dirValCol := innerCol + labelW
	dirMax := innerW - labelW
	if dirMax < 1 {
		dirMax = 1
	}
	if fp.Focus == FocusDir {
		DrawInput(c, fp.DirInput, dirRow, dirValCol, dirMax)
	} else {
		c.WriteAt(dirRow, dirValCol, trunc(fp.DirInput.String(), dirMax))
	}

	// Separators.
	c.HLine(sepAbove, r.Col+1, r.Col+r.W-2, '─')
	c.HLine(sepBelow, r.Col+1, r.Col+r.W-2, '─')

	// Browse list.
	fp.List.ListTop = listTop
	fp.List.ListH = listH
	fp.List.Len = len(fp.Filtered)
	fp.List.ViewportScroll(listH)

	if len(fp.Filtered) == 0 {
		msg := "(empty directory)"
		if fp.scanErr != "" {
			msg = fp.scanErr
		} else if fp.Mode != ModeSaveTarget && strings.TrimSpace(fp.NameInput.String()) != "" {
			msg = "(no matches)"
		}
		c.WriteStyled(listTop, innerCol, trunc(msg, innerW), opts.DimStyle)
	} else {
		start, end := fp.List.VisibleRange()
		for i := start; i < end; i++ {
			ridx := fp.Filtered[i]
			fp.drawRow(c, listTop+(i-start), innerCol, innerW, i, fp.Rows[ridx], opts, trunc)
		}
	}

	// Input row.
	fp.drawLabel(c, inputRow, innerCol, inputLabel, fp.Focus == FocusInput, opts.FocusedFG)
	inputValCol := innerCol + labelW
	inputMax := innerW - labelW
	// Save mode: when Ext row is hidden, paint ext as a suffix so the
	// user still sees what will be appended.
	inlineExt := fp.Mode == ModeSaveTarget && !fp.ShowFormat && fp.Ext() != ""
	if inlineExt {
		extW := len([]rune(fp.Ext())) + 1
		inputMax -= extW
	}
	if inputMax < 1 {
		inputMax = 1
	}
	if fp.Focus == FocusInput {
		DrawInput(c, fp.NameInput, inputRow, inputValCol, inputMax)
	} else {
		c.WriteAt(inputRow, inputValCol, trunc(fp.NameInput.String(), inputMax))
	}
	if inlineExt {
		c.WriteStyled(inputRow, inputValCol+inputMax+1, fp.Ext(), opts.DimStyle)
	}

	// Ext row (optional).
	if extRow >= 0 {
		fp.drawLabel(c, extRow, innerCol, "Ext: ", fp.Focus == FocusExt, opts.FocusedFG)
		extValCol := innerCol + labelW
		extMax := innerW - labelW
		if extMax < 1 {
			extMax = 1
		}
		label := fp.Choice().Display()
		if fp.Focus == FocusExt {
			c.SetFg(opts.FocusedFG)
			c.WriteAt(extRow, extValCol, trunc("< "+label+" >", extMax))
			c.ResetStyle()
		} else {
			c.WriteAt(extRow, extValCol, trunc(label, extMax))
		}
	}
}

func (fp *FilePicker) drawRow(c *term.Cellbuf, y, col, w, i int, rr FileRow, opts DrawOpts, trunc func(string, int) string) {
	isSel := fp.List.IsSelected(i) && fp.Focus == FocusList
	selMark := "  "
	if isSel {
		selMark = "> "
	}
	pickMark := ""
	if fp.Mode == ModeOpenMulti {
		if rr.Kind == RowFile && fp.Marked[rr.Abs] {
			pickMark = "* "
		} else if rr.Kind == RowFile {
			pickMark = "  "
		} else {
			pickMark = "  "
		}
	}
	name := rr.Name
	if rr.Kind == RowDir {
		name = name + string(filepath.Separator)
	}
	line := trunc(selMark+pickMark+name, w)

	dim := rr.Kind == RowParent
	if fp.Mode == ModeSaveTarget && rr.Kind == RowFile && fp.Ext() != "" {
		if !strings.EqualFold(filepath.Ext(rr.Name), fp.Ext()) {
			dim = true
		}
	}

	switch {
	case isSel:
		c.SetFg(opts.FocusedFG)
		c.WriteAt(y, col, line)
		c.ResetStyle()
	case dim:
		c.WriteStyled(y, col, line, opts.DimStyle)
	default:
		c.WriteAt(y, col, line)
	}
}

func (fp *FilePicker) drawLabel(c *term.Cellbuf, row, col int, label string, focused bool, focusedFG int) {
	if focused {
		c.SetFg(focusedFG)
		c.WriteAt(row, col, label)
		c.ResetStyle()
	} else {
		c.WriteAt(row, col, label)
	}
}

// defaultTruncate is a rune-count fallback for callers that don't pass
// a width-aware clipper. Paths are usually ASCII so rune count ~=
// display width.
func defaultTruncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	rs := []rune(s)
	if len(rs) <= w {
		return s
	}
	if w == 1 {
		return string(rs[:1])
	}
	return string(rs[:w-1]) + "…"
}
