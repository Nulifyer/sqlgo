package snowflake

import (
	"net/url"
	"strings"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

// TestBuildDSN covers the account@user form, database+schema path
// composition, semantic option mapping, and raw passthrough. The DSN
// gosnowflake wants is NOT a full url (no scheme), so we split on
// '?' to inspect the prefix and parse the query ourselves.
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
			name: "user_pass_account_db",
			cfg: db.Config{
				Host:     "xy12345.us-east-1",
				User:     "SQLGO",
				Password: "hunter2",
				Database: "SQLGO_DB",
			},
			wantPrefix: "SQLGO:hunter2@xy12345.us-east-1/SQLGO_DB",
		},
		{
			name: "database_and_schema_path",
			cfg: db.Config{
				Host:     "xy12345.us-east-1",
				User:     "SQLGO",
				Password: "p",
				Database: "SQLGO_DB",
				Options:  map[string]string{"schema": "PUBLIC"},
			},
			wantPrefix:   "SQLGO:p@xy12345.us-east-1/SQLGO_DB/PUBLIC",
			absentParams: []string{"schema"},
		},
		{
			name: "warehouse_and_role",
			cfg: db.Config{
				Host:     "xy12345.us-east-1",
				User:     "SQLGO",
				Password: "p",
				Database: "SQLGO_DB",
				Options: map[string]string{
					"warehouse": "COMPUTE_WH",
					"role":      "SYSADMIN",
				},
			},
			wantPrefix: "SQLGO:p@xy12345.us-east-1/SQLGO_DB",
			wantParams: map[string]string{
				"warehouse": "COMPUTE_WH",
				"role":      "SYSADMIN",
			},
		},
		{
			name: "authenticator_and_keypair",
			cfg: db.Config{
				Host:     "xy12345.us-east-1",
				User:     "SQLGO",
				Database: "SQLGO_DB",
				Options: map[string]string{
					"authenticator":          "jwt",
					"private_key_path":       "/etc/snow/key.p8",
					"private_key_passphrase": "secret",
				},
			},
			wantPrefix: "SQLGO@xy12345.us-east-1/SQLGO_DB",
			wantParams: map[string]string{
				"authenticator":     "jwt",
				"privateKeyFile":    "/etc/snow/key.p8",
				"privateKeyFilePwd": "secret",
			},
			absentParams: []string{
				"private_key_path", "private_key_passphrase",
			},
		},
		{
			name: "timeouts_camelcase",
			cfg: db.Config{
				Host:     "xy12345.us-east-1",
				User:     "SQLGO",
				Password: "p",
				Database: "SQLGO_DB",
				Options: map[string]string{
					"login_timeout":             "30s",
					"request_timeout":           "60s",
					"client_session_keep_alive": "true",
				},
			},
			wantParams: map[string]string{
				"loginTimeout":           "30s",
				"requestTimeout":         "60s",
				"clientSessionKeepAlive": "true",
			},
			absentParams: []string{
				"login_timeout", "request_timeout", "client_session_keep_alive",
			},
		},
		{
			name: "host_port_override",
			cfg: db.Config{
				Host:     "private-link.snowflakecomputing.com",
				Port:     443,
				User:     "SQLGO",
				Password: "p",
				Database: "SQLGO_DB",
			},
			wantPrefix: "SQLGO:p@private-link.snowflakecomputing.com:443/SQLGO_DB",
		},
		{
			name: "empty_option_absent",
			cfg: db.Config{
				Host:     "xy12345.us-east-1",
				User:     "SQLGO",
				Password: "p",
				Database: "SQLGO_DB",
				Options: map[string]string{
					"warehouse": "",
					"role":      "SYSADMIN",
				},
			},
			wantParams:   map[string]string{"role": "SYSADMIN"},
			absentParams: []string{"warehouse"},
		},
		{
			name: "passthrough_unknown_option",
			cfg: db.Config{
				Host:     "xy12345.us-east-1",
				User:     "SQLGO",
				Password: "p",
				Database: "SQLGO_DB",
				Options:  map[string]string{"custom_knob": "on"},
			},
			wantParams: map[string]string{"custom_knob": "on"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dsn := buildDSN(tc.cfg)
			// gosnowflake DSNs are `user:pass@host/path?query` with no scheme.
			// Must NOT start with "//" (url.URL emits that for empty-scheme URLs;
			// buildDSN strips the prefix itself).
			if strings.HasPrefix(dsn, "//") {
				t.Errorf("DSN has leading //: %q", dsn)
			}
			prefix, rawQuery, _ := strings.Cut(dsn, "?")
			if tc.wantPrefix != "" && prefix != tc.wantPrefix {
				t.Errorf("prefix = %q, want %q (dsn=%s)", prefix, tc.wantPrefix, dsn)
			}

			// Parse the query portion. Values can contain %-escapes
			// (url.URL.Query().Encode() percent-encodes '/' and others),
			// so run each value through QueryUnescape before comparing.
			got := map[string]string{}
			if rawQuery != "" {
				for _, pair := range strings.Split(rawQuery, "&") {
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

// TestPreset_Capabilities pins the capability fingerprint so drift
// in Profile -> preset wiring surfaces as a test failure.
func TestPreset_Capabilities(t *testing.T) {
	t.Parallel()
	got := preset{}.Capabilities()
	if got.IdentifierQuote != '"' {
		t.Errorf("IdentifierQuote = %q, want '\"'", got.IdentifierQuote)
	}
	if !got.SupportsTransactions {
		t.Error("SupportsTransactions should be true (Snowflake supports ACID transactions)")
	}
	if got.Dialect != sqltok.DialectSnowflake {
		t.Errorf("Dialect = %v, want DialectSnowflake", got.Dialect)
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
		t.Error("SupportsTLS should be true (Snowflake is HTTPS-only)")
	}
	if !got.SupportsCancel {
		t.Error("SupportsCancel should be true")
	}
	if got.SupportsCrossDatabase {
		t.Error("SupportsCrossDatabase should be false (connection pinned to one default DB)")
	}
}

// TestQuoteIdent ensures double-quote escaping matches ANSI/Snowflake:
// wrap in ", double any embedded ".
func TestQuoteIdent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"WIDGETS", `"WIDGETS"`},
		{"with space", `"with space"`},
		{`has"quote`, `"has""quote"`},
		{"", `""`},
	}
	for _, tc := range cases {
		if got := quoteIdent(tc.in); got != tc.want {
			t.Errorf("quoteIdent(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
