package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nulifyer/sqlgo/internal/db"
)

type Root struct {
	app      *tview.Application
	registry *db.Registry
	store    *db.ProfileStore
	pages    *tview.Pages
	status   *tview.TextView
	focus    []tview.Primitive
	explorer *tview.TreeView
	editor   *tview.TextArea
	results  *tview.TextView
	active   *db.ConnectionProfile
}

type explorerNodeRef struct {
	Profile db.ConnectionProfile
	Object  *db.ExplorerObject
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
		case tcell.KeyF5:
			r.runCurrentQuery()
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
	r.explorer = tree
	tree.SetBorder(true).SetTitle(" Explorer ")
	tree.SetRoot(r.buildExplorerTree()).SetCurrentNode(tree.GetRoot())
	tree.SetSelectedFunc(func(node *tview.TreeNode) {
		ref := node.GetReference()
		switch typed := ref.(type) {
		case db.Provider:
			r.status.SetText(fmt.Sprintf("[green]%s[-] driver=[blue]%s[-] pure_go=%t experimental=%t", typed.DisplayName, typed.DriverName, typed.Capabilities.PureGo, typed.Capabilities.Experimental))
		case db.ConnectionProfile:
			provider, _ := r.registry.Provider(typed.ProviderID)
			r.active = &typed
			r.setStatusf("[green]active profile[-] %s  [blue]provider[-] %s", typed.Name, provider.DisplayName)
		case explorerNodeRef:
			r.active = &typed.Profile
			if typed.Object == nil {
				r.setStatusf("[green]active profile[-] %s", typed.Profile.Name)
				return
			}
			r.previewExplorerObject(typed.Profile, *typed.Object)
		}
	})

	return tree
}

func (r *Root) buildEditor() tview.Primitive {
	editor := tview.NewTextArea()
	r.editor = editor
	editor.SetBorder(true).SetTitle(" Query Editor ")
	editor.SetText("-- Select a saved profile in the explorer, then press F5.\nSELECT 1;\n", false)
	return editor
}

func (r *Root) buildResults() tview.Primitive {
	results := tview.NewTextView()
	r.results = results
	results.
		SetDynamicColors(true).
		SetWrap(true).
		SetWordWrap(true).
		SetBorder(true).
		SetTitle(" Results ")

	fmt.Fprintln(results, "[gray]Result preview[/gray]")
	fmt.Fprintln(results, "")
	fmt.Fprintln(results, "Planned next:")
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
	r.refreshExplorer()
	if len(r.focus) > 0 {
		r.app.SetFocus(r.focus[0])
	}
}

func (r *Root) setStatusf(format string, args ...any) {
	r.status.SetText(fmt.Sprintf(format, args...))
}

func (r *Root) buildExplorerTree() *tview.TreeNode {
	root := tview.NewTreeNode("Explorer")
	root.SetColor(tcell.ColorGreen)

	profiles, err := r.store.Load()
	if err != nil {
		r.setStatusf("[red]failed to load explorer profiles:[-] %v", err)
		return root
	}

	for _, provider := range r.registry.Providers() {
		line := provider.DisplayName
		if provider.Capabilities.Experimental {
			line += " (experimental)"
		}
		providerNode := tview.NewTreeNode(line)
		providerNode.SetReference(provider)

		for _, profile := range profiles {
			if profile.ProviderID != provider.ID {
				continue
			}
			profileNode := tview.NewTreeNode(profile.Name)
			profileNode.SetReference(profile)
			r.attachProfileChildren(profileNode, profile)
			providerNode.AddChild(profileNode)
		}

		root.AddChild(providerNode)
	}

	return root
}

func (r *Root) attachProfileChildren(profileNode *tview.TreeNode, profile db.ConnectionProfile) {
	snapshot, err := db.LoadExplorerSnapshot(context.Background(), profile, r.registry)
	if err != nil {
		profileNode.AddChild(tview.NewTreeNode("(browse coming soon)").SetColor(tcell.ColorGray))
		return
	}

	for _, database := range snapshot.Databases {
		databaseRef := database
		databaseNode := tview.NewTreeNode(database.Name)
		databaseNode.SetReference(explorerNodeRef{Profile: profile, Object: &databaseRef})

		tablesNode := tview.NewTreeNode("Tables")
		for _, table := range snapshot.Tables {
			tableRef := table
			child := tview.NewTreeNode(table.Name)
			child.SetReference(explorerNodeRef{Profile: profile, Object: &tableRef})
			tablesNode.AddChild(child)
		}

		viewsNode := tview.NewTreeNode("Views")
		for _, view := range snapshot.Views {
			viewRef := view
			child := tview.NewTreeNode(view.Name)
			child.SetReference(explorerNodeRef{Profile: profile, Object: &viewRef})
			viewsNode.AddChild(child)
		}

		databaseNode.AddChild(tablesNode)
		databaseNode.AddChild(viewsNode)
		profileNode.AddChild(databaseNode)
	}
}

func (r *Root) refreshExplorer() {
	if r.explorer == nil {
		return
	}
	root := r.buildExplorerTree()
	r.explorer.SetRoot(root).SetCurrentNode(root)
}

func (r *Root) runCurrentQuery() {
	if r.active == nil {
		r.setStatusf("[red]run blocked:[-] select a saved profile in the explorer first")
		return
	}

	sqlText := strings.TrimSpace(r.editor.GetText())
	if sqlText == "" {
		r.setStatusf("[red]run blocked:[-] query editor is empty")
		return
	}

	profile := *r.active
	r.results.SetText("[yellow]Running query...[-]")
	r.setStatusf("[yellow]running query[-] %s", profile.Name)

	go func() {
		result, err := db.RunQuery(context.Background(), profile, r.registry, sqlText)
		r.app.QueueUpdateDraw(func() {
			if err != nil {
				r.results.SetText(fmt.Sprintf("[red]Query failed[-]\n\nProfile: %s\n\n%v", profile.Name, err))
				r.setStatusf("[red]query failed:[-] %v", err)
				return
			}

			r.results.SetText(renderQueryResult(result))
			r.setStatusf("[green]query complete[-] %s  duration=%s", profile.Name, result.Duration.Round(10_000_000))
		})
	}()
}

func (r *Root) previewExplorerObject(profile db.ConnectionProfile, object db.ExplorerObject) {
	if object.Type != db.ExplorerTable && object.Type != db.ExplorerView {
		r.setStatusf("[green]active profile[-] %s", profile.Name)
		return
	}

	sqlText := fmt.Sprintf("SELECT * FROM %s LIMIT 25;", object.Qualified)
	r.results.SetText(fmt.Sprintf("[yellow]Previewing %s...[-]", object.Name))
	r.setStatusf("[yellow]previewing[-] %s.%s", profile.Name, object.Name)

	go func() {
		result, err := db.RunQuery(context.Background(), profile, r.registry, sqlText)
		r.app.QueueUpdateDraw(func() {
			if err != nil {
				r.results.SetText(fmt.Sprintf("[red]Preview failed[-]\n\nProfile: %s\nObject: %s\n\n%v", profile.Name, object.Name, err))
				r.setStatusf("[red]preview failed:[-] %v", err)
				return
			}
			r.results.SetText(renderQueryResult(result))
			r.setStatusf("[green]preview ready[-] %s.%s", profile.Name, object.Name)
		})
	}()
}

func renderQueryResult(result db.QueryResult) string {
	var b strings.Builder

	fmt.Fprintf(&b, "[green]%s[-]\n", result.Profile.Name)
	fmt.Fprintf(&b, "Provider: [blue]%s[-]\n", result.Provider.DisplayName)
	fmt.Fprintf(&b, "Executed: %s UTC\n", result.ExecutedAtUTC.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "Duration: %s\n\n", result.Duration.Round(10_000_000))

	if !result.IsQuery {
		fmt.Fprintf(&b, "%s\n", result.Message)
		return b.String()
	}

	fmt.Fprintf(&b, "%s\n", result.Message)
	if result.Truncated {
		fmt.Fprintf(&b, "[yellow]Preview limited to first %d rows[-]\n", len(result.Rows))
	}
	fmt.Fprintln(&b)

	if len(result.Columns) > 0 {
		fmt.Fprintf(&b, "%s\n", strings.Join(result.Columns, " | "))
		fmt.Fprintf(&b, "%s\n", strings.Repeat("-", len(strings.Join(result.Columns, " | "))))
	}

	for _, row := range result.Rows {
		fmt.Fprintf(&b, "%s\n", strings.Join(row, " | "))
	}

	return b.String()
}
