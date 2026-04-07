package db

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const maxPreviewRows = 200

type QueryResult struct {
	Provider      Provider
	Profile       ConnectionProfile
	SQL           string
	Columns       []string
	Rows          [][]string
	RowsFetched   int
	RowsAffected  int64
	Duration      time.Duration
	IsQuery       bool
	Truncated     bool
	Message       string
	ExecutedAtUTC time.Time
}

func RunQuery(ctx context.Context, profile ConnectionProfile, registry *Registry, sqlText string) (QueryResult, error) {
	return RunQueryWithSecrets(ctx, profile, registry, nil, sqlText)
}

func RunQueryWithSecrets(ctx context.Context, profile ConnectionProfile, registry *Registry, secrets SecretStore, sqlText string) (QueryResult, error) {
	start := time.Now().UTC()
	conn, provider, err := OpenWithSecrets(profile, registry, secrets)
	if err != nil {
		return QueryResult{}, err
	}
	defer conn.Close()

	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	result := QueryResult{
		Provider:      provider,
		Profile:       profile,
		SQL:           sqlText,
		ExecutedAtUTC: start,
	}

	isQuery := looksLikeQuery(sqlText)
	if profile.ReadOnly && !isQuery {
		return QueryResult{}, fmt.Errorf("read-only connection blocks write or session-changing statements")
	}

	if isQuery {
		rows, err := conn.QueryContext(runCtx, sqlText)
		if err != nil {
			return QueryResult{}, err
		}
		defer rows.Close()

		columns, err := rows.Columns()
		if err != nil {
			return QueryResult{}, err
		}

		result.IsQuery = true
		result.Columns = columns

		for rows.Next() {
			values := make([]any, len(columns))
			scanArgs := make([]any, len(columns))
			for i := range values {
				scanArgs[i] = &values[i]
			}

			if err := rows.Scan(scanArgs...); err != nil {
				return QueryResult{}, err
			}

			row := make([]string, len(columns))
			for i, value := range values {
				row[i] = stringifyValue(value)
			}

			if result.RowsFetched < maxPreviewRows {
				result.Rows = append(result.Rows, row)
			} else {
				result.Truncated = true
			}
			result.RowsFetched++
		}

		if err := rows.Err(); err != nil {
			return QueryResult{}, err
		}

		result.Duration = time.Since(start)
		result.Message = fmt.Sprintf("Fetched %d row(s)", result.RowsFetched)
		return result, nil
	}

	execResult, err := conn.ExecContext(runCtx, sqlText)
	if err != nil {
		return QueryResult{}, err
	}

	rowsAffected, _ := execResult.RowsAffected()
	result.IsQuery = false
	result.RowsAffected = rowsAffected
	result.Duration = time.Since(start)
	result.Message = fmt.Sprintf("Statement completed, rows affected: %d", rowsAffected)

	return result, nil
}

func looksLikeQuery(sqlText string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(sqlText))
	switch {
	case strings.HasPrefix(trimmed, "select"):
		return true
	case strings.HasPrefix(trimmed, "with"):
		return true
	case strings.HasPrefix(trimmed, "show"):
		return true
	case strings.HasPrefix(trimmed, "describe"):
		return true
	case strings.HasPrefix(trimmed, "desc"):
		return true
	case strings.HasPrefix(trimmed, "pragma"):
		return true
	default:
		return false
	}
}

func stringifyValue(value any) string {
	switch v := value.(type) {
	case nil:
		return "NULL"
	case []byte:
		return string(v)
	case time.Time:
		return v.Format(time.RFC3339Nano)
	default:
		return fmt.Sprint(v)
	}
}
