package tui

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// openLayer is the modal overlay that loads a .sql file into the editor.
// It scans the current working directory for *.sql files on open and
// shows them as a searchable list; the search box filters by substring
// match on the relative path. Enter loads the highlighted file.
type openLayer struct {
	search   *input
	all      []openEntry // discovered files, sorted most-recent first
	results  []openEntry // current filter output
	selected int
	scroll   int
	status   string
	scanErr  string

	// marked is the set of abs paths toggled with Tab. When non-empty,
	// Enter opens each marked file in its own editor tab instead of
	// loading the highlighted entry into the current tab. Keyed by
	// absolute path so marks survive filter changes.
	marked map[string]bool

	lastListTop int
	lastListH   int
	clicks      clickTracker
}

// openEntry is a discovered SQL file. rel is the display path (relative
// to the scan root when possible, else the absolute path). abs is what
// os.ReadFile gets.
type openEntry struct {
	rel     string
	abs     string
	modUnix int64
}

// openScanMaxDepth caps the recursive walk so a stray open inside a huge
// tree (home dir, Go module cache, etc.) doesn't stall the UI.
const openScanMaxDepth = 6

// openScanMaxFiles bounds total results for the same reason. 2000 is
// well beyond any realistic per-repo SQL file count and still renders
// instantly.
const openScanMaxFiles = 2000

func newOpenLayer(seed string) *openLayer {
	ol := &openLayer{search: newInput(seed), marked: map[string]bool{}}
	ol.scan()
	ol.filter()
	return ol
}

// scan walks the current working directory looking for *.sql files. It
// skips common vendor / build / VCS trees so the list stays
// signal-heavy. A .sqlgoignore file in the scan root can add or remove
// directory names from that skip set: each non-blank, non-# line is a
// basename to skip, or a `!name` to re-include one of the defaults
// (e.g. `!vendor` when a project keeps schema SQL under vendor/).
func (ol *openLayer) scan() {
	root, err := os.Getwd()
	if err != nil {
		ol.scanErr = "cwd: " + err.Error()
		return
	}
	skip := loadSkipSet(root)
	rootDepth := strings.Count(filepath.Clean(root), string(filepath.Separator))
	var out []openEntry
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			base := d.Name()
			if path != root && skip[base] {
				return fs.SkipDir
			}
			depth := strings.Count(filepath.Clean(path), string(filepath.Separator)) - rootDepth
			if depth > openScanMaxDepth {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".sql") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		var mod int64
		if err == nil {
			mod = info.ModTime().Unix()
		}
		out = append(out, openEntry{rel: rel, abs: path, modUnix: mod})
		if len(out) >= openScanMaxFiles {
			return filepath.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		ol.scanErr = "scan: " + walkErr.Error()
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].modUnix != out[j].modUnix {
			return out[i].modUnix > out[j].modUnix
		}
		return out[i].rel < out[j].rel
	})
	ol.all = out
}

// defaultSkipDirs are the directory basenames we skip by default when
// scanning for SQL files. Kept narrow on purpose: these are trees that
// almost never contain query files a user is editing. A .sqlgoignore in
// the scan root can add to or remove from this set.
var defaultSkipDirs = []string{
	"node_modules", "vendor", "dist", "build", "out", "target",
	"bin", "obj", ".git", ".hg", ".svn", ".idea", ".vscode",
}

// loadSkipSet returns the effective directory-skip set for a scan
// rooted at root. The defaults seed the set; a .sqlgoignore file at the
// root may layer overrides on top: bare `name` adds, `!name` removes.
// Blank lines and `#` comments are ignored.
func loadSkipSet(root string) map[string]bool {
	skip := make(map[string]bool, len(defaultSkipDirs))
	for _, n := range defaultSkipDirs {
		skip[n] = true
	}
	data, err := os.ReadFile(filepath.Join(root, ".sqlgoignore"))
	if err != nil {
		return skip
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "!") {
			delete(skip, strings.TrimPrefix(line, "!"))
			continue
		}
		skip[line] = true
	}
	return skip
}

// filter recomputes results from the current search string. Matching is
// case-insensitive substring on the relative path; an empty query shows
// everything.
func (ol *openLayer) filter() {
	q := strings.ToLower(strings.TrimSpace(ol.search.String()))
	if q == "" {
		ol.results = ol.all
	} else {
		out := make([]openEntry, 0, len(ol.all))
		for _, e := range ol.all {
			if strings.Contains(strings.ToLower(e.rel), q) {
				out = append(out, e)
			}
		}
		ol.results = out
	}
	if ol.selected >= len(ol.results) {
		ol.selected = len(ol.results) - 1
	}
	if ol.selected < 0 {
		ol.selected = 0
	}
	ol.scroll = 0
	switch {
	case ol.scanErr != "":
		ol.status = ol.scanErr
	case len(ol.all) == 0:
		ol.status = "no .sql files found under cwd"
	case len(ol.results) == 0:
		ol.status = "no matches"
	default:
		if n := len(ol.marked); n > 0 {
			ol.status = fmt.Sprintf("%d / %d files -- %d marked (Enter opens each in a new tab)", len(ol.results), len(ol.all), n)
		} else {
			ol.status = fmt.Sprintf("%d / %d files", len(ol.results), len(ol.all))
		}
	}
}

func (ol *openLayer) Draw(a *app, c *cellbuf) {
	boxW := 100
	if boxW > a.term.width-dialogMargin {
		boxW = a.term.width - dialogMargin
	}
	if boxW < 50 {
		boxW = 50
	}
	boxH := 22
	if boxH > a.term.height-dialogMargin {
		boxH = a.term.height - dialogMargin
	}
	if boxH < 10 {
		boxH = 10
	}
	row := (a.term.height - boxH) / 2
	col := (a.term.width - boxW) / 2
	if row < 1 {
		row = 1
	}
	if col < 1 {
		col = 1
	}
	r := rect{row: row, col: col, w: boxW, h: boxH}
	c.fillRect(r)
	drawFrame(c, r, "Open SQL file", true)

	innerCol := col + 2

	c.writeAt(row+1, innerCol, "Search:")
	searchCol := innerCol + 8
	searchW := boxW - 8 - 4
	if searchW < 1 {
		searchW = 1
	}
	val := ol.search.String()
	rs := []rune(val)
	if len(rs) > searchW {
		rs = rs[len(rs)-searchW:]
	}
	c.writeAt(row+1, searchCol, string(rs))

	c.hLine(row+2, col+1, col+r.w-2, '─')

	listTop := row + 3
	listBot := row + r.h - 3
	listH := listBot - listTop + 1
	if listH < 1 {
		listH = 1
	}
	ol.lastListTop = listTop
	ol.lastListH = listH

	if len(ol.results) == 0 {
		msg := "(no .sql files)"
		if strings.TrimSpace(ol.search.String()) != "" {
			msg = "(no matches -- Enter loads the typed path directly)"
		}
		c.writeAt(listTop, innerCol, truncate(msg, boxW-4))
	} else {
		if ol.selected < ol.scroll {
			ol.scroll = ol.selected
		}
		if ol.selected >= ol.scroll+listH {
			ol.scroll = ol.selected - listH + 1
		}
		if ol.scroll < 0 {
			ol.scroll = 0
		}
		for i := 0; i < listH; i++ {
			idx := ol.scroll + i
			if idx >= len(ol.results) {
				break
			}
			e := ol.results[idx]
			selMark := "  "
			if idx == ol.selected {
				selMark = "▶ "
			}
			pickMark := "  "
			if ol.marked[e.abs] {
				pickMark = "● "
			}
			line := truncate(selMark+pickMark+e.rel, boxW-4)
			if idx == ol.selected {
				c.setFg(colorBorderFocused)
				c.writeAt(listTop+i, innerCol, line)
				c.resetStyle()
			} else {
				c.writeAt(listTop+i, innerCol, line)
			}
		}
	}

	if ol.status != "" {
		c.setFg(colorStatusBar)
		c.writeAt(r.row+r.h-2, innerCol, truncate(ol.status, boxW-4))
		c.resetStyle()
	}

	// Place cursor last so any intermediate style changes from the list
	// render don't leave the terminal cursor in the wrong column.
	c.placeCursor(row+1, searchCol+len(rs))
}

func (ol *openLayer) HandleKey(a *app, k Key) {
	switch k.Kind {
	case KeyEsc:
		a.popLayer()
		return
	case KeyUp:
		if ol.selected > 0 {
			ol.selected--
		}
		return
	case KeyDown:
		if ol.selected < len(ol.results)-1 {
			ol.selected++
		}
		return
	case KeyPgUp:
		ol.selected -= 10
		if ol.selected < 0 {
			ol.selected = 0
		}
		return
	case KeyPgDn:
		ol.selected += 10
		if ol.selected > len(ol.results)-1 {
			ol.selected = len(ol.results) - 1
		}
		return
	case KeyTab:
		ol.toggleMark()
		return
	case KeyEnter:
		ol.load(a)
		return
	}
	ol.search.handle(k)
	ol.filter()
}

// toggleMark flips the marked state for the highlighted entry and
// refreshes the status line.
func (ol *openLayer) toggleMark() {
	if ol.selected < 0 || ol.selected >= len(ol.results) {
		return
	}
	abs := ol.results[ol.selected].abs
	if ol.marked[abs] {
		delete(ol.marked, abs)
	} else {
		ol.marked[abs] = true
	}
	// Refresh the status line so the marked count stays current
	// without re-running the filter.
	if n := len(ol.marked); n > 0 {
		ol.status = fmt.Sprintf("%d / %d files -- %d marked (Enter opens each in a new tab)", len(ol.results), len(ol.all), n)
	} else {
		ol.status = fmt.Sprintf("%d / %d files", len(ol.results), len(ol.all))
	}
}

// load resolves the target path(s): if any entries are marked, each is
// loaded into its own new editor tab; otherwise the highlighted entry
// is loaded into the current tab, falling back to treating the search
// box contents as a direct path if the results list is empty.
func (ol *openLayer) load(a *app) {
	if len(ol.marked) > 0 {
		ol.loadMarked(a)
		return
	}
	var path string
	switch {
	case len(ol.results) > 0 && ol.selected >= 0 && ol.selected < len(ol.results):
		path = ol.results[ol.selected].abs
	default:
		path = strings.TrimSpace(ol.search.String())
	}
	if path == "" {
		ol.status = "path is required"
		return
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = filepath.Clean(abs)
	}
	m := a.mainLayerPtr()
	if idx := m.findTabByPath(path); idx >= 0 {
		m.switchTab(idx)
		a.popLayer()
		m.status = fmt.Sprintf("switched to open tab for %s", filepath.Base(path))
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		ol.status = "read failed: " + err.Error()
		return
	}
	text := string(data)
	m.editor.buf.SetText(text)
	m.session.sourcePath = path
	m.session.savedText = text
	m.session.title = filepath.Base(path)
	a.popLayer()
	m.status = fmt.Sprintf("loaded %d bytes from %s", len(data), path)
}

// loadMarked opens each marked entry in its own new editor tab.
// Iterates ol.all for deterministic order (scan order, most-recent
// first) rather than map iteration. Silently skips files that fail to
// read and reports the count of loaded/skipped in the status line.
func (ol *openLayer) loadMarked(a *app) {
	m := a.mainLayerPtr()
	var loaded, skipped, reused int
	var lastErr string
	lastIdx := -1
	for _, e := range ol.all {
		if !ol.marked[e.abs] {
			continue
		}
		if idx := m.findTabByPath(e.abs); idx >= 0 {
			reused++
			lastIdx = idx
			continue
		}
		data, err := os.ReadFile(e.abs)
		if err != nil {
			skipped++
			lastErr = err.Error()
			continue
		}
		m.newTab()
		text := string(data)
		m.editor.buf.SetText(text)
		m.session.sourcePath = e.abs
		m.session.savedText = text
		m.session.title = filepath.Base(e.abs)
		loaded++
		lastIdx = m.activeTab
	}
	if loaded == 0 && reused == 0 {
		if lastErr != "" {
			ol.status = "read failed: " + lastErr
		} else {
			ol.status = "no files loaded"
		}
		return
	}
	if lastIdx >= 0 {
		m.switchTab(lastIdx)
	}
	a.popLayer()
	switch {
	case skipped > 0:
		m.status = fmt.Sprintf("opened %d new, %d already open (%d failed)", loaded, reused, skipped)
	case reused > 0:
		m.status = fmt.Sprintf("opened %d new, %d already open", loaded, reused)
	default:
		m.status = fmt.Sprintf("opened %d files in new tabs", loaded)
	}
}

// HandleInput routes mouse events: wheel scrolls the selection; left
// click selects the row under the pointer; double-click loads it.
func (ol *openLayer) HandleInput(a *app, msg InputMsg) bool {
	mm, ok := msg.(MouseMsg)
	if !ok {
		return false
	}
	switch mm.Button {
	case MouseButtonWheelUp:
		if ol.selected > 0 {
			ol.selected--
		}
		return true
	case MouseButtonWheelDown:
		if ol.selected < len(ol.results)-1 {
			ol.selected++
		}
		return true
	case MouseButtonLeft:
		if mm.Action != MouseActionPress {
			return false
		}
		if ol.lastListH <= 0 {
			return false
		}
		rowIdx := mm.Y - ol.lastListTop
		if rowIdx < 0 || rowIdx >= ol.lastListH {
			return false
		}
		entryIdx := ol.scroll + rowIdx
		if entryIdx < 0 || entryIdx >= len(ol.results) {
			return false
		}
		ol.selected = entryIdx
		count := ol.clicks.bump(mm)
		if count >= 2 {
			ol.load(a)
		}
		return true
	}
	return false
}

func (ol *openLayer) View(a *app) View {
	return View{AltScreen: true, MouseEnabled: true}
}

func (ol *openLayer) Hints(a *app) string {
	_ = a
	hasResults := len(ol.results) > 0
	return joinHints(
		"type=search",
		hintIf(hasResults, "Up/Dn/PgUp/PgDn=move"),
		hintIf(hasResults, "Tab=mark"),
		"Enter=load",
		"Esc=cancel",
	)
}
