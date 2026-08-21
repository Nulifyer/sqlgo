// Package fileimport streams CSV, TSV, and JSONL files into a
// *sql.DB as tables. Intended to back the "file" driver which
// connects to an in-memory SQLite and loads one table per file so
// the user can query flat data with SQL.
//
// Format is dispatched by extension (.csv, .tsv, .jsonl, .ndjson).
// The first pass infers columns and types by promoting along INTEGER -> REAL
// -> TEXT. The second pass streams rows into one transaction, so memory use
// depends on column count rather than file size. Empty strings are treated as
// NULL and do not affect typing.
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
// header. Column types are inferred from all data rows before a second pass
// streams the records into SQLite.
func loadDelimited(ctx context.Context, sqlDB *sql.DB, path, table string, delim rune) error {
	header, types, err := inferDelimited(ctx, path, delim)
	if err != nil {
		return err
	}
	cols := sanitizeColumns(header)

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()
	r := csv.NewReader(bufio.NewReader(f))
	r.Comma = delim
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	if _, err := r.Read(); err != nil {
		return fmt.Errorf("read header: %w", err)
	}
	return writeTable(ctx, sqlDB, table, cols, types, func() ([]any, bool, error) {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		rec, err := r.Read()
		if err == io.EOF {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, fmt.Errorf("read: %w", err)
		}
		out := make([]any, len(cols))
		for i := range cols {
			if i < len(rec) {
				out[i] = rec[i]
			}
		}
		return out, true, nil
	})
}

func inferDelimited(ctx context.Context, path string, delim rune) ([]string, []ColumnType, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open: %w", err)
	}
	defer f.Close()
	r := csv.NewReader(bufio.NewReader(f))
	r.Comma = delim
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	header, err := r.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("read header: %w", err)
	}
	types := make([]ColumnType, len(header))
	for i := range types {
		types[i] = TypeInteger
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read: %w", err)
		}
		for i := range types {
			if i < len(rec) {
				types[i] = promote(types[i], rec[i])
			}
		}
	}
	return header, types, nil
}

// loadJSONL handles JSONL/NDJSON. One object per line. Column set
// is the union of top-level keys across all lines. Type inference
// walks each value; mixed-type columns demote to TEXT.
func loadJSONL(ctx context.Context, sqlDB *sql.DB, path, table string) error {
	cols, types, err := inferJSONL(ctx, path)
	if err != nil {
		return err
	}
	sanitized := sanitizeColumns(cols)

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	return writeTable(ctx, sqlDB, table, sanitized, types, func() ([]any, bool, error) {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var obj map[string]any
			if err := json.Unmarshal([]byte(line), &obj); err != nil {
				return nil, false, fmt.Errorf("parse line: %w", err)
			}
			return jsonObjectRow(cols, obj), true, nil
		}
		if err := sc.Err(); err != nil {
			return nil, false, fmt.Errorf("scan: %w", err)
		}
		return nil, false, nil
	})
}

func inferJSONL(ctx context.Context, path string) ([]string, []ColumnType, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	typeByColumn := map[string]ColumnType{}
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			return nil, nil, fmt.Errorf("parse line: %w", err)
		}
		for key, value := range obj {
			current, exists := typeByColumn[key]
			if !exists {
				current = TypeInteger
			}
			if value != nil {
				current = promoteValue(current, value)
			}
			typeByColumn[key] = current
		}
	}
	if err := sc.Err(); err != nil {
		return nil, nil, fmt.Errorf("scan: %w", err)
	}
	cols := make([]string, 0, len(typeByColumn))
	for key := range typeByColumn {
		cols = append(cols, key)
	}
	sort.Strings(cols)
	types := make([]ColumnType, len(cols))
	for i, key := range cols {
		types[i] = typeByColumn[key]
	}
	return cols, types, nil
}

func jsonObjectRow(cols []string, obj map[string]any) []any {
	out := make([]any, len(cols))
	for i, key := range cols {
		value, ok := obj[key]
		if !ok || value == nil {
			continue
		}
		switch value := value.(type) {
		case string, float64, bool:
			out[i] = value
		default:
			encoded, _ := json.Marshal(value)
			out[i] = string(encoded)
		}
	}
	return out
}

// rowIter yields one row per call. Returns ok=false when exhausted and an
// error when the source fails after table creation has begun.
type rowIter func() ([]any, bool, error)

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
		row, ok, err := next()
		if err != nil {
			return err
		}
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
