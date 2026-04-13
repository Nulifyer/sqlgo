package tui

import (
	"sort"

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
	label      string      // display text WITHOUT the indent/marker
	schemaName string      // owning schema (set for all kinds)
	subgroup   subgroupKind // valid for itemSubgroup; also set on leaves so Toggle knows which group they belong to
	table      db.TableRef // valid only for itemTable / itemView
}

type explorerItemKind int

const (
	itemSchema explorerItemKind = iota
	itemSubgroup
	itemTable
	itemView
)

// subgroupKind distinguishes the two children a schema can have.
type subgroupKind int

const (
	subgroupNone subgroupKind = iota
	subgroupTables
	subgroupViews
)

func (s subgroupKind) label() string {
	switch s {
	case subgroupTables:
		return "Tables"
	case subgroupViews:
		return "Views"
	}
	return ""
}

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
}

func newExplorer() *explorer {
	return &explorer{expanded: map[string]bool{}}
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
	e.cursor = 0
	e.scroll = 0
	if info != nil {
		for _, t := range info.Tables {
			schemaKey := t.Schema
			if _, seen := e.expanded[schemaKey]; !seen {
				e.expanded[schemaKey] = true
			}
			for _, sg := range []subgroupKind{subgroupTables, subgroupViews} {
				k := subgroupExpansionKey(t.Schema, sg)
				if _, seen := e.expanded[k]; !seen {
					e.expanded[k] = true
				}
			}
		}
		// Flat mode uses a synthetic empty-string schema name for its
		// subgroups; seed those expansion keys too so the Tables/Views
		// groups start expanded instead of rendering collapsed.
		if depth == db.SchemaDepthFlat {
			for _, sg := range []subgroupKind{subgroupTables, subgroupViews} {
				k := subgroupExpansionKey("", sg)
				if _, seen := e.expanded[k]; !seen {
					e.expanded[k] = true
				}
			}
		}
	}
	e.rebuild()
}

// subgroupExpansionKey returns the map key used to track a subgroup's
// expanded state. \x00 is a byte that can't appear in identifiers.
func subgroupExpansionKey(schema string, sg subgroupKind) string {
	return schema + "\x00" + sg.label()
}

// SetError puts the explorer into an error state. The tree is cleared so
// stale data from a previous connection doesn't get mistaken for the new
// one.
func (e *explorer) SetError(msg string) {
	e.err = msg
	e.info = nil
	e.items = nil
	e.cursor = 0
	e.scroll = 0
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

// Toggle expands or collapses the group under the cursor. Works on schemas
// and on Tables/Views subgroups; no-op on leaves.
func (e *explorer) Toggle() {
	if e.cursor < 0 || e.cursor >= len(e.items) {
		return
	}
	it := e.items[e.cursor]
	var key string
	switch it.kind {
	case itemSchema:
		key = it.schemaName
	case itemSubgroup:
		key = subgroupExpansionKey(it.schemaName, it.subgroup)
	default:
		return
	}
	e.expanded[key] = !e.expanded[key]

	// Preserve the highlight across the rebuild.
	targetKind := it.kind
	targetSchema := it.schemaName
	targetSub := it.subgroup
	e.rebuild()
	for i, row := range e.items {
		if row.kind != targetKind || row.schemaName != targetSchema {
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
	if e.info == nil {
		return
	}
	// Group tables+views by schema (info.Tables is already sorted by
	// schema+name). Splitting into tables/views buckets is driven off
	// the TableKind so the source ordering doesn't have to care.
	type schemaBucket struct {
		tables []db.TableRef
		views  []db.TableRef
	}
	buckets := map[string]*schemaBucket{}
	var schemas []string
	for _, t := range e.info.Tables {
		b := buckets[t.Schema]
		if b == nil {
			b = &schemaBucket{}
			buckets[t.Schema] = b
			schemas = append(schemas, t.Schema)
		}
		if t.Kind == db.TableKindView {
			b.views = append(b.views, t)
		} else {
			b.tables = append(b.tables, t)
		}
	}
	sort.Strings(schemas)

	if e.depth == db.SchemaDepthFlat {
		// Merge every bucket into one synthetic "" schema so the Tables/
		// Views subgroups contain everything. sqlite normally reports
		// a single "main" schema, but we join across any rogue buckets
		// defensively.
		var allTables, allViews []db.TableRef
		for _, s := range schemas {
			allTables = append(allTables, buckets[s].tables...)
			allViews = append(allViews, buckets[s].views...)
		}
		e.appendSubgroup("", subgroupTables, allTables)
		e.appendSubgroup("", subgroupViews, allViews)
	} else {
		for _, s := range schemas {
			e.items = append(e.items, explorerItem{
				kind:       itemSchema,
				label:      s,
				schemaName: s,
			})
			if !e.expanded[s] {
				continue
			}
			b := buckets[s]
			e.appendSubgroup(s, subgroupTables, b.tables)
			e.appendSubgroup(s, subgroupViews, b.views)
		}
	}

	if e.cursor >= len(e.items) {
		e.cursor = len(e.items) - 1
	}
	if e.cursor < 0 {
		e.cursor = 0
	}
}

// appendSubgroup emits the "Tables"/"Views" header and, if expanded, its
// entries. Called from rebuild under a schema that's already expanded.
func (e *explorer) appendSubgroup(schema string, sg subgroupKind, entries []db.TableRef) {
	if len(entries) == 0 {
		return
	}
	e.items = append(e.items, explorerItem{
		kind:       itemSubgroup,
		label:      sg.label(),
		schemaName: schema,
		subgroup:   sg,
	})
	if !e.expanded[subgroupExpansionKey(schema, sg)] {
		return
	}
	leafKind := itemTable
	if sg == subgroupViews {
		leafKind = itemView
	}
	for _, t := range entries {
		e.items = append(e.items, explorerItem{
			kind:       leafKind,
			label:      t.Name,
			schemaName: schema,
			subgroup:   sg,
			table:      t,
		})
	}
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
	switch it.kind {
	case itemSchema:
		marker := "▸"
		if expanded[it.schemaName] {
			marker = "▾"
		}
		return marker + " " + it.label
	case itemSubgroup:
		marker := "▸"
		if expanded[subgroupExpansionKey(it.schemaName, it.subgroup)] {
			marker = "▾"
		}
		if it.schemaName == "" {
			return marker + " " + it.label
		}
		return "  " + marker + " " + it.label
	case itemTable, itemView:
		leaf := "· "
		if it.kind == itemView {
			leaf = "◇ "
		}
		if it.schemaName == "" {
			return "    " + leaf + it.label
		}
		return "      " + leaf + it.label
	}
	return it.label
}

// QualifiedName returns "schema.name" with driver-appropriate quoting for a
// SELECT. The quote character comes from the driver's Capabilities, so adding
// a new engine is just a matter of setting IdentifierQuote on its
// capability struct — no string-switch on Name() here.
func QualifiedName(caps db.Capabilities, t db.TableRef) string {
	open, close := quoteChars(caps.IdentifierQuote)
	if t.Schema == "" {
		return open + t.Name + close
	}
	return open + t.Schema + close + "." + open + t.Name + close
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
	name := QualifiedName(caps, t)
	if caps.LimitSyntax == db.LimitSyntaxSelectTop {
		return "SELECT TOP " + itoa(limit) + " * FROM " + name + ";"
	}
	return "SELECT * FROM " + name + " LIMIT " + itoa(limit) + ";"
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

