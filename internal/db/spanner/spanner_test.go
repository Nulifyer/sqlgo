package spanner

import (
	"net/url"
	"strings"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

// TestBuildDSN covers the projects/.../instances/.../databases/...
// base, emulator host:port prefix, ;-separated params, credentials
// credentials_json, PG dialect toggle, and raw passthrough. The DSN
// format is key=value pairs separated by `;` (NOT `&`), so we parse
// with strings.Split(';') instead of url.ParseQuery.
func TestBuildDSN(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		cfg          db.Config
		wantPrefix   string
		wantParams   map[string]string
		absentParams []string
	}{
		{
			name: "emulator_with_auto_config",
			cfg: db.Config{
				Host:     "localhost",
				Port:     19010,
				Database: "sqlgo_test",
				Options: map[string]string{
					"project":            "sqlgo-emu",
					"instance":           "sqlgo",
					"autoConfigEmulator": "true",
				},
			},
			wantPrefix: "localhost:19010/projects/sqlgo-emu/instances/sqlgo/databases/sqlgo_test",
			wantParams: map[string]string{"autoConfigEmulator": "true"},
		},
		{
			name: "production_path_only",
			cfg: db.Config{
				Database: "appdb",
				Options: map[string]string{
					"project":     "my-proj",
					"instance":    "main-inst",
					"credentials": "/etc/sa-key.json",
				},
			},
			wantPrefix: "projects/my-proj/instances/main-inst/databases/appdb",
			wantParams: map[string]string{
				// url.QueryEscape encodes '/' as %2F
				"credentials": "/etc/sa-key.json",
			},
		},
		{
			name: "credentials_json_inline",
			cfg: db.Config{
				Database: "d",
				Options: map[string]string{
					"project":          "p",
					"instance":         "i",
					"credentials_json": `{"type":"service_account"}`,
				},
			},
			wantPrefix: "projects/p/instances/i/databases/d",
			wantParams: map[string]string{
				"credentialsJson": `{"type":"service_account"}`,
			},
		},
		{
			name: "pg_dialect_toggle_and_plain_text",
			cfg: db.Config{
				Host:     "emu",
				Port:     9010,
				Database: "pgdb",
				Options: map[string]string{
					"project":            "p",
					"instance":           "i",
					"dialect":            "postgresql",
					"use_plain_text":     "true",
					"autoConfigEmulator": "true",
				},
			},
			wantPrefix: "emu:9010/projects/p/instances/i/databases/pgdb",
			wantParams: map[string]string{
				"dialect":            "postgresql",
				"usePlainText":       "true",
				"autoConfigEmulator": "true",
			},
		},
		{
			name: "sessions_and_retry_tuning",
			cfg: db.Config{
				Database: "d",
				Options: map[string]string{
					"project":                 "p",
					"instance":                "i",
					"num_channels":            "8",
					"min_sessions":            "4",
					"max_sessions":            "200",
					"retry_aborts_internally": "true",
					"database_role":           "reader",
				},
			},
			wantPrefix: "projects/p/instances/i/databases/d",
			wantParams: map[string]string{
				"numChannels":           "8",
				"minSessions":           "4",
				"maxSessions":           "200",
				"retryAbortsInternally": "true",
				"databaseRole":          "reader",
			},
		},
		{
			name: "database_option_fallback",
			cfg: db.Config{
				// cfg.Database unset -> falls back to Options["database"]
				Options: map[string]string{
					"project":  "p",
					"instance": "i",
					"database": "fallback_db",
				},
			},
			wantPrefix: "projects/p/instances/i/databases/fallback_db",
		},
		{
			name: "project_id_alias",
			cfg: db.Config{
				Database: "d",
				Options: map[string]string{
					"project_id":  "alias-proj",
					"instance_id": "alias-inst",
				},
			},
			wantPrefix: "projects/alias-proj/instances/alias-inst/databases/d",
		},
		{
			name: "passthrough_unknown_option",
			cfg: db.Config{
				Database: "d",
				Options: map[string]string{
					"project":     "p",
					"instance":    "i",
					"custom_knob": "on",
				},
			},
			wantPrefix: "projects/p/instances/i/databases/d",
			wantParams: map[string]string{"custom_knob": "on"},
		},
		{
			name: "empty_option_absent",
			cfg: db.Config{
				Database: "d",
				Options: map[string]string{
					"project":       "p",
					"instance":      "i",
					"credentials":   "",
					"database_role": "reader",
				},
			},
			wantPrefix:   "projects/p/instances/i/databases/d",
			wantParams:   map[string]string{"databaseRole": "reader"},
			absentParams: []string{"credentials"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dsn := buildDSN(tc.cfg)
			// Reject `&` anywhere -- the driver rejects `&` between
			// params, so catching it at DSN-build time guards against
			// accidentally switching to url.Values.Encode() in future.
			if strings.Contains(dsn, "&") {
				t.Errorf("DSN contains '&' (must use ';'): %q", dsn)
			}
			prefix, rest, _ := strings.Cut(dsn, ";")
			if prefix != tc.wantPrefix {
				t.Errorf("prefix = %q, want %q (dsn=%s)", prefix, tc.wantPrefix, dsn)
			}

			got := map[string]string{}
			if rest != "" {
				for _, pair := range strings.Split(rest, ";") {
					k, v, ok := strings.Cut(pair, "=")
					if !ok {
						continue
					}
					if dec, err := url.QueryUnescape(v); err == nil {
						v = dec
					}
					got[k] = v
				}
			}
			for k, want := range tc.wantParams {
				if g := got[k]; g != want {
					t.Errorf("q[%q] = %q, want %q (dsn=%s)", k, g, want, dsn)
				}
			}
			for _, k := range tc.absentParams {
				if _, ok := got[k]; ok {
					t.Errorf("q[%q] present (=%q), want absent (dsn=%s)", k, got[k], dsn)
				}
			}
		})
	}
}

// TestPreset_Capabilities pins the capability fingerprint so drift in
// Profile -> preset wiring surfaces as a test failure.
func TestPreset_Capabilities(t *testing.T) {
	t.Parallel()
	got := preset{}.Capabilities()
	if got.IdentifierQuote != '`' {
		t.Errorf("IdentifierQuote = %q, want '`'", got.IdentifierQuote)
	}
	if !got.SupportsTransactions {
		t.Error("SupportsTransactions should be true (Spanner has read/write transactions)")
	}
	if got.Dialect != sqltok.DialectSpanner {
		t.Errorf("Dialect = %v, want DialectSpanner", got.Dialect)
	}
	if got.SchemaDepth != db.SchemaDepthSchemas {
		t.Errorf("SchemaDepth = %v, want SchemaDepthSchemas", got.SchemaDepth)
	}
	if got.LimitSyntax != db.LimitSyntaxLimit {
		t.Errorf("LimitSyntax = %v, want LimitSyntaxLimit", got.LimitSyntax)
	}
	if got.ExplainFormat != db.ExplainFormatNone {
		t.Errorf("ExplainFormat = %v, want ExplainFormatNone", got.ExplainFormat)
	}
	if !got.SupportsTLS {
		t.Error("SupportsTLS should be true (Spanner is gRPC-over-TLS)")
	}
	if !got.SupportsCancel {
		t.Error("SupportsCancel should be true")
	}
	if got.SupportsCrossDatabase {
		t.Error("SupportsCrossDatabase should be false (pinned to one projects/I/D path)")
	}
}

// TestQuoteIdent confirms backtick escaping (GoogleSQL grammar).
func TestQuoteIdent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"Singers", "`Singers`"},
		{"with space", "`with space`"},
		{"has`tick", "`has``tick`"},
		{"", "``"},
	}
	for _, tc := range cases {
		if got := quoteIdent(tc.in); got != tc.want {
			t.Errorf("quoteIdent(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
