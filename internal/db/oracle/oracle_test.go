package oracle

import (
	"strings"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
)

func TestBuildDSNDefaults(t *testing.T) {
	t.Parallel()
	dsn := buildDSN(db.Config{User: "sqlgo", Password: "pw", Database: "ORCLPDB1"})
	if !strings.HasPrefix(dsn, "oracle://") {
		t.Fatalf("want oracle:// scheme, got %q", dsn)
	}
	if !strings.Contains(dsn, "sqlgo:pw@") {
		t.Fatalf("missing credentials: %q", dsn)
	}
	if !strings.Contains(dsn, "localhost:1521") {
		t.Fatalf("want default localhost:1521, got %q", dsn)
	}
	if !strings.HasSuffix(dsn, "/ORCLPDB1") {
		t.Fatalf("want service suffix, got %q", dsn)
	}
}

func TestIsPermissionDenied(t *testing.T) {
	t.Parallel()
	cases := []struct {
		msg  string
		want bool
	}{
		{"ORA-01031: insufficient privileges", true},
		{"ORA-00942: table or view does not exist", true},
		{"ORA-00001: unique constraint violated", false},
		{"network error", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.msg, func(t *testing.T) {
			t.Parallel()
			got := isPermissionDenied(errString(tc.msg))
			if got != tc.want {
				t.Fatalf("isPermissionDenied(%q) = %v, want %v", tc.msg, got, tc.want)
			}
		})
	}
}

type errString string

func (e errString) Error() string { return string(e) }
