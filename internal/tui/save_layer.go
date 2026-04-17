package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Nulifyer/sqlgo/internal/store"
	"github.com/Nulifyer/sqlgo/internal/tui/widget"
)

// saveLayer is the modal prompt for "Save As". Thin wrapper around
// widget.FilePicker in ModeSaveTarget: the picker owns the Dir field,
// single-level browse list (files from other exts are dimmed), filename
// input, and overwrite guard. This layer owns the tab identity + async
// scan dispatch + the actual write to disk.
type saveLayer struct {
	picker *widget.FilePicker
	tabIdx int
	status string

	// initDone is flipped on first Draw so the picker's OnDirChange
	// binds to *app exactly once and the initial scan fires.
	initDone bool
}

// newSaveLayer constructs a save dialog seeded from seed (typically the
// session's sourcePath or its title). exts is the set of allowed
// extensions cycled via the Ext row; the first entry is the default
// when seed's extension doesn't match any.
//
// When seed carries no path separator the dialog represents a new-file
// save and the directory is sourced from last_dirs (per-cwd memory),
// matching VS Code's behavior of reopening at the previously used save
// location. Save As of an existing file passes a separator-bearing seed
// and keeps the file's own directory, also like VS Code.
func newSaveLayer(a *app, tabIdx int, seed string, exts []string) *saveLayer {
	if len(exts) == 0 {
		exts = []string{".sql"}
	}
	dirText, stem, extIdx := splitSeed(seed, exts)
	if !strings.ContainsAny(seed, `/\`) {
		dirText = seedDir(a, store.LastDirSave)
	}

	choices := make([]widget.ExtChoice, 0, len(exts))
	for _, e := range exts {
		choices = append(choices, widget.ExtChoice{Ext: e})
	}
	fp := widget.NewFilePicker(widget.FilePickerOpts{
		Mode:          widget.ModeSaveTarget,
		Dir:           dirText,
		Name:          stem,
		Choices:       choices,
		InitialExtIdx: extIdx,
	})
	return &saveLayer{picker: fp, tabIdx: tabIdx}
}

// splitSeed breaks seed into (dir, stem, extIdx). If seed has no
// directory component the current working directory is used. If seed's
// extension matches one of exts, extIdx points to it; otherwise extIdx
// is 0 and the name's extension is dropped from the stem.
func splitSeed(seed string, exts []string) (dir, stem string, extIdx int) {
	if seed == "" {
		cwd, _ := os.Getwd()
		return cwd, "untitled", 0
	}
	abs, err := filepath.Abs(seed)
	if err != nil {
		abs = seed
	}
	dir = filepath.Dir(abs)
	base := filepath.Base(abs)
	ext := filepath.Ext(base)
	stem = strings.TrimSuffix(base, ext)
	extIdx = 0
	for i, e := range exts {
		if strings.EqualFold(ext, e) {
			extIdx = i
			return
		}
	}
	return
}

// triggerScan is the OnDirChange callback wired up on first Draw. It
// reads the current ScanBase from the picker, lists one directory level
// on a goroutine, and posts the result back via asyncCh. The topLayer
// check discards results when the layer has been popped; base-equality
// inside ApplyRows discards results when the user has typed past the
// base.
func (sl *saveLayer) triggerScan(a *app) {
	base := sl.picker.ScanBase()
	go func() {
		rows, err := widget.ListDir(base)
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}
		a.asyncCh <- func(app *app) {
			if top, ok := app.topLayer().(*saveLayer); !ok || top != sl {
				return
			}
			sl.picker.ApplyRows(base, rows, errStr)
		}
	}()
}

func (sl *saveLayer) Draw(a *app, c *cellbuf) {
	if !sl.initDone {
		sl.initDone = true
		sl.picker.SetOnDirChange(func() { sl.triggerScan(a) })
		sl.picker.NotifyDirChange()
	}

	boxW := 80
	if boxW > a.term.width-dialogMargin {
		boxW = a.term.width - dialogMargin
	}
	if boxW < 50 {
		boxW = 50
	}
	boxH := sl.picker.PreferredHeight(10)
	if boxH > a.term.height-dialogMargin {
		boxH = a.term.height - dialogMargin
	}
	if boxH < 12 {
		boxH = 12
	}
	row := (a.term.height - boxH) / 2
	col := (a.term.width - boxW) / 2
	if row < 1 {
		row = 1
	}
	if col < 1 {
		col = 1
	}
	r := rect{Row: row, Col: col, W: boxW, H: boxH}
	widget.DrawDialog(c, r, "Save file", true)

	sl.picker.Draw(c, r, widget.DrawOpts{
		FocusedFG: colorBorderFocused,
		DimStyle:  Style{FG: ansiBrightBlack, BG: ansiDefaultBG},
		Truncate:  truncate,
	})

	if sl.status != "" {
		innerCol := r.Col + 2
		innerW := r.W - 4
		c.SetFg(colorBorderFocused)
		c.WriteAt(r.Row+r.H-2, innerCol, truncate(sl.status, innerW))
		c.ResetStyle()
	}
}

// HandleKey routes input through the picker first, intercepting Esc
// for layer dismissal and Enter-on-name for the actual save.
func (sl *saveLayer) HandleKey(a *app, k Key) {
	if k.Kind == KeyEsc {
		a.popLayer()
		return
	}
	res := sl.picker.HandleKey(k)
	if res.SaveRequested {
		sl.save(a)
	}
}

func (sl *saveLayer) save(a *app) {
	name := strings.TrimSpace(sl.picker.NameInput.String())
	if name == "" {
		sl.status = "filename is required"
		return
	}
	full := sl.picker.Path()

	m := a.mainLayerPtr()
	if sl.tabIdx < 0 || sl.tabIdx >= len(m.sessions) {
		sl.status = "tab no longer exists"
		return
	}
	sess := m.sessions[sl.tabIdx]

	if idx := m.findTabByPath(full); idx >= 0 && idx != sl.tabIdx {
		sl.status = fmt.Sprintf("another tab already has %s open", filepath.Base(full))
		return
	}

	parent := filepath.Dir(full)
	if info, err := os.Stat(parent); err != nil || !info.IsDir() {
		sl.status = "directory does not exist: " + parent
		return
	}

	if full != sess.sourcePath {
		if !sl.picker.Guard.Check(full) {
			sl.status = "file exists -- Enter to overwrite, edit path to cancel"
			return
		}
	}

	text := sess.editor.buf.Text()
	if err := os.WriteFile(full, []byte(text), 0644); err != nil {
		sl.status = "write failed: " + err.Error()
		return
	}
	sess.sourcePath = full
	sess.savedText = text
	sess.title = filepath.Base(full)
	recordDir(a, store.LastDirSave, filepath.Dir(full))
	a.popLayer()
	m.status = fmt.Sprintf("saved %d bytes to %s", len(text), full)
}

func (sl *saveLayer) Hints(a *app) string {
	_ = a
	if sl.picker.Guard.Armed() {
		return joinHints("Enter=overwrite", "edit=cancel", "Esc=close")
	}
	hasExt := len(sl.picker.Choices) > 1
	switch sl.picker.Focus {
	case widget.FocusDir:
		return joinHints("type=dir", "Tab=next", "Enter=descend", "Esc=cancel")
	case widget.FocusList:
		has := sl.picker.HasEntries()
		return joinHints(
			hintIf(has, "Up/Dn=move"),
			"Tab=next",
			"Enter=pick",
			"Esc=cancel",
		)
	case widget.FocusInput:
		return joinHints("type=name", "Tab=next", "Enter=save", "Esc=cancel")
	case widget.FocusExt:
		_ = hasExt
		return joinHints("Up/Dn=cycle", "Tab=next", "Esc=cancel")
	}
	return joinHints("Tab=next", "Enter=save", "Esc=cancel")
}
