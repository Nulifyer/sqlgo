package ui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/tview"

	"github.com/nulifyer/sqlgo/internal/db"
	"github.com/nulifyer/sqlgo/internal/editor"
)

type focusPane int

const (
	focusExplorer focusPane = iota
	focusEditor
	focusResults
)

type Root struct {
	app             *tview.Application
	registry        *db.Registry
	store           *db.ProfileStore
	secrets         db.SecretStore
	pages           *tview.Pages
	status          *tview.TextView
	mainLayout      tview.Primitive
	explorer        *tview.TreeView
	editor          *SQLEditor
	editorStatus    *tview.TextView
	results         *tview.TextView
	sqlLens         *tview.TextView
	active          *db.ConnectionProfile
	focus           []tview.Primitive
	focusNames      []string
	focusIndex      int
	truncateResults bool
	lastResult      *db.QueryResult
	completionCache map[string]db.CompletionMetadata
}

type explorerNodeRef struct {
	Profile db.ConnectionProfile
	Object  *db.ExplorerObject
}

const lazyLoadHint = "(connect...)"

func NewRoot(registry *db.Registry) (*Root, error) {
	store, err := db.NewProfileStore()
	if err != nil {
		return nil, err
	}
	secrets, err := db.NewSecretStore()
	if err != nil {
		return nil, err
	}

	r := &Root{
		app:             tview.NewApplication(),
		registry:        registry,
		store:           store,
		secrets:         secrets,
		pages:           tview.NewPages(),
		status:          tview.NewTextView(),
		truncateResults: true,
		completionCache: map[string]db.CompletionMetadata{},
	}

	r.status.SetDynamicColors(true).SetTextAlign(tview.AlignLeft).SetBorder(true).SetTitle(" Status ")
	r.mainLayout = r.buildMainPage()
	r.pages.AddPage("main", r.mainLayout, true, true)
	return r, nil
}

func (r *Root) Run() error {
	r.active = nil
	if profiles, err := r.store.Load(); err == nil && len(profiles) == 0 {
		r.showConnectionManager()
		r.setStatusf("[yellow]no saved connections[-] create one to get started")
	}
	r.setFocusByIndex(int(focusExplorer))
	return r.app.SetRoot(r.pages, true).EnableMouse(true).Run()
}

func (r *Root) buildMainPage() tview.Primitive {
	explorer := r.buildExplorer()
	editorPane := r.buildEditor()
	results := r.buildResults()

	r.focus = []tview.Primitive{explorer, r.editor, results}
	r.focusNames = []string{"Explorer", "Editor", "Results"}

	center := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(editorPane, 0, 2, false).
		AddItem(results, 0, 3, false)

	body := tview.NewFlex().
		AddItem(explorer, 36, 0, true).
		AddItem(center, 0, 1, false)

	layout := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(body, 0, 1, true).
		AddItem(r.status, 3, 0, false)

	r.app.SetInputCapture(r.handleGlobalKeys)
	return layout
}

func (r *Root) handleGlobalKeys(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyF7 {
		if r.pages.HasPage("overlay") && !r.pages.HasPage("sql-lens") {
			return event
		}
		r.toggleSQLLens()
		return nil
	}

	if r.pages.HasPage("overlay") {
		return event
	}

	if r.focusIndex == int(focusEditor) && r.handleAutocompleteKey(event) {
		return nil
	}

	switch event.Key() {
	case tcell.KeyCtrlC:
		r.app.Stop()
		return nil
	case tcell.KeyF1:
		r.showHelp()
		return nil
	case tcell.KeyF2:
		r.showConnectionManager()
		return nil
	case tcell.KeyF4:
		r.formatEditorSQL()
		return nil
	case tcell.KeyF5:
		r.runCurrentQuery()
		return nil
	case tcell.KeyF6:
		r.setStatusf("[yellow]export not implemented yet[-] future file export will always use full untruncated data")
		return nil
	}

	if event.Key() == tcell.KeyRune && event.Modifiers()&tcell.ModAlt != 0 {
		switch event.Rune() {
		case '1':
			r.setFocusByIndex(int(focusExplorer))
			return nil
		case '2':
			r.setFocusByIndex(int(focusEditor))
			return nil
		case '3':
			r.setFocusByIndex(int(focusResults))
			return nil
		}
	}

	return event
}

func (r *Root) cycleFocus(delta int) {
	if len(r.focus) == 0 {
		return
	}
	r.focusIndex = (r.focusIndex + delta + len(r.focus)) % len(r.focus)
	r.setFocusByIndex(r.focusIndex)
}

func (r *Root) setFocusByIndex(index int) {
	if index < 0 || index >= len(r.focus) {
		return
	}
	r.focusIndex = index
	if index != int(focusEditor) {
		r.closeAutocomplete()
	}
	r.app.SetFocus(r.focus[index])
	r.updateEditorStatus()
	r.updateStatusHints("")
}

func (r *Root) buildExplorer() tview.Primitive {
	tree := tview.NewTreeView()
	r.explorer = tree
	tree.SetBorder(true).SetTitle(" Explorer ")
	tree.SetRoot(r.buildExplorerTree()).SetCurrentNode(tree.GetRoot())
	tree.SetChangedFunc(func(node *tview.TreeNode) {
		r.describeExplorerSelection(node)
	})
	tree.SetSelectedFunc(func(node *tview.TreeNode) {
		r.activateExplorerSelection(node, false)
	})
	tree.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyRune:
			switch event.Rune() {
			case ' ':
				r.activateExplorerSelection(tree.GetCurrentNode(), true)
				return nil
			case 'r':
				r.refreshExplorer(true)
				r.setStatusf("[green]explorer refreshed[-]")
				return nil
			case 'n':
				r.showConnectionManager()
				return nil
			}
		}
		return event
	})
	return tree
}

func (r *Root) describeExplorerSelection(node *tview.TreeNode) {
	if node == nil {
		return
	}
	switch typed := node.GetReference().(type) {
	case db.ConnectionProfile:
		r.updateStatusHints(fmt.Sprintf("[green]%s[-] selected  [blue]Enter[-] connect/load  [blue]F2[-] manage connections", typed.Name))
	case explorerNodeRef:
		if typed.Object == nil {
			r.updateStatusHints(fmt.Sprintf("[green]%s[-] selected  [blue]Enter[-] connect/load", typed.Profile.Name))
			return
		}
		r.updateStatusHints(fmt.Sprintf("[green]%s[-]  [blue]Enter[-] select  [blue]Space[-] preview %s", typed.Object.Name, typed.Object.Type))
	default:
		r.updateStatusHints("")
	}
}

func (r *Root) activateExplorerSelection(node *tview.TreeNode, preview bool) {
	if node == nil {
		return
	}
	switch typed := node.GetReference().(type) {
	case db.ConnectionProfile:
		r.active = &typed
		r.updateEditorStatus()
		r.loadProfileExplorerNode(node, typed)
		r.setStatusf("[green]active connection[-] %s", typed.Name)
	case explorerNodeRef:
		r.active = &typed.Profile
		r.updateEditorStatus()
		if typed.Object == nil {
			r.setStatusf("[green]active connection[-] %s", typed.Profile.Name)
			return
		}
		if preview && (typed.Object.Type == db.ExplorerTable || typed.Object.Type == db.ExplorerView) {
			r.previewExplorerObject(typed.Profile, *typed.Object)
			return
		}
		r.setStatusf("[green]selected object[-] %s.%s", typed.Profile.Name, typed.Object.Name)
	}
}

func (r *Root) buildEditor() tview.Primitive {
	textArea := NewSQLEditor()
	r.editor = textArea
	textArea.SetBorder(true).SetTitle(" Query Editor ")
	textArea.SetWrap(false).SetWordWrap(false)
	textArea.SetPlaceholder("-- Write SQL here.\n-- F4 format, F5 run, F7 SQL lens.\n")
	textArea.SetCompletionProvider(func(force bool, text string, cursor int) (editor.CompletionContext, []editor.CompletionItem, error) {
		ctx := editor.AnalyzeSQL(text, cursor).Context
		if r.active == nil {
			if force {
				r.setStatusf("[red]autocomplete blocked:[-] select an active connection first")
			}
			return ctx, nil, fmt.Errorf("no active connection")
		}

		meta, err := r.completionMetadata(*r.active)
		if err != nil {
			if force {
				r.setStatusf("[red]autocomplete unavailable:[-] %v", err)
			}
			return ctx, nil, err
		}

		items := editor.BuildCompletionItems(meta, text, ctx)
		if force && len(items) == 0 {
			r.setStatusf("[yellow]autocomplete[-] no matches for %q", ctx.Prefix)
		}
		return ctx, items, nil
	})
	textArea.SetChangedFunc(func() {
		r.updateEditorStatus()
		r.refreshSQLLens()
	})
	textArea.SetMovedFunc(func() {
		r.updateEditorStatus()
	})

	status := tview.NewTextView()
	r.editorStatus = status
	status.SetDynamicColors(true)
	status.SetBorder(true).SetTitle(" Editor Status ")

	r.updateEditorStatus()

	return tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(textArea, 0, 1, true).
		AddItem(status, 3, 0, false)
}

func (r *Root) buildResults() tview.Primitive {
	results := tview.NewTextView()
	r.results = results
	results.SetDynamicColors(true).SetWrap(false).SetWordWrap(false).SetScrollable(true).SetBorder(true).SetTitle(" Results ")
	fmt.Fprintln(results, "[gray]Ready[/gray]")
	fmt.Fprintln(results)
	fmt.Fprintln(results, "[yellow]Explorer flow:[-]")
	fmt.Fprintln(results, "  [green]-[-] select a connection")
	fmt.Fprintln(results, "  [green]-[-] use [blue]Space[-] on a table/view for preview")
	fmt.Fprintln(results, "  [green]-[-] use [blue]F5[-] from editor to run SQL")
	fmt.Fprintln(results, "  [green]-[-] results scroll with [blue]arrows[-], [blue]h/j/k/l[-], [blue]PgUp/PgDn[-]")
	fmt.Fprintln(results, "  [green]-[-] press [blue]w[-] to toggle column truncation")
	results.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyRune && event.Rune() == 'w' {
			r.truncateResults = !r.truncateResults
			mode := "off"
			if r.truncateResults {
				mode = "on"
			}
			r.refreshRenderedResult()
			r.setStatusf("[green]result column truncation[-] %s", mode)
			return nil
		}
		return event
	})
	return results
}

func (r *Root) showConnectionManager() {
	manager := newConnectionManager(r)
	r.pages.AddPage("overlay", manager.Primitive(), true, true)
	r.app.SetFocus(manager.FocusTarget())
	r.setStatusf("[blue]connection wizard[-] F2 opened connection manager")
}

func (r *Root) showHelp() {
	help := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(true).
		SetWordWrap(true).
		SetText(helpText())
	help.SetBorder(true).SetTitle(" Help ")
	help.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc || event.Key() == tcell.KeyEnter {
			r.closeOverlay()
			return nil
		}
		return event
	})
	r.pages.AddPage("overlay", help, true, true)
	r.app.SetFocus(help)
}

func helpText() string {
	return "[yellow]Global[-]\nAlt+1 Explorer\nAlt+2 Editor\nAlt+3 Results\nF1 Help\nF2 Connections\nF4 Format SQL\nF5 Run Query\nF7 SQL Lens\nEsc Close overlay\n\n[yellow]Editor[-]\nTab inserts indentation\nCtrl+Space autocomplete\nF4 format current buffer\nF5 run selection or full buffer\nF7 open syntax-highlighted SQL lens\n\n[yellow]Explorer[-]\nEnter activate selection\nSpace preview table/view\nr refresh explorer\nn new connection\n\n[yellow]Connection Wizard[-]\nTab moves between fields\nCtrl+T test connection\nCtrl+S save on review step\nDel delete selected connection"
}

func (r *Root) closeOverlay() {
	r.pages.RemovePage("overlay")
	r.refreshExplorer(true)
	r.setFocusByIndex(r.focusIndex)
}

func (r *Root) setStatusf(format string, args ...any) {
	r.status.SetText(fmt.Sprintf(format, args...))
}

func (r *Root) updateStatusHints(prefix string) {
	var hint string
	switch focusPane(r.focusIndex) {
	case focusExplorer:
		hint = "[yellow]Alt+1/2/3[-] focus  [yellow]Enter[-] connect  [yellow]Space[-] preview  [yellow]F2[-] connections"
	case focusEditor:
		hint = "[yellow]Alt+1/2/3[-] focus  [yellow]Ctrl+Space[-] complete  [yellow]F4[-] format  [yellow]F5[-] run"
	case focusResults:
		hint = "[yellow]Alt+1/2/3[-] focus  [yellow]w[-] truncate  [yellow]Arrows/PgUp/PgDn[-] scroll"
	default:
		hint = "[yellow]Alt+1/2/3[-] focus  [yellow]F2[-] connections  [yellow]F5[-] run"
	}
	if prefix == "" {
		prefix = fmt.Sprintf("[blue]%s[-]", r.focusNames[r.focusIndex])
	}
	r.status.SetText(prefix + "  " + hint)
}

func (r *Root) buildExplorerTree() *tview.TreeNode {
	root := tview.NewTreeNode("Connections")
	root.SetColor(tcell.ColorGreen)

	profiles, err := r.store.Load()
	if err != nil {
		r.setStatusf("[red]failed to load explorer profiles:[-] %v", err)
		return root
	}

	for _, profile := range profiles {
		profileRef := profile
		provider, _ := r.registry.Provider(profile.ProviderID)
		label := profile.Name
		if provider.DisplayName != "" {
			label = fmt.Sprintf("%s [%s]", profile.Name, provider.DisplayName)
		}
		profileNode := tview.NewTreeNode(label)
		profileNode.SetColor(tcell.ColorLightCyan)
		profileNode.SetReference(profileRef)
		profileNode.AddChild(tview.NewTreeNode(lazyLoadHint).SetColor(tcell.ColorGray))
		root.AddChild(profileNode)
	}

	if len(profiles) == 0 {
		root.AddChild(tview.NewTreeNode("No connections yet").SetColor(tcell.ColorYellow))
	}
	return root
}

func (r *Root) loadProfileExplorerNode(profileNode *tview.TreeNode, profile db.ConnectionProfile) {
	children := profileNode.GetChildren()
	if len(children) > 0 && children[0].GetText() != lazyLoadHint {
		return
	}

	profileNode.ClearChildren()
	r.attachProfileChildren(profileNode, profile)
}

func (r *Root) attachProfileChildren(profileNode *tview.TreeNode, profile db.ConnectionProfile) {
	snapshot, err := db.LoadExplorerSnapshotWithSecrets(context.Background(), profile, r.registry, r.secrets)
	if err != nil {
		profileNode.AddChild(tview.NewTreeNode("(browse coming soon)").SetColor(tcell.ColorGray))
		return
	}
	for _, database := range snapshot.Databases {
		databaseRef := database
		databaseNode := tview.NewTreeNode(database.Name)
		databaseNode.SetColor(tcell.ColorLightBlue)
		databaseNode.SetReference(explorerNodeRef{Profile: profile, Object: &databaseRef})

		tablesNode := tview.NewTreeNode("Tables")
		tablesNode.SetColor(tcell.ColorYellow)
		for _, table := range snapshot.Tables {
			tableRef := table
			child := tview.NewTreeNode(table.Name)
			child.SetColor(tcell.ColorGreen)
			child.SetReference(explorerNodeRef{Profile: profile, Object: &tableRef})
			tablesNode.AddChild(child)
		}
		viewsNode := tview.NewTreeNode("Views")
		viewsNode.SetColor(tcell.ColorDarkCyan)
		for _, view := range snapshot.Views {
			viewRef := view
			child := tview.NewTreeNode(view.Name)
			child.SetColor(tcell.ColorTurquoise)
			child.SetReference(explorerNodeRef{Profile: profile, Object: &viewRef})
			viewsNode.AddChild(child)
		}

		databaseNode.AddChild(tablesNode)
		databaseNode.AddChild(viewsNode)
		profileNode.AddChild(databaseNode)
	}
}

func (r *Root) refreshExplorer(preserveActive bool) {
	if r.explorer == nil {
		return
	}
	activeName := ""
	if preserveActive && r.active != nil {
		activeName = r.active.Name
	}
	root := r.buildExplorerTree()
	r.explorer.SetRoot(root).SetCurrentNode(root)
	r.active = activeProfileByName(activeName, root.GetChildren())
	r.completionCache = map[string]db.CompletionMetadata{}
	r.updateEditorStatus()
	r.updateStatusHints("")
}

func activeProfileByName(name string, nodes []*tview.TreeNode) *db.ConnectionProfile {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	for _, node := range nodes {
		profile, ok := node.GetReference().(db.ConnectionProfile)
		if !ok || profile.Name != name {
			continue
		}
		profileCopy := profile
		return &profileCopy
	}
	return nil
}

func (r *Root) runCurrentQuery() {
	if r.active == nil {
		r.setStatusf("[red]run blocked:[-] select an active connection in Explorer first")
		return
	}
	sqlText, scope := r.editorQueryText()
	if sqlText == "" {
		r.setStatusf("[red]run blocked:[-] query editor is empty")
		return
	}

	profile := *r.active
	r.lastResult = nil
	r.results.SetText("[yellow]Running query...[-]")
	r.setStatusf("[yellow]running query[-] %s  [gray](%s)[-]", profile.Name, scope)
	go func() {
		result, err := db.RunQueryWithSecrets(context.Background(), profile, r.registry, r.secrets, sqlText)
		r.app.QueueUpdateDraw(func() {
			if err != nil {
				r.results.SetText(fmt.Sprintf("[red]Query failed[-]\n\nProfile: %s\n\n%v", profile.Name, err))
				r.setStatusf("[red]query failed:[-] %v", err)
				return
			}
			r.lastResult = &result
			r.results.SetText(renderQueryResult(result, r.truncateResults))
			r.setStatusf("[green]query complete[-] %s  duration=%s", profile.Name, result.Duration.Round(10_000_000))
		})
	}()
}

func (r *Root) previewExplorerObject(profile db.ConnectionProfile, object db.ExplorerObject) {
	if object.Type != db.ExplorerTable && object.Type != db.ExplorerView {
		r.setStatusf("[green]active connection[-] %s", profile.Name)
		return
	}
	sqlText := fmt.Sprintf("SELECT * FROM %s LIMIT 25;", object.Qualified)
	r.results.SetText(fmt.Sprintf("[yellow]Previewing %s...[-]", object.Name))
	r.setStatusf("[yellow]previewing[-] %s.%s", profile.Name, object.Name)
	go func() {
		result, err := db.RunQueryWithSecrets(context.Background(), profile, r.registry, r.secrets, sqlText)
		r.app.QueueUpdateDraw(func() {
			if err != nil {
				r.results.SetText(fmt.Sprintf("[red]Preview failed[-]\n\nProfile: %s\nObject: %s\n\n%v", profile.Name, object.Name, err))
				r.setStatusf("[red]preview failed:[-] %v", err)
				return
			}
			r.lastResult = &result
			r.results.SetText(renderQueryResult(result, r.truncateResults))
			r.setStatusf("[green]preview ready[-] %s.%s", profile.Name, object.Name)
		})
	}()
}

func (r *Root) editorQueryText() (string, string) {
	selected, _, _ := r.editor.GetSelection()
	if trimmed := strings.TrimSpace(selected); trimmed != "" {
		return trimmed, "selection"
	}
	return strings.TrimSpace(r.editor.GetText()), "buffer"
}

func (r *Root) formatEditorSQL() {
	if r.editor == nil {
		return
	}

	current := r.editor.GetText()
	if strings.TrimSpace(current) == "" {
		r.setStatusf("[yellow]format skipped[-] query editor is empty")
		return
	}

	formatted := editor.FormatSQL(current)
	if formatted == current {
		r.setStatusf("[green]format complete[-] SQL already looks tidy")
		return
	}

	r.editor.SetText(formatted, false)
	r.editor.Select(0, 0)
	r.closeAutocomplete()
	r.refreshSQLLens()
	r.setStatusf("[green]format complete[-] editor buffer updated")
}

func (r *Root) toggleSQLLens() {
	if r.pages.HasPage("sql-lens") {
		r.pages.RemovePage("sql-lens")
		r.sqlLens = nil
		r.setFocusByIndex(r.focusIndex)
		r.setStatusf("[green]sql lens[-] closed")
		return
	}

	lens := tview.NewTextView()
	r.sqlLens = lens
	lens.SetDynamicColors(true).SetWrap(false).SetWordWrap(false).SetScrollable(true)
	lens.SetBorder(true).SetTitle(" SQL Lens ")
	lens.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEsc, tcell.KeyF7:
			r.toggleSQLLens()
			return nil
		case tcell.KeyF4:
			r.toggleSQLLens()
			r.formatEditorSQL()
			return nil
		}
		return event
	})
	r.refreshSQLLens()
	r.pages.AddPage("sql-lens", lens, true, true)
	r.app.SetFocus(lens)
	r.setStatusf("[green]sql lens[-] opened")
}

func (r *Root) showAutocomplete() {
	if r.editor == nil {
		return
	}
	r.editor.TriggerAutocomplete()
}

func (r *Root) closeAutocomplete() {
	if r.editor != nil {
		r.editor.HideAutocomplete()
	}
	r.updateStatusHints("")
}

func (r *Root) completionMetadata(profile db.ConnectionProfile) (db.CompletionMetadata, error) {
	if meta, ok := r.completionCache[profile.Name]; ok {
		return meta, nil
	}
	meta, err := db.LoadCompletionMetadataWithSecrets(context.Background(), profile, r.registry, r.secrets)
	if err != nil {
		return db.CompletionMetadata{}, err
	}
	slices.Sort(meta.Catalogs)
	slices.Sort(meta.Schemas)
	r.completionCache[profile.Name] = meta
	return meta, nil
}

func (r *Root) refreshSQLLens() {
	if r.sqlLens == nil {
		return
	}
	var b strings.Builder
	b.WriteString("[gray]Esc/F7 close  F4 format in editor[-]\n\n")
	b.WriteString(editor.HighlightSQL(r.editor.GetText()))
	r.sqlLens.SetText(b.String())
}

func (r *Root) refreshRenderedResult() {
	if r.lastResult == nil {
		return
	}
	r.results.SetText(renderQueryResult(*r.lastResult, r.truncateResults))
}

func (r *Root) updateEditorStatus() {
	if r.editor == nil || r.editorStatus == nil {
		return
	}

	_, _, toRow, toColumn := r.editor.GetCursor()
	selection, _, _ := r.editor.GetSelection()
	connection := "[gray]none[-]"
	if r.active != nil {
		connection = fmt.Sprintf("[green]%s[-]", tview.Escape(r.active.Name))
	}

	selectionInfo := "[gray]0[-]"
	mode := "buffer"
	if selection != "" {
		selectionInfo = fmt.Sprintf("[yellow]%d[-]", utf8.RuneCountInString(selection))
		mode = "selection"
	}

	r.editorStatus.SetText(fmt.Sprintf(
		"Conn %s  [yellow]Ln[-] %d  [yellow]Col[-] %d  [yellow]Sel[-] %s  [yellow]Run[-] %s  [yellow]Keys[-] F4 format  F5 run  F7 lens",
		connection,
		toRow+1,
		toColumn+1,
		selectionInfo,
		mode,
	))
}

func renderQueryResult(result db.QueryResult, truncate bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[green]%s[-]\n", result.Profile.Name)
	fmt.Fprintf(&b, "[yellow]Provider:[-] [blue]%s[-]\n", result.Provider.DisplayName)
	fmt.Fprintf(&b, "[yellow]Executed:[-] %s UTC\n", result.ExecutedAtUTC.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "[yellow]Duration:[-] %s\n\n", result.Duration.Round(10_000_000))
	if !result.IsQuery {
		fmt.Fprintf(&b, "[green]%s[-]\n", result.Message)
		return b.String()
	}
	fmt.Fprintf(&b, "[green]%s[-]\n", result.Message)
	if result.Truncated {
		fmt.Fprintf(&b, "[yellow]Preview limited to first %d rows[-]\n", len(result.Rows))
	}
	if truncate {
		fmt.Fprintf(&b, "[yellow]Display truncation[-] on  [gray](press w in Results to toggle)[-]\n")
	} else {
		fmt.Fprintf(&b, "[yellow]Display truncation[-] off  [gray](press w in Results to toggle)[-]\n")
	}
	fmt.Fprintln(&b)
	if len(result.Columns) > 0 {
		widths := computeColumnWidths(result.Columns, result.Rows, truncate)
		fmt.Fprintf(&b, "%s\n", renderHeaderRow(result.Columns, widths))
		fmt.Fprintf(&b, "%s\n", renderDividerRow(widths))
		for _, row := range result.Rows {
			fmt.Fprintf(&b, "%s\n", renderDataRow(row, widths, truncate))
		}
	}
	return b.String()
}

const maxDisplayColumnWidth = 40

func computeColumnWidths(columns []string, rows [][]string, truncate bool) []int {
	widths := make([]int, len(columns))
	for i, col := range columns {
		widths[i] = runeWidth(col)
	}
	for _, row := range rows {
		for i := range columns {
			if i >= len(row) {
				continue
			}
			width := displayCellWidth(row[i], truncate)
			if width > widths[i] {
				widths[i] = width
			}
		}
	}
	return widths
}

func renderHeaderRow(columns []string, widths []int) string {
	cells := make([]string, 0, len(columns))
	for i, col := range columns {
		cells = append(cells, formatCell("[yellow]"+tview.Escape(col)+"[-]", runeWidth(col), widths[i]))
	}
	return strings.Join(cells, " [gray]|[-] ") + " "
}

func renderDividerRow(widths []int) string {
	parts := make([]string, 0, len(widths))
	for _, width := range widths {
		parts = append(parts, "[gray]"+strings.Repeat("-", width)+"[-]")
	}
	return strings.Join(parts, "[gray]-+-[-]")
}

func renderDataRow(row []string, widths []int, truncate bool) string {
	cells := make([]string, 0, len(widths))
	for i, width := range widths {
		value := ""
		if i < len(row) {
			value = row[i]
		}
		styled, visible := renderCellValue(value, truncate)
		cells = append(cells, formatCell(styled, visible, width))
	}
	return strings.Join(cells, " [gray]|[-] ") + " "
}

func renderCellValue(value string, truncate bool) (string, int) {
	if value == "NULL" {
		return "[gray]NULL[-]", 4
	}
	escaped, visible := escapeControlChars(value)
	if truncate && visible > maxDisplayColumnWidth {
		return truncateStyledCell(escaped, maxDisplayColumnWidth), maxDisplayColumnWidth
	}
	return escaped, visible
}

func formatCell(styled string, currentWidth, targetWidth int) string {
	if currentWidth >= targetWidth {
		return styled
	}
	return styled + strings.Repeat(" ", targetWidth-currentWidth)
}

func escapeControlChars(value string) (string, int) {
	var b strings.Builder
	visible := 0

	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '\r':
			if i+1 < len(value) && value[i+1] == '\n' {
				token := `\r\n`
				b.WriteString(controlToken(token))
				visible += len(token)
				i++
				continue
			}
			token := `\r`
			b.WriteString(controlToken(token))
			visible += len(token)
		case '\n':
			token := `\n`
			b.WriteString(controlToken(token))
			visible += len(token)
		case '\t':
			token := `\t`
			b.WriteString(controlToken(token))
			visible += len(token)
		default:
			r, size := decodeRune(value[i:])
			b.WriteString(tview.Escape(value[i : i+size]))
			visible += runewidth.RuneWidth(r)
			i += size - 1
		}
	}

	return b.String(), visible
}

func controlToken(text string) string {
	return "[darkcyan::b]" + tview.Escape(text) + "[-:-:-]"
}

func displayCellWidth(value string, truncate bool) int {
	if value == "NULL" {
		return 4
	}
	_, width := escapeControlChars(value)
	if truncate && width > maxDisplayColumnWidth {
		return maxDisplayColumnWidth
	}
	return width
}

func runeWidth(value string) int {
	return runewidth.StringWidth(value)
}

func truncateStyledCell(styled string, maxWidth int) string {
	if maxWidth <= 3 {
		return styled
	}
	plain := stripStyleTags(styled)
	runes := []rune(plain)
	if runewidth.StringWidth(plain) <= maxWidth {
		return styled
	}
	ellipsis := "..."
	budget := maxWidth - runewidth.StringWidth(ellipsis)
	if budget <= 0 {
		return "[gray]...[-]"
	}
	var b strings.Builder
	current := 0
	for _, r := range runes {
		width := runewidth.RuneWidth(r)
		if current+width > budget {
			break
		}
		b.WriteRune(r)
		current += width
	}
	return tview.Escape(b.String()) + "[gray]...[-]"
}

func stripStyleTags(value string) string {
	var b strings.Builder
	inTag := false
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '[':
			inTag = true
		case ']':
			if inTag {
				inTag = false
			} else {
				b.WriteByte(value[i])
			}
		default:
			if !inTag {
				b.WriteByte(value[i])
			}
		}
	}
	return b.String()
}

func decodeRune(value string) (rune, int) {
	for _, r := range value {
		return r, len(string(r))
	}
	return rune(0), 0
}

func centerModal(p tview.Primitive, width, height int) tview.Primitive {
	return tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().
			SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(p, height, 1, true).
			AddItem(nil, 0, 1, false), width, 1, true).
		AddItem(nil, 0, 1, false)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func fallbackText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (r *Root) handleAutocompleteKey(event *tcell.EventKey) bool {
	if r.editor == nil {
		return false
	}
	return r.editor.HandleAutocompleteKey(event)
}

func isAutocompleteTrigger(event *tcell.EventKey) bool {
	if event == nil {
		return false
	}

	switch event.Key() {
	case tcell.KeyCtrlSpace, tcell.KeyNUL:
		return true
	case tcell.Key(' '):
		return event.Modifiers()&tcell.ModCtrl != 0 && event.Modifiers()&tcell.ModAlt == 0
	case tcell.KeyRune:
		if event.Modifiers()&tcell.ModCtrl == 0 || event.Modifiers()&tcell.ModAlt != 0 {
			return false
		}
		switch event.Rune() {
		case ' ', '@', '2':
			return true
		}
	}

	return false
}
