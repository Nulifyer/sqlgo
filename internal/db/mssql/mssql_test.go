package mssql

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
		wantQuery  map[string]string
		wantScheme string
	}{
		{
			name: "defaults fill in localhost and 1433",
			cfg: db.Config{
				User:     "sa",
				Password: "p",
			},
			wantHost:   "localhost:1433",
			wantUser:   "sa",
			wantPass:   "p",
			wantScheme: "sqlserver",
			wantQuery:  map[string]string{},
		},
		{
			name: "explicit host, port, database",
			cfg: db.Config{
				Host:     "db.example.com",
				Port:     11433,
				User:     "sa",
				Password: "pw",
				Database: "app",
			},
			wantHost:   "db.example.com:11433",
			wantUser:   "sa",
			wantPass:   "pw",
			wantScheme: "sqlserver",
			wantQuery:  map[string]string{"database": "app"},
		},
		{
			name: "options passed through as query params",
			cfg: db.Config{
				Host:     "h",
				Port:     1,
				User:     "u",
				Password: "pp",
				Options: map[string]string{
					"encrypt":                "disable",
					"TrustServerCertificate": "true",
				},
			},
			wantHost:   "h:1",
			wantUser:   "u",
			wantPass:   "pp",
			wantScheme: "sqlserver",
			wantQuery: map[string]string{
				"encrypt":                "disable",
				"TrustServerCertificate": "true",
			},
		},
		{
			name: "password with special characters is url-encoded",
			cfg: db.Config{
				Host:     "h",
				Port:     1,
				User:     "sa",
				Password: "p@ss w:rd!",
			},
			wantHost:   "h:1",
			wantUser:   "sa",
			wantPass:   "p@ss w:rd!",
			wantScheme: "sqlserver",
			wantQuery:  map[string]string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := buildDSN(tc.cfg)
			u, err := url.Parse(got)
			if err != nil {
				t.Fatalf("parse DSN %q: %v", got, err)
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
			pw, _ := u.User.Password()
			if pw != tc.wantPass {
				t.Errorf("password = %q, want %q", pw, tc.wantPass)
			}
			q := u.Query()
			if len(q) != len(tc.wantQuery) {
				t.Errorf("query params = %v, want %v", q, tc.wantQuery)
			}
			for k, v := range tc.wantQuery {
				if got := q.Get(k); got != v {
					t.Errorf("query[%q] = %q, want %q", k, got, v)
				}
			}
		})
	}
}
