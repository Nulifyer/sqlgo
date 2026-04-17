//go:build integration

package dbtest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/errinfo"
)

// ErrorCase describes one bad query and the structured fields the driver
// should recover from its native error.
type ErrorCase struct {
	Name string
	SQL  string

	WantEngine   string
	CheckLine    bool
	WantLine     int
	CheckColumn  bool
	WantColumn   int
	CheckNumber  bool
	WantNumber   int
	CheckState   bool
	WantState    int
	CheckClass   bool
	WantClass    int
	WantSQLState string
	WantName     string
	WantType     string
	WantReason   string
	CheckCodes   bool
	WantCodesLen int

	WantMessageContains []string
	WantFormatContains  []string

	// Check runs any extra driver-specific assertions once the shared
	// ones have passed.
	Check func(t *testing.T, info errinfo.Info)
}

// ExerciseErrorParsing runs each bad query through conn, asks the named
// driver to parse the resulting error into the shared errinfo.Info shape,
// and asserts the requested fields.
func ExerciseErrorParsing(t *testing.T, conn db.Conn, driverName string, cases []ErrorCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			err := queryErr(t, conn, tc.SQL)
			info := db.ParseErrorInfo(driverName, err, tc.SQL)
			if info.Message == "" {
				t.Fatalf("ParseErrorInfo(%q) returned empty message for %q", driverName, tc.SQL)
			}

			if tc.WantEngine != "" && info.Engine != tc.WantEngine {
				t.Fatalf("Engine = %q, want %q", info.Engine, tc.WantEngine)
			}
			if tc.CheckLine && info.Location.Line != tc.WantLine {
				t.Fatalf("Location.Line = %d, want %d", info.Location.Line, tc.WantLine)
			}
			if tc.CheckColumn && info.Location.Column != tc.WantColumn {
				t.Fatalf("Location.Column = %d, want %d", info.Location.Column, tc.WantColumn)
			}
			if tc.CheckNumber && info.Number != tc.WantNumber {
				t.Fatalf("Number = %d, want %d", info.Number, tc.WantNumber)
			}
			if tc.CheckState && info.State != tc.WantState {
				t.Fatalf("State = %d, want %d", info.State, tc.WantState)
			}
			if tc.CheckClass && info.Class != tc.WantClass {
				t.Fatalf("Class = %d, want %d", info.Class, tc.WantClass)
			}
			if tc.WantSQLState != "" && info.SQLState != tc.WantSQLState {
				t.Fatalf("SQLState = %q, want %q", info.SQLState, tc.WantSQLState)
			}
			if tc.WantName != "" && info.Name != tc.WantName {
				t.Fatalf("Name = %q, want %q", info.Name, tc.WantName)
			}
			if tc.WantType != "" && info.Type != tc.WantType {
				t.Fatalf("Type = %q, want %q", info.Type, tc.WantType)
			}
			if tc.WantReason != "" && info.Reason != tc.WantReason {
				t.Fatalf("Reason = %q, want %q", info.Reason, tc.WantReason)
			}
			if tc.CheckCodes && len(info.Codes) != tc.WantCodesLen {
				t.Fatalf("len(Codes) = %d, want %d", len(info.Codes), tc.WantCodesLen)
			}

			for _, want := range tc.WantMessageContains {
				if !strings.Contains(info.Message, want) {
					t.Fatalf("Message = %q, want substring %q", info.Message, want)
				}
			}
			formatted := info.Format()
			for _, want := range tc.WantFormatContains {
				if !strings.Contains(formatted, want) {
					t.Fatalf("Format() = %q, want substring %q", formatted, want)
				}
			}

			if tc.Check != nil {
				tc.Check(t, info)
			}
		})
	}
}

func queryErr(t *testing.T, conn db.Conn, sql string) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	rows, err := conn.Query(ctx, sql)
	if err != nil {
		if rows != nil {
			rows.Close()
		}
		return err
	}
	if rows == nil {
		t.Fatalf("expected query error for %q", sql)
	}
	defer rows.Close()

	for rows.Next() {
		if _, err := rows.Scan(); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	t.Fatalf("expected query error for %q", sql)
	return nil
}
