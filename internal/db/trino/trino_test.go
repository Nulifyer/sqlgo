package trino

import (
	"net/url"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

// TestBuildDSN covers scheme toggle, host/port defaults, user/password
// encoding (driver only honors password under https), catalog path via
// ?catalog=, semantic Options mapping, and raw passthrough. An empty
// want value means "key must NOT be present".
func TestBuildDSN(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		cfg          db.Config
		wantScheme   string
		wantHost     string
		wantUser     string
		wantPassword string // "" = password absent on userinfo
		wantParams   map[string]string
		absentParams []string
	}{
		{
			name: "explicit_host_port_catalog",
			cfg: db.Config{
				Host:     "trino.internal",
				Port:     8080,
				User:     "analyst",
				Database: "hive",
			},
			wantScheme: "http",
			wantHost:   "trino.internal:8080",
			wantUser:   "analyst",
			wantParams: map[string]string{"catalog": "hive"},
		},
		{
			name:       "default_host_and_port_http",
			cfg:        db.Config{},
			wantScheme: "http",
			wantHost:   "localhost:8080",
			wantUser:   "sqlgo",
		},
		{
			name: "default_port_switches_on_https",
			cfg: db.Config{
				User:    "admin",
				Options: map[string]string{"ssl": "true"},
			},
			wantScheme: "https",
			wantHost:   "localhost:8443",
			wantUser:   "admin",
		},
		{
			name: "password_present_under_https",
			cfg: db.Config{
				Host:     "trino.internal",
				Port:     8443,
				User:     "analyst",
				Password: "hunter2",
				Options:  map[string]string{"secure": "yes"},
			},
			wantScheme:   "https",
			wantHost:     "trino.internal:8443",
			wantUser:     "analyst",
			wantPassword: "hunter2",
		},
		{
			name: "password_attached_under_http_driver_ignores",
			// buildDSN still attaches the password under http; trino-go-client
			// silently drops it (requires https). Documented behavior, verified
			// here so we notice if we ever change policy.
			cfg: db.Config{
				Host:     "trino.internal",
				Port:     8080,
				User:     "analyst",
				Password: "hunter2",
			},
			wantScheme:   "http",
			wantUser:     "analyst",
			wantPassword: "hunter2",
		},
		{
			name: "semantic_schema_and_access_token",
			cfg: db.Config{
				Host: "trino.internal",
				Port: 8443,
				User: "sp",
				Options: map[string]string{
					"ssl":          "true",
					"schema":       "analytics",
					"access_token": "eyJhbGciOi...",
				},
			},
			wantScheme: "https",
			wantParams: map[string]string{
				"schema":      "analytics",
				"accessToken": "eyJhbGciOi...",
			},
			absentParams: []string{"access_token"},
		},
		{
			name: "semantic_ssl_cert_path_mapping",
			cfg: db.Config{
				Host: "trino.internal",
				Port: 8443,
				Options: map[string]string{
					"ssl":           "true",
					"ssl_cert_path": "/etc/ssl/ca.pem",
				},
			},
			wantScheme:   "https",
			wantParams:   map[string]string{"SSLCertPath": "/etc/ssl/ca.pem"},
			absentParams: []string{"ssl_cert_path"},
		},
		{
			name: "kerberos_options_camelcase",
			cfg: db.Config{
				Host: "trino.internal",
				Port: 8443,
				Options: map[string]string{
					"ssl":                          "true",
					"kerberos_enabled":             "true",
					"kerberos_keytab_path":         "/etc/krb5.keytab",
					"kerberos_principal":           "sqlgo@EXAMPLE",
					"kerberos_realm":               "EXAMPLE",
					"kerberos_config_path":         "/etc/krb5.conf",
					"kerberos_remote_service_name": "trino",
				},
			},
			wantScheme: "https",
			wantParams: map[string]string{
				"KerberosEnabled":           "true",
				"KerberosKeytabPath":        "/etc/krb5.keytab",
				"KerberosPrincipal":         "sqlgo@EXAMPLE",
				"KerberosRealm":             "EXAMPLE",
				"KerberosConfigPath":        "/etc/krb5.conf",
				"KerberosRemoteServiceName": "trino",
			},
			absentParams: []string{
				"kerberos_enabled", "kerberos_keytab_path", "kerberos_principal",
				"kerberos_realm", "kerberos_config_path", "kerberos_remote_service_name",
			},
		},
		{
			name: "source_and_session_properties",
			cfg: db.Config{
				Host: "trino.internal",
				Port: 8080,
				Options: map[string]string{
					"source":             "sqlgo",
					"session_properties": "query_max_run_time=30m",
					"client_tags":        "env=dev,team=data",
				},
			},
			wantParams: map[string]string{
				"source":             "sqlgo",
				"session_properties": "query_max_run_time=30m",
				"clientTags":         "env=dev,team=data",
			},
			absentParams: []string{"client_tags"},
		},
		{
			name: "passthrough_unknown_option",
			cfg: db.Config{
				Host: "trino.internal",
				Port: 8080,
				Options: map[string]string{
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
			u, err := url.Parse(dsn)
			if err != nil {
				t.Fatalf("parse dsn %q: %v", dsn, err)
			}
			if tc.wantScheme != "" && u.Scheme != tc.wantScheme {
				t.Errorf("scheme = %q, want %q", u.Scheme, tc.wantScheme)
			}
			if tc.wantHost != "" && u.Host != tc.wantHost {
				t.Errorf("host = %q, want %q", u.Host, tc.wantHost)
			}
			if tc.wantUser != "" && u.User.Username() != tc.wantUser {
				t.Errorf("user = %q, want %q", u.User.Username(), tc.wantUser)
			}
			if tc.wantPassword != "" {
				pw, ok := u.User.Password()
				if !ok || pw != tc.wantPassword {
					t.Errorf("password = %q (set=%v), want %q", pw, ok, tc.wantPassword)
				}
			}
			q := u.Query()
			for k, want := range tc.wantParams {
				if got := q.Get(k); got != want {
					t.Errorf("q[%q] = %q, want %q (dsn=%s)", k, got, want, dsn)
				}
			}
			for _, k := range tc.absentParams {
				if q.Has(k) {
					t.Errorf("q[%q] present (=%q), want absent (dsn=%s)", k, q.Get(k), dsn)
				}
			}
		})
	}
}

// TestPreset_Capabilities pins the capability fingerprint so silent
// drift in Profile -> preset wiring surfaces as a test failure.
func TestPreset_Capabilities(t *testing.T) {
	t.Parallel()
	got := preset{}.Capabilities()
	if got.IdentifierQuote != '"' {
		t.Errorf("IdentifierQuote = %q, want '\"'", got.IdentifierQuote)
	}
	if got.SupportsTransactions {
		t.Error("SupportsTransactions should be false (most Trino deployments disable them)")
	}
	if got.Dialect != sqltok.DialectTrino {
		t.Errorf("Dialect = %v, want DialectTrino", got.Dialect)
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
		t.Error("SupportsTLS should be true (Trino supports TLS via https scheme)")
	}
	if !got.SupportsCancel {
		t.Error("SupportsCancel should be true")
	}
}

// TestQuoteIdent ensures double-quote escaping matches ANSI/Trino:
// wrap in ", double any embedded ".
func TestQuoteIdent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"events", `"events"`},
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
