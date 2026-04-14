package firebird

import (
	"strings"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
)

func TestBuildDSN(t *testing.T) {
	t.Parallel()
	dsn := buildDSN(db.Config{User: "sysdba", Password: "masterkey", Host: "fb.local", Port: 3050, Database: "/var/db/acme.fdb"})
	want := "sysdba:masterkey@fb.local:3050//var/db/acme.fdb"
	if dsn != want {
		t.Fatalf("got %q want %q", dsn, want)
	}
}

func TestBuildDSNDefaultHost(t *testing.T) {
	t.Parallel()
	dsn := buildDSN(db.Config{User: "sysdba", Password: "pw", Database: "acme"})
	if !strings.HasPrefix(dsn, "sysdba:pw@localhost/acme") {
		t.Fatalf("unexpected %q", dsn)
	}
}

func TestBuildDSNOptions(t *testing.T) {
	t.Parallel()
	dsn := buildDSN(db.Config{User: "u", Password: "p", Database: "db", Options: map[string]string{"role": "READ_ONLY"}})
	if !strings.Contains(dsn, "role=READ_ONLY") {
		t.Fatalf("missing role option: %q", dsn)
	}
}

func TestIsPermissionDenied(t *testing.T) {
	t.Parallel()
	cases := []struct {
		msg  string
		want bool
	}{
		{"SQLCODE: -551 ... no permission for SELECT access to", true},
		{"generic error", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.msg, func(t *testing.T) {
			t.Parallel()
			got := isPermissionDenied(errString(tc.msg))
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

type errString string

func (e errString) Error() string { return string(e) }
