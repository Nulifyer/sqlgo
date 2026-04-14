// Package fileimport streams CSV, TSV, and JSONL files into a
// *sql.DB as tables. Intended to back the "file" driver which
// connects to an in-memory SQLite and loads one table per file so
// the user can query flat data with SQL.
//
// Format is dispatched by extension (.csv, .tsv, .jsonl, .ndjson).
// Column types are inferred in a single pass by promoting along
// INTEGER -> REAL -> TEXT: the first non-integer value demotes the
// column to REAL, and the first non-numeric value demotes it to
// TEXT. Empty strings are treated as NULL and don't affect typing.
package fileimport

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// ColumnType is the inferred SQLite storage class for a column.
type ColumnType int

const (
	TypeInteger ColumnType = iota
	TypeReal
	TypeText
)

func (t ColumnType) sqlName() string {
	switch t {
	case TypeInteger:
		return "INTEGER"
	case TypeReal:
		return "REAL"
	default:
		return "TEXT"
	}
}

// Load streams path into sqlDB as a new table. Returns the table
// name (derived from the filename). Format comes from the extension.
func Load(ctx context.Context, sqlDB *sql.DB, path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	table := TableName(path)
	switch ext {
	case ".csv":
		return table, loadDelimited(ctx, sqlDB, path, table, ',')
	case ".tsv":
		return table, loadDelimited(ctx, sqlDB, path, table, '\t')
	case ".jsonl", ".ndjson":
		return table, loadJSONL(ctx, sqlDB, path, table)
	default:
		return "", fmt.Errorf("fileimport: unsupported extension %q", ext)
	}
}

// TableName sanitizes a filename into a SQL identifier.
func TableName(path string) string {
	base := filepath.Base(path)
	if ext := filepath.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	var b strings.Builder
	for _, r := range base {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	name := b.String()
	if name == "" || unicode.IsDigit(rune(name[0])) {
		name = "t_" + name
	}
	return name
}

// loadDelimited handles CSV/TSV. The first record is treated as
// header. Column types are inferred from all data rows.
func loadDelimited(ctx context.Context, sqlDB *sql.DB, path, table string, delim rune) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	r := csv.NewReader(bufio.NewReader(f))
	r.Comma = delim
	r.FieldsPerRecord = -1
	r.LazyQuotes = true

	header, err := r.Read()
	if err != nil {
		return fmt.Errorf("read header: %w", err)
	}
	cols := sanitizeColumns(header)

	types := make([]ColumnType, len(cols))
	for i := range types {
		types[i] = TypeInteger
	}
	var rows [][]string
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		if len(rec) < len(cols) {
			pad := make([]string, len(cols))
			copy(pad, rec)
			rec = pad
		}
		for i := 0; i < len(cols); i++ {
			types[i] = promote(types[i], rec[i])
		}
		rows = append(rows, rec)
	}
	return writeTable(ctx, sqlDB, table, cols, types, delimitedRows(rows))
}

// delimitedRows adapts [][]string to the rowIter shape used by writeTable.
func delimitedRows(rows [][]string) rowIter {
	i := 0
	return func() ([]any, bool) {
		if i >= len(rows) {
			return nil, false
		}
		r := rows[i]
		i++
		out := make([]any, len(r))
		for j, v := range r {
			out[j] = v
		}
		return out, true
	}
}

// loadJSONL handles JSONL/NDJSON. One object per line. Column set
// is the union of top-level keys across all lines. Type inference
// walks each value; mixed-type columns demote to TEXT.
func loadJSONL(ctx context.Context, sqlDB *sql.DB, path, table string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)

	var rows []map[string]any
	colSet := map[string]struct{}{}
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			return fmt.Errorf("parse line: %w", err)
		}
		rows = append(rows, obj)
		for k := range obj {
			colSet[k] = struct{}{}
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	cols := make([]string, 0, len(colSet))
	for k := range colSet {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	sanitized := sanitizeColumns(cols)

	types := make([]ColumnType, len(cols))
	for i := range types {
		types[i] = TypeInteger
	}
	for _, obj := range rows {
		for i, k := range cols {
			v, ok := obj[k]
			if !ok || v == nil {
				continue
			}
			types[i] = promoteValue(types[i], v)
		}
	}

	return writeTable(ctx, sqlDB, table, sanitized, types, jsonlRows(cols, rows))
}

// jsonlRows adapts parsed JSONL records to rowIter, pulling each
// column by the original (un-sanitized) key.
func jsonlRows(cols []string, rows []map[string]any) rowIter {
	i := 0
	return func() ([]any, bool) {
		if i >= len(rows) {
			return nil, false
		}
		r := rows[i]
		i++
		out := make([]any, len(cols))
		for j, k := range cols {
			v, ok := r[k]
			if !ok || v == nil {
				out[j] = nil
				continue
			}
			switch vv := v.(type) {
			case string, float64, bool:
				out[j] = vv
			default:
				// objects, arrays: re-encode as JSON text.
				b, _ := json.Marshal(vv)
				out[j] = string(b)
			}
		}
		return out, true
	}
}

// rowIter yields one row per call. Returns ok=false when exhausted.
type rowIter func() ([]any, bool)

// writeTable creates the target table and inserts rows in batches.
// Uses a single transaction for atomicity and speed.
func writeTable(ctx context.Context, sqlDB *sql.DB, table string, cols []string, types []ColumnType, next rowIter) error {
	if len(cols) == 0 {
		return fmt.Errorf("no columns")
	}
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var create strings.Builder
	create.WriteString(`CREATE TABLE "`)
	create.WriteString(strings.ReplaceAll(table, `"`, `""`))
	create.WriteString(`" (`)
	for i, c := range cols {
		if i > 0 {
			create.WriteString(", ")
		}
		fmt.Fprintf(&create, `"%s" %s`, strings.ReplaceAll(c, `"`, `""`), types[i].sqlName())
	}
	create.WriteString(")")
	if _, err := tx.ExecContext(ctx, create.String()); err != nil {
		return fmt.Errorf("create table: %w", err)
	}

	var ins strings.Builder
	ins.WriteString(`INSERT INTO "`)
	ins.WriteString(strings.ReplaceAll(table, `"`, `""`))
	ins.WriteString(`" VALUES (`)
	for i := range cols {
		if i > 0 {
			ins.WriteString(", ")
		}
		ins.WriteString("?")
	}
	ins.WriteString(")")
	stmt, err := tx.PrepareContext(ctx, ins.String())
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	for {
		row, ok := next()
		if !ok {
			break
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		args := make([]any, len(cols))
		for i := 0; i < len(cols); i++ {
			if i < len(row) {
				args[i] = convertForType(types[i], row[i])
			}
		}
		if _, err := stmt.ExecContext(ctx, args...); err != nil {
			return fmt.Errorf("insert: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// promote refines t based on a string cell. Empty stays.
func promote(t ColumnType, s string) ColumnType {
	if s == "" {
		return t
	}
	switch t {
	case TypeInteger:
		if _, err := strconv.ParseInt(s, 10, 64); err == nil {
			return TypeInteger
		}
		if _, err := strconv.ParseFloat(s, 64); err == nil {
			return TypeReal
		}
		return TypeText
	case TypeReal:
		if _, err := strconv.ParseFloat(s, 64); err == nil {
			return TypeReal
		}
		return TypeText
	}
	return TypeText
}

// promoteValue refines t based on a JSON-decoded value.
func promoteValue(t ColumnType, v any) ColumnType {
	switch vv := v.(type) {
	case float64:
		// JSON numbers: demote to REAL unless the value is integral.
		if t == TypeInteger && vv == float64(int64(vv)) {
			return TypeInteger
		}
		if t == TypeText {
			return TypeText
		}
		return TypeReal
	case string:
		return TypeText
	case bool:
		return TypeText
	default:
		return TypeText
	}
}

// convertForType coerces a raw cell (string from CSV or JSON value)
// into the destination type. Errors fall back to the raw form, which
// sqlite will still store.
func convertForType(t ColumnType, v any) any {
	if v == nil {
		return nil
	}
	switch s := v.(type) {
	case string:
		if s == "" {
			return nil
		}
		switch t {
		case TypeInteger:
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				return n
			}
		case TypeReal:
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return f
			}
		}
		return s
	}
	// JSON path: preserve value; sqlite binds float64/bool natively.
	return v
}

// sanitizeColumns replaces characters outside [a-zA-Z0-9_] with _
// and deduplicates by appending _2, _3, ... as needed.
func sanitizeColumns(in []string) []string {
	out := make([]string, len(in))
	seen := map[string]int{}
	for i, s := range in {
		var b strings.Builder
		for _, r := range s {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
				b.WriteRune(r)
			} else {
				b.WriteByte('_')
			}
		}
		name := b.String()
		if name == "" {
			name = fmt.Sprintf("col_%d", i+1)
		}
		if n, dup := seen[name]; dup {
			seen[name] = n + 1
			name = fmt.Sprintf("%s_%d", name, n+1)
		}
		seen[name] = 1
		out[i] = name
	}
	return out
}
