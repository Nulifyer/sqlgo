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
	pages    *tview.Pages
	status   *tview.TextView
}

func NewRoot(registry *db.Registry) *Root {
	r := &Root{
		app:      tview.NewApplication(),
		registry: registry,
		pages:    tview.NewPages(),
		status:   tview.NewTextView(),
	}

	r.status.
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft).
		SetBorder(true).
		SetTitle(" Status ")

	r.pages.AddPage("main", r.buildMainPage(), true, true)

	return r
}

func (r *Root) Run() error {
	return r.app.SetRoot(r.pages, true).EnableMouse(true).Run()
}

func (r *Root) buildMainPage() tview.Primitive {
	explorer := r.buildExplorer()
	editor := r.buildEditor()
	results := r.buildResults()

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
			r.app.SetFocus(r.app.GetFocus().(tview.Primitive))
			return event
		case tcell.KeyF2:
			r.showConnectionModal()
			return nil
		}
		return event
	})

	return layout
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

func (r *Root) showConnectionModal() {
	modal := tview.NewModal().
		SetText("Connection manager is the next major feature.\n\nThe provider registry is already in place for SQL Server, Azure SQL, PostgreSQL, MySQL, SQLite, Snowflake, and Sybase ASE.").
		AddButtons([]string{"Close"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			r.pages.RemovePage("modal")
		})

	modal.SetBorder(true).SetTitle(" Connections ")
	r.pages.AddPage("modal", modal, true, true)
	r.app.SetFocus(modal)
}
