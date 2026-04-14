package db

import "strings"

// wrapExplainSQL builds the driver-specific EXPLAIN prefix. Strips
// trailing semicolons + whitespace so the wrapper SQL lands as one
// statement. MSSQL is absent — it uses a custom ExplainRunner.
func wrapExplainSQL(format ExplainFormat, sql string) string {
	trimmed := strings.TrimRightFunc(strings.TrimSpace(sql), func(r rune) bool {
		return r == ';' || r == ' ' || r == '\t' || r == '\n'
	})
	switch format {
	case ExplainFormatPostgresJSON:
		return "EXPLAIN (FORMAT JSON) " + trimmed
	case ExplainFormatMySQLJSON:
		return "EXPLAIN FORMAT=JSON " + trimmed
	case ExplainFormatSQLiteRows:
		return "EXPLAIN QUERY PLAN " + trimmed
	}
	return trimmed
}
