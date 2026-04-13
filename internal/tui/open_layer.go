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
	ol := &openLayer{search: newInput(seed)}
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
		ol.status = fmt.Sprintf("%d / %d files", len(ol.results), len(ol.all))
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
			if idx == ol.selected {
				c.setFg(colorBorderFocused)
				c.writeAt(listTop+i, innerCol, truncate("▶ "+e.rel, boxW-4))
				c.resetStyle()
			} else {
				c.writeAt(listTop+i, innerCol, truncate("  "+e.rel, boxW-4))
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
	case KeyEnter:
		ol.load(a)
		return
	}
	ol.search.handle(k)
	ol.filter()
}

// load resolves the target path: if the results list has a highlighted
// entry, load it; otherwise treat the search box contents as a path and
// load that directly. The direct-path fallback preserves the original
// openLayer behavior for users who already know the file they want.
func (ol *openLayer) load(a *app) {
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
	data, err := os.ReadFile(path)
	if err != nil {
		ol.status = "read failed: " + err.Error()
		return
	}
	m := a.mainLayerPtr()
	m.editor.buf.SetText(string(data))
	a.popLayer()
	m.status = fmt.Sprintf("loaded %d bytes from %s", len(data), path)
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
		"Enter=load",
		"Esc=cancel",
	)
}
