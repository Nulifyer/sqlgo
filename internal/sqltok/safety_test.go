package sqltok

import "testing"

func TestUnsafeMutations(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		src    string
		wantN  int
		reason string // reason of the first flagged statement (empty if wantN == 0)
	}{
		{"update no where", "UPDATE t SET a = 1", 1, "UPDATE without WHERE"},
		{"update with where", "UPDATE t SET a = 1 WHERE id = 2", 0, ""},
		{"delete no where", "DELETE FROM t", 1, "DELETE without WHERE"},
		{"delete with where", "DELETE FROM t WHERE id = 1", 0, ""},
		{"truncate", "TRUNCATE TABLE t", 1, "TRUNCATE"},
		{"drop table", "DROP TABLE t", 1, "DROP TABLE"},
		{"drop database", "DROP DATABASE prod", 1, "DROP DATABASE"},
		{"select is safe", "SELECT * FROM t", 0, ""},
		{"insert is safe", "INSERT INTO t VALUES (1)", 0, ""},
		{"subquery where doesn't count", "UPDATE t SET a = (SELECT 1 FROM u WHERE u.id = 1)", 1, "UPDATE without WHERE"},
		{"multi-stmt mix", "SELECT 1; DELETE FROM t; UPDATE t SET a=1 WHERE id=1", 1, "DELETE without WHERE"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := UnsafeMutations(tc.src)
			if len(got) != tc.wantN {
				t.Fatalf("UnsafeMutations(%q) returned %d entries, want %d: %+v", tc.src, len(got), tc.wantN, got)
			}
			if tc.wantN > 0 && got[0].Reason != tc.reason {
				t.Errorf("first reason = %q, want %q", got[0].Reason, tc.reason)
			}
		})
	}
}
