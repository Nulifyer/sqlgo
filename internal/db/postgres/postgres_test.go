package postgres

import (
	"net/url"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
)

func TestBuildDSN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		cfg        db.Config
		wantHost   string
		wantUser   string
		wantPass   string
		wantPath   string
		wantQuery  map[string]string
		wantScheme string
	}{
		{
			name: "defaults fill in localhost and 5432",
			cfg: db.Config{
				User:     "postgres",
				Password: "p",
			},
			wantHost:   "localhost:5432",
			wantUser:   "postgres",
			wantPass:   "p",
			wantScheme: "postgres",
			wantQuery:  map[string]string{},
		},
		{
			name: "explicit host port database",
			cfg: db.Config{
				Host:     "db.example.com",
				Port:     55432,
				User:     "app",
				Password: "pw",
				Database: "appdb",
			},
			wantHost:   "db.example.com:55432",
			wantUser:   "app",
			wantPass:   "pw",
			wantPath:   "/appdb",
			wantScheme: "postgres",
			wantQuery:  map[string]string{},
		},
		{
			name: "sslmode + application_name pass through",
			cfg: db.Config{
				Host:     "h",
				Port:     1,
				User:     "u",
				Password: "pp",
				Options: map[string]string{
					"sslmode":          "require",
					"application_name": "sqlgo",
				},
			},
			wantHost:   "h:1",
			wantUser:   "u",
			wantPass:   "pp",
			wantScheme: "postgres",
			wantQuery: map[string]string{
				"sslmode":          "require",
				"application_name": "sqlgo",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := buildDSN(tc.cfg)
			u, err := url.Parse(got)
			if err != nil {
				t.Fatalf("parse %q: %v", got, err)
			}
			if u.Scheme != tc.wantScheme {
				t.Errorf("scheme = %q, want %q", u.Scheme, tc.wantScheme)
			}
			if u.Host != tc.wantHost {
				t.Errorf("host = %q, want %q", u.Host, tc.wantHost)
			}
			if u.User.Username() != tc.wantUser {
				t.Errorf("user = %q, want %q", u.User.Username(), tc.wantUser)
			}
			if pw, _ := u.User.Password(); pw != tc.wantPass {
				t.Errorf("pass = %q, want %q", pw, tc.wantPass)
			}
			if tc.wantPath != "" && u.Path != tc.wantPath {
				t.Errorf("path = %q, want %q", u.Path, tc.wantPath)
			}
			q := u.Query()
			for k, v := range tc.wantQuery {
				if q.Get(k) != v {
					t.Errorf("query[%q] = %q, want %q", k, q.Get(k), v)
				}
			}
		})
	}
}

func TestRegistered(t *testing.T) {
	t.Parallel()
	d, err := db.Get(driverName)
	if err != nil {
		t.Fatalf("db.Get(%q): %v", driverName, err)
	}
	caps := d.Capabilities()
	if caps.SchemaDepth != db.SchemaDepthSchemas || caps.LimitSyntax != db.LimitSyntaxLimit || caps.IdentifierQuote != '"' {
		t.Errorf("capabilities = %+v", caps)
	}
}
