package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// HistoryEntry is one recorded query execution. Fields mirror the columns
// on the history table.
type HistoryEntry struct {
	ID             int64
	ConnectionName string
	SQL            string
	ExecutedAt     time.Time
	Elapsed        time.Duration
	RowCount       int64
	Error          string // empty on success
}

// DeleteHistory removes a single history row by id. Used by the
// history browser's 'd' key binding. Returns ErrConnectionNotFound
// style semantics via rowsAffected -- a missing id is an error so
// the caller can surface "already gone" feedback.
func (s *Store) DeleteHistory(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM history WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete history: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete history rows: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("history id %d not found", id)
	}
	return nil
}

// ClearHistory removes every history row. When connectionName is
// non-empty, only rows for that connection are wiped; pass "" to
// nuke everything. Returns the number of rows deleted so the UI can
// report "cleared N entries".
func (s *Store) ClearHistory(ctx context.Context, connectionName string) (int64, error) {
	var (
		res interface {
			RowsAffected() (int64, error)
		}
		err error
	)
	if connectionName == "" {
		r, e := s.db.ExecContext(ctx, `DELETE FROM history`)
		res, err = r, e
	} else {
		r, e := s.db.ExecContext(ctx, `DELETE FROM history WHERE connection_name = ?`, connectionName)
		res, err = r, e
	}
	if err != nil {
		return 0, fmt.Errorf("clear history: %w", err)
	}
	return res.RowsAffected()
}

// RecordHistory inserts a history row and lazily trims the per-connection
// ring to the current cap. The trim runs in the same transaction as the
// insert so observers never see a stale above-cap view.
func (s *Store) RecordHistory(ctx context.Context, e HistoryEntry) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("record history: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	executedAt := e.ExecutedAt
	if executedAt.IsZero() {
		executedAt = time.Now().UTC()
	}
	var errText any
	if e.Error != "" {
		errText = e.Error
	}
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO history(connection_name, sql, executed_at, elapsed_ms, row_count, error)
        VALUES (?, ?, ?, ?, ?, ?)`,
		e.ConnectionName,
		e.SQL,
		executedAt.UTC().Format("2006-01-02 15:04:05.000"),
		e.Elapsed.Milliseconds(),
		e.RowCount,
		errText,
	); err != nil {
		return fmt.Errorf("record history insert: %w", err)
	}

	// Ring trim: delete any rows for this connection that fall outside
	// the newest N by executed_at (ties broken by id desc). Done in SQL
	// so we don't fetch keys just to delete them.
	if _, err := tx.ExecContext(ctx, `
        DELETE FROM history
        WHERE connection_name = ?
          AND id NOT IN (
              SELECT id FROM history
              WHERE connection_name = ?
              ORDER BY executed_at DESC, id DESC
              LIMIT ?
          )`,
		e.ConnectionName, e.ConnectionName, s.historyRingMax,
	); err != nil {
		return fmt.Errorf("record history trim: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("record history commit: %w", err)
	}
	return nil
}

// ListRecentHistory returns up to limit most-recent history entries for
// the given connection. A zero or negative limit is treated as 50. Pass
// an empty connectionName to list across every connection.
func (s *Store) ListRecentHistory(ctx context.Context, connectionName string, limit int) ([]HistoryEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	var (
		rows *sql.Rows
		err  error
	)
	if connectionName == "" {
		rows, err = s.db.QueryContext(ctx, `
            SELECT id, connection_name, sql, executed_at, elapsed_ms, row_count, COALESCE(error, '')
            FROM history
            ORDER BY executed_at DESC, id DESC
            LIMIT ?`, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `
            SELECT id, connection_name, sql, executed_at, elapsed_ms, row_count, COALESCE(error, '')
            FROM history
            WHERE connection_name = ?
            ORDER BY executed_at DESC, id DESC
            LIMIT ?`, connectionName, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("list history: %w", err)
	}
	defer rows.Close()
	return scanHistory(rows)
}

// SearchHistory runs a full-text search against the history's sql column
// using the FTS5 index. If the query string is empty, falls back to
// ListRecentHistory. Pass an empty connectionName to search across every
// connection.
//
// Explicit phrase / boolean queries are passed through verbatim. Plain-text
// input is tokenized conservatively and rewritten as a prefix query so
// punctuation-heavy strings like "foo-bar" and "a@b.c" don't become raw
// FTS syntax errors.
func (s *Store) SearchHistory(ctx context.Context, connectionName, q string, limit int) ([]HistoryEntry, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return s.ListRecentHistory(ctx, connectionName, limit)
	}
	if limit <= 0 {
		limit = 50
	}
	fts := expandFTSQuery(q)
	if fts == "" {
		return []HistoryEntry{}, nil
	}

	var (
		rows *sql.Rows
		err  error
	)
	if connectionName == "" {
		rows, err = s.db.QueryContext(ctx, `
            SELECT h.id, h.connection_name, h.sql, h.executed_at, h.elapsed_ms, h.row_count, COALESCE(h.error, '')
            FROM history h
            JOIN history_fts f ON f.rowid = h.id
            WHERE history_fts MATCH ?
            ORDER BY h.executed_at DESC, h.id DESC
            LIMIT ?`, fts, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `
            SELECT h.id, h.connection_name, h.sql, h.executed_at, h.elapsed_ms, h.row_count, COALESCE(h.error, '')
            FROM history h
            JOIN history_fts f ON f.rowid = h.id
            WHERE h.connection_name = ?
              AND history_fts MATCH ?
            ORDER BY h.executed_at DESC, h.id DESC
            LIMIT ?`, connectionName, fts, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("search history: %w", err)
	}
	defer rows.Close()
	return scanHistory(rows)
}

// expandFTSQuery turns plain user text into a safe FTS5 expression. Raw
// pass-through is reserved for clearly advanced input: explicit phrases or
// standalone boolean operators. Everything else is tokenized as plain text
// and rewritten into prefix matches so common punctuation never trips the
// SQLite parser.
func expandFTSQuery(q string) string {
	if strings.ContainsRune(q, '"') || containsFTSOp(q) {
		return q
	}
	fields := tokenizePlainFTSQuery(q)
	if len(fields) == 0 {
		return ""
	}
	for i, f := range fields {
		fields[i] = f + "*"
	}
	return strings.Join(fields, " ")
}

func containsFTSOp(q string) bool {
	for _, field := range strings.Fields(q) {
		switch strings.ToUpper(field) {
		case "AND", "OR", "NOT":
			return true
		}
	}
	return false
}

func tokenizePlainFTSQuery(q string) []string {
	return strings.FieldsFunc(q, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
}

func scanHistory(rows *sql.Rows) ([]HistoryEntry, error) {
	var out []HistoryEntry
	for rows.Next() {
		var (
			e          HistoryEntry
			executedAt string
			elapsedMs  int64
		)
		if err := rows.Scan(
			&e.ID,
			&e.ConnectionName,
			&e.SQL,
			&executedAt,
			&elapsedMs,
			&e.RowCount,
			&e.Error,
		); err != nil {
			return nil, fmt.Errorf("history scan: %w", err)
		}
		e.Elapsed = time.Duration(elapsedMs) * time.Millisecond
		// Accept both the with-ms and without-ms spellings so hand-edited
		// rows in the store don't break listing.
		if t, err := time.Parse("2006-01-02 15:04:05.000", executedAt); err == nil {
			e.ExecutedAt = t
		} else if t, err := time.Parse("2006-01-02 15:04:05", executedAt); err == nil {
			e.ExecutedAt = t
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history rows: %w", err)
	}
	return out, nil
}
