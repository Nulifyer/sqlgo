package ui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"sqlgo/internal/db"
)

type Root struct {
	app      *tview.Application
	registry *db.Registry
	store    *db.ProfileStore
	pages    *tview.Pages
	status   *tview.TextView
	focus    []tview.Primitive
}

func NewRoot(registry *db.Registry) (*Root, error) {
	store, err := db.NewProfileStore()
	if err != nil {
		return nil, err
	}

	r := &Root{
		app:      tview.NewApplication(),
		registry: registry,
		store:    store,
		pages:    tview.NewPages(),
		status:   tview.NewTextView(),
	}

	r.status.
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft).
		SetBorder(true).
		SetTitle(" Status ")

	r.pages.AddPage("main", r.buildMainPage(), true, true)

	return r, nil
}

func (r *Root) Run() error {
	return r.app.SetRoot(r.pages, true).EnableMouse(true).Run()
}

func (r *Root) buildMainPage() tview.Primitive {
	explorer := r.buildExplorer()
	editor := r.buildEditor()
	results := r.buildResults()
	r.focus = []tview.Primitive{explorer, editor, results}

	center := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(editor, 0, 2, true).
		AddItem(results, 0, 3, false)

	body := tview.NewFlex().
		AddItem(explorer, 34, 0, false).
		AddItem(center, 0, 1, true)

	layout := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(body, 0, 1, true).
		AddItem(r.status, 3, 0, false)

	r.status.SetText("[yellow]Ctrl+C[-] quit  [yellow]Tab[-] cycle focus  [yellow]F2[-] connections  [yellow]F5[-] run query  [yellow]F6[-] export CSV")

	r.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyCtrlC:
			r.app.Stop()
			return nil
		case tcell.KeyTAB:
			r.cycleFocus()
			return event
		case tcell.KeyF2:
			r.showConnectionManager()
			return nil
		}
		return event
	})

	return layout
}

func (r *Root) cycleFocus() {
	current := r.app.GetFocus()
	if current == nil || len(r.focus) == 0 {
		return
	}

	for i, primitive := range r.focus {
		if primitive == current {
			r.app.SetFocus(r.focus[(i+1)%len(r.focus)])
			return
		}
	}

	r.app.SetFocus(r.focus[0])
}

func (r *Root) buildExplorer() tview.Primitive {
	tree := tview.NewTreeView()
	tree.SetBorder(true).SetTitle(" Explorer ")

	root := tview.NewTreeNode("Connections")
	root.SetColor(tcell.ColorGreen)

	for _, provider := range r.registry.Providers() {
		line := provider.DisplayName
		if provider.Capabilities.Experimental {
			line += " (experimental)"
		}
		node := tview.NewTreeNode(line)
		node.SetReference(provider)
		root.AddChild(node)
	}

	tree.SetRoot(root).SetCurrentNode(root)
	tree.SetSelectedFunc(func(node *tview.TreeNode) {
		ref := node.GetReference()
		if provider, ok := ref.(db.Provider); ok {
			r.status.SetText(fmt.Sprintf("[green]%s[-] driver=[blue]%s[-] pure_go=%t experimental=%t", provider.DisplayName, provider.DriverName, provider.Capabilities.PureGo, provider.Capabilities.Experimental))
		}
	})

	return tree
}

func (r *Root) buildEditor() tview.Primitive {
	editor := tview.NewTextArea()
	editor.SetBorder(true).SetTitle(" Query Editor ")
	editor.SetText("-- SQLGo scaffold\n-- F5 will execute the current buffer once the query runner exists.\nSELECT 1;\n", false)
	return editor
}

func (r *Root) buildResults() tview.Primitive {
	results := tview.NewTextView()
	results.
		SetDynamicColors(true).
		SetWrap(true).
		SetWordWrap(true).
		SetBorder(true).
		SetTitle(" Results ")

	fmt.Fprintln(results, "[gray]Result grid scaffold[/gray]")
	fmt.Fprintln(results, "")
	fmt.Fprintln(results, "Planned next:")
	fmt.Fprintln(results, "  - streaming query execution")
	fmt.Fprintln(results, "  - virtualized row viewport")
	fmt.Fprintln(results, "  - CSV export")
	fmt.Fprintln(results, "  - transaction guard flow")

	return results
}

func (r *Root) showConnectionManager() {
	manager := newConnectionManager(r)
	r.pages.AddPage("overlay", manager.Primitive(), true, true)
	r.app.SetFocus(manager.FocusTarget())
	r.setStatusf("[blue]connection profiles[-] %s", r.store.Path())
}

func (r *Root) closeOverlay() {
	r.pages.RemovePage("overlay")
	if len(r.focus) > 0 {
		r.app.SetFocus(r.focus[0])
	}
}

func (r *Root) setStatusf(format string, args ...any) {
	r.status.SetText(fmt.Sprintf(format, args...))
}
