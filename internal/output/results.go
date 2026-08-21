package output

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/Nulifyer/sqlgo/internal/db"
)

// Result is one buffered result set in a multi-result invocation.
type Result struct {
	Columns []db.Column
	Rows    [][]string
}

// WriteResults serializes all result sets as one document. JSON preserves the
// existing object shape for one result and uses an array of those objects for
// multiple results. HTML emits every table inside one html document.
func WriteResults(w io.Writer, results []Result, format Format) error {
	switch format {
	case JSON:
		if len(results) == 1 {
			return writeJSON(w, results[0].Columns, results[0].Rows)
		}
		out := make([]jsonResult, 0, len(results))
		for _, result := range results {
			out = append(out, makeJSONResult(result.Columns, result.Rows))
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return fmt.Errorf("output json: %w", err)
		}
		return nil
	case HTML:
		return writeHTMLResults(w, results)
	default:
		for _, result := range results {
			if err := Write(w, result.Columns, result.Rows, format); err != nil {
				return err
			}
		}
		return nil
	}
}
