package oracle

import (
	"net/url"
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

// TestTranslateOracleOptions covers the snake_case -> go-ora key mapping.
// go-ora's connect_config parser is case-insensitive but uses spaces
// between words, which the TUI can't type directly.
func TestTranslateOracleOptions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   map[string]string
		want map[string]string
	}{
		{
			name: "nil input",
			in:   nil,
			want: nil,
		},
		{
			name: "wallet + ssl",
			in: map[string]string{
				"wallet_path":     "/opt/oracle/wallet",
				"wallet_password": "wpw",
				"ssl":             "true",
				"ssl_verify":      "false",
			},
			want: map[string]string{
				"WALLET":          "/opt/oracle/wallet",
				"WALLET PASSWORD": "wpw",
				"SSL":             "true",
				"SSL VERIFY":      "false",
			},
		},
		{
			name: "os auth",
			in: map[string]string{
				"auth_type":   "OS",
				"os_user":     "oracle",
				"os_password": "opw",
			},
			want: map[string]string{
				"AUTH TYPE": "OS",
				"OS USER":   "oracle",
				"OS PASS":   "opw",
			},
		},
		{
			name: "server dn",
			in:   map[string]string{"server_dn": "CN=oracle,O=org"},
			want: map[string]string{"SSL SERVER CERT DN": "CN=oracle,O=org"},
		},
		{
			name: "unknown key passes through",
			in:   map[string]string{"PREFETCH_ROWS": "500"},
			want: map[string]string{"PREFETCH_ROWS": "500"},
		},
		{
			name: "blank values dropped",
			in: map[string]string{
				"wallet_path": "   ",
				"ssl":         "true",
			},
			want: map[string]string{"SSL": "true"},
		},
		{
			name: "case insensitive keys",
			in:   map[string]string{"Wallet_Path": "/w"},
			want: map[string]string{"WALLET": "/w"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := translateOracleOptions(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d: got=%v", len(got), len(tc.want), got)
			}
			for k, want := range tc.want {
				if g, ok := got[k]; !ok || g != want {
					t.Errorf("got[%q] = %q (present=%v), want %q", k, g, ok, want)
				}
			}
		})
	}
}

// TestBuildDSNWalletTCPS verifies the mTLS/TCPS path produces a URL
// whose query string carries go-ora's space-separated keys. We URL-decode
// and check the fields rather than match the escaped string byte-for-byte.
func TestBuildDSNWalletTCPS(t *testing.T) {
	t.Parallel()
	dsn := buildDSN(db.Config{
		Host:     "orcl.internal",
		Port:     2484,
		User:     "sqlgo",
		Password: "pw",
		Database: "ORCLPDB1",
		Options: map[string]string{
			"auth_type":       "TCPS",
			"wallet_path":     "/opt/oracle/wallet",
			"wallet_password": "wpw",
			"ssl":             "true",
			"ssl_verify":      "true",
			// Keep server_dn comma-free -- go-ora's BuildUrl splits option
			// values on "," and emits one DSN param per piece, which the
			// url.Query parser then collapses to the first. A DN with
			// commas is a known go-ora quirk, not in scope here.
			"server_dn": "CN=oracle",
		},
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	if u.Host != "orcl.internal:2484" {
		t.Errorf("host = %q, want orcl.internal:2484", u.Host)
	}
	q := u.Query()
	wants := map[string]string{
		"WALLET":             "/opt/oracle/wallet",
		"WALLET PASSWORD":    "wpw",
		"AUTH TYPE":          "TCPS",
		"SSL":                "true",
		"SSL VERIFY":         "true",
		"SSL SERVER CERT DN": "CN=oracle",
	}
	for k, want := range wants {
		if got := q.Get(k); got != want {
			t.Errorf("q[%q] = %q, want %q", k, got, want)
		}
	}
}

// TestBuildDSNOSAuth covers the AUTH TYPE=OS path. Password is still in
// the URL userinfo slot -- go-ora ignores it when AUTH TYPE=OS but the
// connection form allows entering one anyway.
func TestBuildDSNOSAuth(t *testing.T) {
	t.Parallel()
	dsn := buildDSN(db.Config{
		Host:     "orcl.internal",
		Port:     1521,
		User:     "sqlgo",
		Database: "ORCLPDB1",
		Options: map[string]string{
			"auth_type":   "OS",
			"os_user":     "oracle",
			"os_password": "opw",
		},
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	q := u.Query()
	if got := q.Get("AUTH TYPE"); got != "OS" {
		t.Errorf("AUTH TYPE = %q, want OS", got)
	}
	if got := q.Get("OS USER"); got != "oracle" {
		t.Errorf("OS USER = %q, want oracle", got)
	}
	if got := q.Get("OS PASS"); got != "opw" {
		t.Errorf("OS PASS = %q, want opw", got)
	}
}

func TestRegistered(t *testing.T) {
	t.Parallel()
	d, err := db.Get(driverName)
	if err != nil {
		t.Fatalf("db.Get(%q): %v", driverName, err)
	}
	caps := d.Capabilities()
	if caps.SchemaDepth != db.SchemaDepthSchemas {
		t.Errorf("SchemaDepth = %v", caps.SchemaDepth)
	}
	if caps.IdentifierQuote != '"' {
		t.Errorf("IdentifierQuote = %q, want \"", caps.IdentifierQuote)
	}
}
