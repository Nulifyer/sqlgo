package databricks

import (
	"net/url"
	"strings"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

// TestBuildDSN covers the token:@host:port/http_path form, catalog/
// schema mapping from Database + Options, OAuth M2M credential
// placement, and raw passthrough. databricks-sql-go DSNs have no
// scheme, so we split on '?' for prefix inspection and parse the
// query ourselves (with url.QueryUnescape for % escapes the driver
// emits on slashes in http_path values).
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
			name: "pat_token_with_catalog_and_http_path",
			cfg: db.Config{
				Host:     "dbc-abc.cloud.databricks.com",
				Port:     443,
				Password: "dapi-hunter2",
				Database: "MAIN",
				Options: map[string]string{
					"http_path": "/sql/1.0/warehouses/abc123",
					"schema":    "default",
				},
			},
			wantPrefix: "token:dapi-hunter2@dbc-abc.cloud.databricks.com:443/sql/1.0/warehouses/abc123",
			wantParams: map[string]string{
				"catalog": "MAIN",
				"schema":  "default",
			},
		},
		{
			name: "default_port_when_unset",
			cfg: db.Config{
				Host:     "workspace.cloud.databricks.com",
				Password: "dapi-xyz",
				Database: "MAIN",
				Options:  map[string]string{"http_path": "/sql/1.0/endpoints/wh"},
			},
			wantPrefix: "token:dapi-xyz@workspace.cloud.databricks.com:443/sql/1.0/endpoints/wh",
			wantParams: map[string]string{"catalog": "MAIN"},
		},
		{
			name: "http_path_without_leading_slash_normalized",
			cfg: db.Config{
				Host:     "w.cloud.databricks.com",
				Password: "t",
				Options:  map[string]string{"http_path": "sql/1.0/warehouses/xyz"},
			},
			wantPrefix: "token:t@w.cloud.databricks.com:443/sql/1.0/warehouses/xyz",
		},
		{
			name: "oauth_m2m_no_userinfo_credentials_in_query",
			cfg: db.Config{
				Host: "w.cloud.databricks.com",
				User: "my-client-id",
				// Password is the client secret in OAuth M2M flow.
				Password: "my-client-secret",
				Database: "MAIN",
				Options: map[string]string{
					"authType":  "OAuthM2M",
					"http_path": "/sql/1.0/warehouses/w1",
				},
			},
			// No userinfo expected for OAuth M2M -- credentials move
			// to the query string.
			wantPrefix: "w.cloud.databricks.com:443/sql/1.0/warehouses/w1",
			wantParams: map[string]string{
				"authType":     "OAuthM2M",
				"clientID":     "my-client-id",
				"clientSecret": "my-client-secret",
				"catalog":      "MAIN",
			},
		},
		{
			name: "oauth_m2m_explicit_client_fields_override_user_password",
			cfg: db.Config{
				Host:     "w.cloud.databricks.com",
				User:     "ignored-user",
				Password: "ignored-pw",
				Options: map[string]string{
					"authType":     "OAuthM2M",
					"clientID":     "explicit-id",
					"clientSecret": "explicit-secret",
					"http_path":    "/sql/1.0/warehouses/w1",
				},
			},
			wantPrefix: "w.cloud.databricks.com:443/sql/1.0/warehouses/w1",
			wantParams: map[string]string{
				"authType":     "OAuthM2M",
				"clientID":     "explicit-id",
				"clientSecret": "explicit-secret",
			},
		},
		{
			name: "oauth_u2m_no_credentials",
			cfg: db.Config{
				Host:     "w.cloud.databricks.com",
				Database: "MAIN",
				Options: map[string]string{
					"authType":  "OauthU2M",
					"http_path": "/sql/1.0/warehouses/w1",
				},
			},
			wantPrefix: "w.cloud.databricks.com:443/sql/1.0/warehouses/w1",
			wantParams: map[string]string{
				"authType": "OauthU2M",
				"catalog":  "MAIN",
			},
			absentParams: []string{"clientID", "clientSecret"},
		},
		{
			name: "tuning_options_passthrough",
			cfg: db.Config{
				Host:     "w.cloud.databricks.com",
				Password: "pat",
				Options: map[string]string{
					"http_path":      "/sql/1.0/warehouses/w1",
					"timeout":        "30",
					"maxRows":        "50000",
					"useCloudFetch":  "false",
					"userAgentEntry": "sqlgo/1.0",
				},
			},
			wantParams: map[string]string{
				"timeout":        "30",
				"maxRows":        "50000",
				"useCloudFetch":  "false",
				"userAgentEntry": "sqlgo/1.0",
			},
		},
		{
			name: "empty_option_absent",
			cfg: db.Config{
				Host:     "w.cloud.databricks.com",
				Password: "pat",
				Options: map[string]string{
					"http_path": "/sql/1.0/warehouses/w1",
					"schema":    "",
					"timeout":   "30",
				},
			},
			wantParams:   map[string]string{"timeout": "30"},
			absentParams: []string{"schema"},
		},
		{
			name: "passthrough_unknown_option",
			cfg: db.Config{
				Host:     "w.cloud.databricks.com",
				Password: "pat",
				Options: map[string]string{
					"http_path":   "/sql/1.0/warehouses/w1",
					"custom_knob": "on",
				},
			},
			wantParams: map[string]string{"custom_knob": "on"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dsn := buildDSN(tc.cfg)
			if strings.HasPrefix(dsn, "//") {
				t.Errorf("DSN has leading //: %q", dsn)
			}
			prefix, rawQuery, _ := strings.Cut(dsn, "?")
			if tc.wantPrefix != "" && prefix != tc.wantPrefix {
				t.Errorf("prefix = %q, want %q (dsn=%s)", prefix, tc.wantPrefix, dsn)
			}

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

// TestPreset_Capabilities pins the capability fingerprint so drift in
// Profile -> preset wiring surfaces as a test failure.
func TestPreset_Capabilities(t *testing.T) {
	t.Parallel()
	got := preset{}.Capabilities()
	if got.IdentifierQuote != '`' {
		t.Errorf("IdentifierQuote = %q, want '`'", got.IdentifierQuote)
	}
	if got.SupportsTransactions {
		t.Error("SupportsTransactions should be false (Databricks SQL is auto-commit only)")
	}
	if got.Dialect != sqltok.DialectDatabricks {
		t.Errorf("Dialect = %v, want DialectDatabricks", got.Dialect)
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
		t.Error("SupportsTLS should be true (Databricks is HTTPS-only)")
	}
	if !got.SupportsCancel {
		t.Error("SupportsCancel should be true")
	}
	if got.SupportsCrossDatabase {
		t.Error("SupportsCrossDatabase should be false (connection pinned to one catalog)")
	}
}

// TestQuoteIdent ensures backtick escaping matches Spark SQL: wrap
// in `, double any embedded `.
func TestQuoteIdent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"widgets", "`widgets`"},
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
