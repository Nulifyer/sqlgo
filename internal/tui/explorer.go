package tui

import (
	"sort"
	"strings"

	"github.com/Nulifyer/sqlgo/internal/db"
)

// explorerItem is a single visible line in the tree. Keeping items flat (one
// entry per visible row) lets us index the cursor with a plain int and makes
// scrolling trivial: just clamp index+scroll.
//
// The tree has three levels: schema → subgroup (Tables/Views) → leaf. Each
// level can be collapsed independently; expansion state lives on the parent
// explorer's `expanded` map keyed by expansionKey(item).
type explorerItem struct {
	kind       explorerItemKind
	label      string        // display text WITHOUT the indent/marker
	catalog    string        // owning database for DB-tier mode; empty in single-DB mode
	schemaName string        // owning schema (set for all kinds except itemDatabase)
	subgroup   subgroupKind  // valid for itemSubgroup; also set on leaves so Toggle knows which group they belong to
	table      db.TableRef   // valid only for itemTable / itemView
	routine    db.RoutineRef // valid only for itemProcedure / itemFunction
	trigger    db.TriggerRef // valid only for itemTrigger
	suffix     string        // optional trailing hint (e.g. "(denied)", "AFTER INSERT on foo")
}

type explorerItemKind int

const (
	itemSchema explorerItemKind = iota
	itemSubgroup
	itemTable
	itemView
	itemProcedure
	itemFunction
	itemTrigger
	itemDatabase
)

// subgroupKind distinguishes the children a schema can have.
type subgroupKind int

const (
	subgroupNone subgroupKind = iota
	subgroupTables
	subgroupViews
	subgroupProcedures
	subgroupFunctions
	subgroupTriggers
)

func (s subgroupKind) label() string {
	switch s {
	case subgroupTables:
		return "Tables"
	case subgroupViews:
		return "Views"
	case subgroupProcedures:
		return "Procedures"
	case subgroupFunctions:
		return "Functions"
	case subgroupTriggers:
		return "Triggers"
	}
	return ""
}

// allSubgroups lists the subgroups in the render order used by rebuild
// and SetSchema (for seeding expansion state).
var allSubgroups = []subgroupKind{
	subgroupTables,
	subgroupViews,
	subgroupProcedures,
	subgroupFunctions,
	subgroupTriggers,
}

// sysSchemaSentinel is the synthetic schema name used for the top-level
// "Sys" pseudo-schema that groups every System-flagged table/view
// regardless of its physical schema. Non-empty so renderExplorerLine
// uses the schemas-mode indent for its subgroups/leaves.
const sysSchemaSentinel = "\x00sys"

// explorer renders a collapsible schema tree. Selection and scroll live on
// the widget; the main layer reads them to know which table to prefill a
// SELECT for.
type explorer struct {
	info     *db.SchemaInfo
	depth    db.SchemaDepth // rendering mode: flat or schemas
	expanded map[string]bool
	items    []explorerItem // rebuilt from info+expanded on SetSchema / toggle
	cursor   int            // index into items
	scroll   int            // first visible item
	err      string         // non-empty when schema load failed
	loading  string         // non-empty while a schema fetch is in flight; holds current spinner frame

	// DB-tier mode (SupportsCrossDatabase + blank default database).
	// dbMode flips rebuild into a top-level list of databases; each
	// expanded entry draws the standard schema tier from dbSchemas[name].
	dbMode    bool
	databases []string
	dbSchemas map[string]*db.SchemaInfo // catalog -> loaded schema (absent == not yet fetched)
	dbLoading map[string]string         // catalog -> current spinner frame while a fetch is in flight
	dbErr     map[string]string         // catalog -> load error message
}

func newExplorer() *explorer {
	return &explorer{
		expanded:  map[string]bool{},
		dbSchemas: map[string]*db.SchemaInfo{},
		dbLoading: map[string]string{},
		dbErr:     map[string]string{},
	}
}

// SetSchema replaces the displayed schema and resets cursor/scroll.
// depth controls whether a schema header is emitted above the
// Tables/Views subgroups: Schemas for Postgres/MSSQL/MySQL, Flat for
// SQLite (which has no schema concept). Schemas and both subgroups
// start expanded the first time we see them so the user doesn't land
// on a wall of closed groups after connecting.
func (e *explorer) SetSchema(info *db.SchemaInfo, depth db.SchemaDepth) {
	e.info = info
	e.depth = depth
	e.err = ""
	e.loading = ""
	e.cursor = 0
	e.scroll = 0
	if info != nil {
		e.seedExpansion("", info, depth)
	}
	e.rebuild()
}

// seedExpansion marks schemas + default subgroups as expanded the first
// time we see them under `catalog`. Shared by single-DB SetSchema and
// per-DB SetDatabaseSchema so both tiers open to a usable default.
func (e *explorer) seedExpansion(catalog string, info *db.SchemaInfo, depth db.SchemaDepth) {
	defaultExpandedSubgroups := []subgroupKind{subgroupTables, subgroupViews}
	seedSchema := func(s string) {
		sk := schemaExpansionKey(catalog, s)
		if _, seen := e.expanded[sk]; !seen {
			e.expanded[sk] = true
		}
		for _, sg := range defaultExpandedSubgroups {
			k := subgroupExpansionKey(catalog, s, sg)
			if _, seen := e.expanded[k]; !seen {
				e.expanded[k] = true
			}
		}
	}
	for _, t := range info.Tables {
		seedSchema(t.Schema)
	}
	for _, r := range info.Routines {
		seedSchema(r.Schema)
	}
	for _, tr := range info.Triggers {
		seedSchema(tr.Schema)
	}
	if depth == db.SchemaDepthFlat {
		for _, sg := range defaultExpandedSubgroups {
			k := subgroupExpansionKey(catalog, "", sg)
			if _, seen := e.expanded[k]; !seen {
				e.expanded[k] = true
			}
		}
	}
	// Sys pseudo-schema and all its subgroups start collapsed.
	sysKey := schemaExpansionKey(catalog, sysSchemaSentinel)
	if _, seen := e.expanded[sysKey]; !seen {
		e.expanded[sysKey] = false
	}
}

// Expansion-key helpers. All three share the `expanded` map. Keys are
// namespaced by a leading sentinel + optional catalog so DB-tier mode
// can carry independent schema/subgroup state per database without
// colliding with the legacy single-DB layout.
//
//	db key:       "\x02" + catalog
//	schema key:   "\x03" + catalog + "\x01" + schema
//	subgroup key: "\x04" + catalog + "\x01" + schema + "\x00" + sg.label()
//
// In single-DB mode catalog is the empty string.
func dbExpansionKey(catalog string) string {
	return "\x02" + catalog
}

func schemaExpansionKey(catalog, schema string) string {
	return "\x03" + catalog + "\x01" + schema
}

func subgroupExpansionKey(catalog, schema string, sg subgroupKind) string {
	return "\x04" + catalog + "\x01" + schema + "\x00" + sg.label()
}

// SetLoading shows an animated placeholder while a background schema
// fetch is in flight. Called on the main goroutine before kicking off
// the fetch so the user has immediate feedback. The initial frame is
// the first braille dot; the spinner goroutine advances it via
// SetLoadingFrame.
func (e *explorer) SetLoading() {
	e.err = ""
	e.loading = spinnerFrames[0]
	e.info = nil
	e.items = nil
	e.cursor = 0
	e.scroll = 0
}

// SetLoadingFrame updates the spinner frame while a schema fetch is in
// flight. A no-op once loading has ended so late spinner ticks can't
// reintroduce the placeholder after SetSchema/SetError.
func (e *explorer) SetLoadingFrame(frame string) {
	if e.loading == "" {
		return
	}
	e.loading = frame
}

// SetError puts the explorer into an error state. The tree is cleared so
// stale data from a previous connection doesn't get mistaken for the new
// one.
func (e *explorer) SetError(msg string) {
	e.err = msg
	e.loading = ""
	e.info = nil
	e.items = nil
	e.cursor = 0
	e.scroll = 0
}

// SetDatabases switches the explorer into DB-tier mode and seeds the
// top-level list. Called once after ListDatabases returns. Preserves any
// already-loaded per-DB schemas (dbSchemas) so a refresh doesn't wipe
// expanded children; callers wanting a full reset should call SetSchema
// with nil first.
func (e *explorer) SetDatabases(names []string) {
	e.dbMode = true
	e.databases = append(e.databases[:0], names...)
	e.info = nil
	e.err = ""
	e.loading = ""
	e.cursor = 0
	e.scroll = 0
	e.rebuild()
}

// SetDatabaseSchema stores a loaded schema for one database and drops
// any loading/error marker for it. Seeds default expansion under the
// catalog so the user lands on open Tables/Views like single-DB mode.
func (e *explorer) SetDatabaseSchema(catalog string, info *db.SchemaInfo) {
	if info != nil {
		e.seedExpansion(catalog, info, e.depth)
	}
	e.dbSchemas[catalog] = info
	delete(e.dbLoading, catalog)
	delete(e.dbErr, catalog)
	e.rebuild()
}

// SetDatabaseError records a per-DB load failure. Clears any loading
// marker so the placeholder flips from spinner to error text.
func (e *explorer) SetDatabaseError(catalog, msg string) {
	e.dbErr[catalog] = msg
	delete(e.dbLoading, catalog)
	e.rebuild()
}

// SetDatabaseLoading marks one DB as in-flight with the given spinner
// frame. Passing an empty frame clears the marker without storing a
// schema (used if the caller aborts before a result lands).
func (e *explorer) SetDatabaseLoading(catalog, frame string) {
	if frame == "" {
		delete(e.dbLoading, catalog)
	} else {
		e.dbLoading[catalog] = frame
		delete(e.dbErr, catalog)
	}
	e.rebuild()
}

// SetDatabaseLoadingFrame advances the spinner for a DB already marked
// loading. No-op if the DB isn't currently loading so late ticks can't
// resurrect a cleared placeholder.
func (e *explorer) SetDatabaseLoadingFrame(catalog, frame string) {
	if _, ok := e.dbLoading[catalog]; !ok {
		return
	}
	e.dbLoading[catalog] = frame
}

// SelectedDatabase returns the catalog name under the cursor when it's
// on a DB row. ok==false otherwise. Used by the main layer to know
// which DB to fetch on expand.
func (e *explorer) SelectedDatabase() (string, bool) {
	if e.cursor < 0 || e.cursor >= len(e.items) {
		return "", false
	}
	it := e.items[e.cursor]
	if it.kind != itemDatabase {
		return "", false
	}
	return it.catalog, true
}

// CursorCatalog returns the owning database of the item under the cursor,
// or empty when the tree isn't in DB-tier mode or the row has no catalog
// (e.g. single-DB drivers). Used by the main layer to auto-pin a query
// tab's activeCatalog when the user opens a table/routine/trigger from a
// specific DB subtree.
func (e *explorer) CursorCatalog() string {
	if e.cursor < 0 || e.cursor >= len(e.items) {
		return ""
	}
	return e.items[e.cursor].catalog
}

// NeedsDatabaseLoad reports whether the cursor is on a DB row that is
// expanded (or about to be, after a toggle) but has no schema loaded
// and no in-flight fetch. The main layer calls this after Toggle to
// decide whether to kick off SchemaForDatabase.
func (e *explorer) NeedsDatabaseLoad() (string, bool) {
	if !e.dbMode {
		return "", false
	}
	cat, ok := e.SelectedDatabase()
	if !ok {
		return "", false
	}
	if !e.expanded[dbExpansionKey(cat)] {
		return "", false
	}
	if _, loading := e.dbLoading[cat]; loading {
		return "", false
	}
	if _, loaded := e.dbSchemas[cat]; loaded {
		return "", false
	}
	return cat, true
}

// Selected returns the currently highlighted item, if any. ok==false means
// the tree is empty, the cursor is on a schema header, or we're in an error
// state — all cases where "run a SELECT" makes no sense.
func (e *explorer) Selected() (db.TableRef, bool) {
	if e.cursor < 0 || e.cursor >= len(e.items) {
		return db.TableRef{}, false
	}
	it := e.items[e.cursor]
	if it.kind != itemTable && it.kind != itemView {
		return db.TableRef{}, false
	}
	return it.table, true
}

// SelectedRoutine returns the currently highlighted routine (procedure or
// function). ok==false unless the cursor is on a routine leaf.
func (e *explorer) SelectedRoutine() (db.RoutineRef, bool) {
	if e.cursor < 0 || e.cursor >= len(e.items) {
		return db.RoutineRef{}, false
	}
	it := e.items[e.cursor]
	if it.kind != itemProcedure && it.kind != itemFunction {
		return db.RoutineRef{}, false
	}
	return it.routine, true
}

// SelectedTrigger returns the currently highlighted trigger. ok==false
// unless the cursor is on a trigger leaf.
func (e *explorer) SelectedTrigger() (db.TriggerRef, bool) {
	if e.cursor < 0 || e.cursor >= len(e.items) {
		return db.TriggerRef{}, false
	}
	it := e.items[e.cursor]
	if it.kind != itemTrigger {
		return db.TriggerRef{}, false
	}
	return it.trigger, true
}

// SelectedKind returns the kind of the currently highlighted row (or -1 if
// nothing is selected). Used by the main layer to decide between "toggle
// schema" and "prefill SELECT".
func (e *explorer) SelectedKind() explorerItemKind {
	if e.cursor < 0 || e.cursor >= len(e.items) {
		return -1
	}
	return e.items[e.cursor].kind
}

// SelectedSchema returns the schema name under the cursor (either the schema
// header itself or the parent of a table row).
func (e *explorer) SelectedSchema() string {
	if e.cursor < 0 || e.cursor >= len(e.items) {
		return ""
	}
	return e.items[e.cursor].schemaName
}

// ItemAt maps a screen row (1-based) inside the drawn rect r to an item
// index, or -1 if the row is outside the visible list. Used by the
// mouse hit test in mainLayer.
func (e *explorer) ItemAt(r rect, screenRow int) int {
	innerRow := r.row + 1
	innerH := r.h - 2
	if screenRow < innerRow || screenRow >= innerRow+innerH {
		return -1
	}
	idx := e.scroll + (screenRow - innerRow)
	if idx < 0 || idx >= len(e.items) {
		return -1
	}
	return idx
}

// SetCursor positions the cursor on the given index (clamped to valid
// range). Used by the mouse click path so it doesn't have to reach into
// MoveCursor deltas.
func (e *explorer) SetCursor(i int) {
	if len(e.items) == 0 {
		e.cursor = 0
		return
	}
	if i < 0 {
		i = 0
	}
	if i >= len(e.items) {
		i = len(e.items) - 1
	}
	e.cursor = i
}

// MoveCursor shifts the cursor by delta, clamped to valid range.
func (e *explorer) MoveCursor(delta int) {
	if len(e.items) == 0 {
		return
	}
	e.cursor += delta
	if e.cursor < 0 {
		e.cursor = 0
	}
	if e.cursor >= len(e.items) {
		e.cursor = len(e.items) - 1
	}
}

// Toggle expands or collapses the group under the cursor. Works on
// databases, schemas, and Tables/Views subgroups; no-op on leaves.
func (e *explorer) Toggle() {
	if e.cursor < 0 || e.cursor >= len(e.items) {
		return
	}
	it := e.items[e.cursor]
	var key string
	switch it.kind {
	case itemDatabase:
		key = dbExpansionKey(it.catalog)
	case itemSchema:
		key = schemaExpansionKey(it.catalog, it.schemaName)
	case itemSubgroup:
		key = subgroupExpansionKey(it.catalog, it.schemaName, it.subgroup)
	default:
		return
	}
	e.expanded[key] = !e.expanded[key]

	// Preserve the highlight across the rebuild.
	targetKind := it.kind
	targetCatalog := it.catalog
	targetSchema := it.schemaName
	targetSub := it.subgroup
	e.rebuild()
	for i, row := range e.items {
		if row.kind != targetKind || row.catalog != targetCatalog || row.schemaName != targetSchema {
			continue
		}
		if targetKind == itemSubgroup && row.subgroup != targetSub {
			continue
		}
		e.cursor = i
		return
	}
}

// rebuild flattens info+expanded into the visible items slice.
//
// Schemas mode (Postgres/MSSQL/MySQL) produces:
//
//	schema
//	  Tables
//	    tableA
//	  Views
//	    viewA
//
// Flat mode (SQLite) drops the schema header entirely, emitting
// Tables/Views subgroups at the root:
//
//	Tables
//	  tableA
//	Views
//	  viewA
//
// Subgroup headers are only emitted when their group has at least one entry.
func (e *explorer) rebuild() {
	e.items = nil
	if e.dbMode {
		for _, name := range e.databases {
			e.items = append(e.items, explorerItem{
				kind:    itemDatabase,
				label:   name,
				catalog: name,
			})
			if !e.expanded[dbExpansionKey(name)] {
				continue
			}
			if msg, ok := e.dbErr[name]; ok && msg != "" {
				e.items = append(e.items, explorerItem{
					kind:    itemSubgroup, // reuse for indent; treated as informational
					label:   "(error: " + msg + ")",
					catalog: name,
				})
				continue
			}
			if frame := e.dbLoading[name]; frame != "" {
				e.items = append(e.items, explorerItem{
					kind:    itemSubgroup,
					label:   frame + " loading…",
					catalog: name,
				})
				continue
			}
			info := e.dbSchemas[name]
			if info == nil {
				continue
			}
			e.emitSchemaTier(name, info, e.depth)
		}
	} else if e.info != nil {
		e.emitSchemaTier("", e.info, e.depth)
	}

	if e.cursor >= len(e.items) {
		e.cursor = len(e.items) - 1
	}
	if e.cursor < 0 {
		e.cursor = 0
	}
}

// emitSchemaTier flattens info into schema/subgroup/leaf rows under
// catalog. Shared between single-DB rebuild and per-DB expansion in
// dbMode. Leaves carry catalog on the row for display; BuildSelect
// strips it from the scaffold so tabs can retarget via activeCatalog.
func (e *explorer) emitSchemaTier(catalog string, info *db.SchemaInfo, depth db.SchemaDepth) {
	type schemaBucket struct {
		tables     []db.TableRef
		views      []db.TableRef
		procedures []db.RoutineRef
		functions  []db.RoutineRef
		triggers   []db.TriggerRef
	}
	buckets := map[string]*schemaBucket{}
	var schemas []string
	sysBucket := &schemaBucket{}
	touch := func(s string, system bool) *schemaBucket {
		if system {
			return sysBucket
		}
		b := buckets[s]
		if b == nil {
			b = &schemaBucket{}
			buckets[s] = b
			schemas = append(schemas, s)
		}
		return b
	}

	for _, t := range info.Tables {
		if catalog != "" {
			t.Catalog = catalog
		}
		target := touch(t.Schema, t.System)
		if t.Kind == db.TableKindView {
			target.views = append(target.views, t)
		} else {
			target.tables = append(target.tables, t)
		}
	}
	for _, r := range info.Routines {
		target := touch(r.Schema, r.System)
		if r.Kind == db.RoutineKindProcedure {
			target.procedures = append(target.procedures, r)
		} else {
			target.functions = append(target.functions, r)
		}
	}
	for _, tr := range info.Triggers {
		target := touch(tr.Schema, tr.System)
		target.triggers = append(target.triggers, tr)
	}
	sort.Strings(schemas)

	emit := func(schema string, b *schemaBucket) {
		e.appendTableSubgroup(catalog, schema, subgroupTables, b.tables)
		e.appendTableSubgroup(catalog, schema, subgroupViews, b.views)
		e.appendRoutineSubgroup(catalog, schema, subgroupProcedures, b.procedures)
		e.appendRoutineSubgroup(catalog, schema, subgroupFunctions, b.functions)
		e.appendTriggerSubgroup(catalog, schema, b.triggers)
	}

	if depth == db.SchemaDepthFlat {
		merged := &schemaBucket{}
		for _, s := range schemas {
			b := buckets[s]
			merged.tables = append(merged.tables, b.tables...)
			merged.views = append(merged.views, b.views...)
			merged.procedures = append(merged.procedures, b.procedures...)
			merged.functions = append(merged.functions, b.functions...)
			merged.triggers = append(merged.triggers, b.triggers...)
		}
		emit("", merged)
	} else {
		for _, s := range schemas {
			e.items = append(e.items, explorerItem{
				kind:       itemSchema,
				label:      s,
				catalog:    catalog,
				schemaName: s,
			})
			if !e.expanded[schemaExpansionKey(catalog, s)] {
				continue
			}
			emit(s, buckets[s])
		}
	}

	sysNonEmpty := len(sysBucket.tables)+len(sysBucket.views)+
		len(sysBucket.procedures)+len(sysBucket.functions)+
		len(sysBucket.triggers) > 0
	if sysNonEmpty {
		e.items = append(e.items, explorerItem{
			kind:       itemSchema,
			label:      "Sys",
			catalog:    catalog,
			schemaName: sysSchemaSentinel,
		})
		if e.expanded[schemaExpansionKey(catalog, sysSchemaSentinel)] {
			emit(sysSchemaSentinel, sysBucket)
		}
	}
}

func (e *explorer) appendTableSubgroup(catalog, schema string, sg subgroupKind, entries []db.TableRef) {
	if len(entries) == 0 {
		return
	}
	e.items = append(e.items, explorerItem{
		kind:       itemSubgroup,
		label:      sg.label(),
		catalog:    catalog,
		schemaName: schema,
		subgroup:   sg,
	})
	if !e.expanded[subgroupExpansionKey(catalog, schema, sg)] {
		return
	}
	for _, t := range entries {
		leafKind := itemTable
		if t.Kind == db.TableKindView {
			leafKind = itemView
		}
		e.items = append(e.items, explorerItem{
			kind:       leafKind,
			label:      t.Name,
			catalog:    catalog,
			schemaName: schema,
			subgroup:   sg,
			table:      t,
		})
	}
}

func (e *explorer) appendRoutineSubgroup(catalog, schema string, sg subgroupKind, entries []db.RoutineRef) {
	if len(entries) == 0 {
		return
	}
	e.items = append(e.items, explorerItem{
		kind:       itemSubgroup,
		label:      sg.label(),
		catalog:    catalog,
		schemaName: schema,
		subgroup:   sg,
	})
	if !e.expanded[subgroupExpansionKey(catalog, schema, sg)] {
		return
	}
	leafKind := itemFunction
	if sg == subgroupProcedures {
		leafKind = itemProcedure
	}
	for _, r := range entries {
		suffix := ""
		if r.Language != "" && r.Language != "SQL" {
			suffix = "(" + r.Language + ")"
		}
		e.items = append(e.items, explorerItem{
			kind:       leafKind,
			label:      r.Name,
			catalog:    catalog,
			schemaName: schema,
			subgroup:   sg,
			routine:    r,
			suffix:     suffix,
		})
	}
}

func (e *explorer) appendTriggerSubgroup(catalog, schema string, entries []db.TriggerRef) {
	if len(entries) == 0 {
		return
	}
	e.items = append(e.items, explorerItem{
		kind:       itemSubgroup,
		label:      subgroupTriggers.label(),
		catalog:    catalog,
		schemaName: schema,
		subgroup:   subgroupTriggers,
	})
	if !e.expanded[subgroupExpansionKey(catalog, schema, subgroupTriggers)] {
		return
	}
	for _, tr := range entries {
		suffix := ""
		if tr.Timing != "" || tr.Event != "" || tr.Table != "" {
			suffix = "(" + trimSpace(tr.Timing+" "+tr.Event) + " on " + tr.Table + ")"
		}
		e.items = append(e.items, explorerItem{
			kind:       itemTrigger,
			label:      tr.Name,
			catalog:    catalog,
			schemaName: schema,
			subgroup:   subgroupTriggers,
			trigger:    tr,
			suffix:     suffix,
		})
	}
}

// trimSpace collapses consecutive spaces and trims ends without pulling in strings.
func trimSpace(s string) string {
	out := make([]byte, 0, len(s))
	prevSpace := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' {
			if prevSpace {
				continue
			}
			prevSpace = true
			out = append(out, c)
			continue
		}
		prevSpace = false
		out = append(out, c)
	}
	if n := len(out); n > 0 && out[n-1] == ' ' {
		out = out[:n-1]
	}
	return string(out)
}

// draw renders the tree inside r (caller has already drawn the border).
func (e *explorer) draw(c *cellbuf, r rect, focused bool) {
	innerRow := r.row + 1
	innerCol := r.col + 1
	innerW := r.w - 2
	innerH := r.h - 2
	if innerW <= 0 || innerH <= 0 {
		return
	}

	if e.loading != "" {
		c.writeAt(innerRow, innerCol, truncate(e.loading+" loading schema…", innerW))
		return
	}
	if e.err != "" {
		c.writeAt(innerRow, innerCol, truncate("error: "+e.err, innerW))
		return
	}
	if len(e.items) == 0 {
		msg := "(no schema loaded)"
		if e.info != nil {
			msg = "(empty)"
		}
		c.writeAt(innerRow, innerCol, truncate(msg, innerW))
		return
	}

	// Keep the cursor visible by clamping scroll to [cursor-innerH+1 .. cursor].
	if e.cursor < e.scroll {
		e.scroll = e.cursor
	}
	if e.cursor >= e.scroll+innerH {
		e.scroll = e.cursor - innerH + 1
	}
	if e.scroll < 0 {
		e.scroll = 0
	}

	for i := 0; i < innerH; i++ {
		idx := e.scroll + i
		if idx >= len(e.items) {
			break
		}
		line := renderExplorerLine(e.items[idx], e.expanded)
		selected := idx == e.cursor && focused
		if selected {
			c.setFg(colorTitleFocused)
		}
		c.writeAt(innerRow+i, innerCol, truncate(line, innerW))
		if selected {
			c.resetStyle()
		}
	}
}

// renderExplorerLine formats one visible row with indent + marker.
// In Schemas mode indent is schemas=0, subgroups=2, leaves=4. In Flat
// mode (SQLite) the schema layer is skipped so subgroups sit at 0 and
// leaves at 2. The Flat case is distinguished by an empty schemaName
// on the subgroup / leaf (populated by rebuild()).
func renderExplorerLine(it explorerItem, expanded map[string]bool) string {
	dbIndent := ""
	if it.catalog != "" && it.kind != itemDatabase {
		dbIndent = "  "
	}
	switch it.kind {
	case itemDatabase:
		marker := "▸"
		if expanded[dbExpansionKey(it.catalog)] {
			marker = "▾"
		}
		return marker + " " + it.label
	case itemSchema:
		marker := "▸"
		if expanded[schemaExpansionKey(it.catalog, it.schemaName)] {
			marker = "▾"
		}
		return dbIndent + marker + " " + it.label
	case itemSubgroup:
		// subgroup rows with a zero subgroup value are the informational
		// loading/error placeholders emitted under a DB row — render
		// them as plain indented text with no marker.
		if it.subgroup == subgroupNone {
			return dbIndent + "  " + it.label
		}
		marker := "▸"
		if expanded[subgroupExpansionKey(it.catalog, it.schemaName, it.subgroup)] {
			marker = "▾"
		}
		if it.schemaName == "" {
			return dbIndent + marker + " " + it.label
		}
		return dbIndent + "  " + marker + " " + it.label
	case itemTable, itemView, itemProcedure, itemFunction, itemTrigger:
		leaf := "· "
		switch it.kind {
		case itemView:
			leaf = "◇ "
		case itemProcedure:
			leaf = "λ "
		case itemFunction:
			leaf = "ƒ "
		case itemTrigger:
			leaf = "! "
		}
		body := leaf + it.label
		if it.suffix != "" {
			body += " " + it.suffix
		}
		if it.schemaName == "" {
			return dbIndent + "    " + body
		}
		return dbIndent + "      " + body
	}
	return it.label
}

// QualifiedName returns "schema.name" with driver-appropriate quoting for a
// SELECT. The quote character comes from the driver's Capabilities, so adding
// a new engine is just a matter of setting IdentifierQuote on its
// capability struct — no string-switch on Name() here.
func QualifiedName(caps db.Capabilities, t db.TableRef) string {
	open, close := quoteChars(caps.IdentifierQuote)
	q := func(s string) string {
		// Double any embedded close char so an identifier containing ']'
		// (MSSQL), '`' (MySQL) or '"' (ANSI) round-trips correctly
		// instead of prematurely terminating the quoted form.
		if strings.Contains(s, close) {
			s = strings.ReplaceAll(s, close, close+close)
		}
		return open + s + close
	}
	parts := ""
	if t.Catalog != "" {
		parts = q(t.Catalog) + "."
	}
	if t.Schema == "" {
		return parts + q(t.Name)
	}
	return parts + q(t.Schema) + "." + q(t.Name)
}

// quoteChars returns the opening and closing identifier quote characters
// for a given opening quote. '[' pairs with ']'; everything else pairs
// with itself (backtick, double-quote).
func quoteChars(open rune) (string, string) {
	switch open {
	case '[':
		return "[", "]"
	case 0:
		// Default to ANSI double quotes for drivers that don't set one.
		return `"`, `"`
	default:
		s := string(open)
		return s, s
	}
}

// BuildSelect produces a capability-appropriate "first N rows" SELECT for
// the given table. The limit form (TOP vs LIMIT) and identifier quoting
// both come from caps.
func BuildSelect(caps db.Capabilities, t db.TableRef, limit int) string {
	// Strip catalog so scaffold reruns cleanly under whatever DB the
	// tab's activeCatalog routes to via USE-prepend. Three-part names
	// would pin the query to the source DB.
	t.Catalog = ""
	name := QualifiedName(caps, t)
	switch caps.LimitSyntax {
	case db.LimitSyntaxSelectTop:
		return "SELECT TOP " + itoa(limit) + " * FROM " + name + ";"
	case db.LimitSyntaxFetchFirst:
		return "SELECT * FROM " + name + " OFFSET 0 ROWS FETCH NEXT " + itoa(limit) + " ROWS ONLY;"
	default:
		return "SELECT * FROM " + name + " LIMIT " + itoa(limit) + ";"
	}
}

// itoa avoids pulling in strconv for one call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
