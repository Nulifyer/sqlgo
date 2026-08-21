package output

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"

	"github.com/Nulifyer/sqlgo/internal/db"
)

// StreamsRows reports whether format can be written one row at a time without
// retaining the result set in memory.
func StreamsRows(format Format) bool {
	return format == CSV || format == TSV || format == JSONL
}

// RowStream serializes one result set incrementally. Call Close after the last
// row so buffered encoders can surface their final write error.
type RowStream struct {
	cols   []db.Column
	csv    *csv.Writer
	json   *json.Encoder
	rowNum int
}

// NewRowStream starts an incremental CSV, TSV, or JSONL result set.
func NewRowStream(w io.Writer, cols []db.Column, format Format) (*RowStream, error) {
	s := &RowStream{cols: cols}
	switch format {
	case CSV, TSV:
		s.csv = csv.NewWriter(w)
		if format == TSV {
			s.csv.Comma = '\t'
		}
		if err := s.csv.Write(columnNames(cols)); err != nil {
			return nil, fmt.Errorf("output header: %w", err)
		}
	case JSONL:
		s.json = json.NewEncoder(w)
	default:
		return nil, fmt.Errorf("output: format %s does not support row streaming", format)
	}
	return s, nil
}

// WriteRow writes one already-stringified row.
func (s *RowStream) WriteRow(row []string) error {
	if s.csv != nil {
		if err := s.csv.Write(padRow(row, len(s.cols))); err != nil {
			return fmt.Errorf("output row %d: %w", s.rowNum, err)
		}
	} else {
		rec := make(map[string]string, len(s.cols))
		for i, c := range s.cols {
			if i < len(row) {
				rec[c.Name] = row[i]
			} else {
				rec[c.Name] = ""
			}
		}
		if err := s.json.Encode(rec); err != nil {
			return fmt.Errorf("output jsonl row %d: %w", s.rowNum, err)
		}
	}
	s.rowNum++
	return nil
}

// Close flushes any buffered output and reports the underlying writer error.
func (s *RowStream) Close() error {
	if s.csv == nil {
		return nil
	}
	s.csv.Flush()
	if err := s.csv.Error(); err != nil {
		return fmt.Errorf("output: %w", err)
	}
	return nil
}
