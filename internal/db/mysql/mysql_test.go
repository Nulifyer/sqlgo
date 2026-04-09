package mysql

import (
	"strings"
	"testing"

	gomysql "github.com/go-sql-driver/mysql"

	"github.com/Nulifyer/sqlgo/internal/db"
)

// TestBuildDSNRoundTrip validates the generated DSN parses back through
// the upstream driver's own Config parser and preserves the fields the
// TUI cares about. The upstream ParseDSN is the only spec-accurate
// check; hand-rolling a regex here would drift when the format gains
// new options.
func TestBuildDSNRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  db.Config
		addr string
		db   string
		tls  string
	}{
		{
			name: "defaults",
			cfg:  db.Config{User: "root", Password: "pw"},
			addr: "localhost:3306",
		},
		{
			name: "explicit host + port + db",
			cfg: db.Config{
				Host:     "db.internal",
				Port:     33060,
				User:     "app",
				Password: "pw",
				Database: "shop",
			},
			addr: "db.internal:33060",
			db:   "shop",
		},
		{
			name: "tls lifted from options",
			cfg: db.Config{
				Host:     "h",
				Port:     1,
				User:     "u",
				Password: "p",
				Options:  map[string]string{"tls": "skip-verify"},
			},
			addr: "h:1",
			tls:  "skip-verify",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dsn := buildDSN(tc.cfg)
			parsed, err := gomysql.ParseDSN(dsn)
			if err != nil {
				t.Fatalf("ParseDSN(%q): %v", dsn, err)
			}
			if parsed.Addr != tc.addr {
				t.Errorf("addr = %q, want %q", parsed.Addr, tc.addr)
			}
			if tc.db != "" && parsed.DBName != tc.db {
				t.Errorf("dbname = %q, want %q", parsed.DBName, tc.db)
			}
			if tc.tls != "" && parsed.TLSConfig != tc.tls {
				t.Errorf("tls = %q, want %q", parsed.TLSConfig, tc.tls)
			}
			if !parsed.ParseTime {
				t.Errorf("parseTime = false, want true (default)")
			}
		})
	}
}

func TestBuildDSNExtraParams(t *testing.T) {
	t.Parallel()
	dsn := buildDSN(db.Config{
		User:     "u",
		Password: "p",
		Options:  map[string]string{"charset": "utf8mb4"},
	})
	// charset isn't a Config field, so it lands in Params and appears
	// in the DSN's query string.
	if !strings.Contains(dsn, "charset=utf8mb4") {
		t.Errorf("DSN missing charset param: %q", dsn)
	}
}

func TestRegistered(t *testing.T) {
	t.Parallel()
	d, err := db.Get(driverName)
	if err != nil {
		t.Fatalf("db.Get(%q): %v", driverName, err)
	}
	caps := d.Capabilities()
	if caps.SchemaDepth != db.SchemaDepthSchemas || caps.LimitSyntax != db.LimitSyntaxLimit || caps.IdentifierQuote != '`' {
		t.Errorf("capabilities = %+v", caps)
	}
}
