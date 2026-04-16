package azuresql

import (
	"net/url"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
)

// TestBuildDSN_Modes covers the semantic field mapping per fedauth
// mode. Each case declares the Config and the (key -> value) query
// params the resulting DSN must contain. "" as a wanted value means
// "key must NOT be present". Additional keys are tolerated so that
// adding new defaults (e.g. future driver knobs) doesn't churn the
// test.
func TestBuildDSN_Modes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		cfg        db.Config
		wantParams map[string]string
		// absentParams lists keys that MUST NOT be present. Catches
		// regressions where a mapping leaks credentials into the wrong
		// mode (e.g. Password field sneaking into ManagedIdentity DSN).
		absentParams []string
		wantHost     string
	}{
		{
			name: "password_mode",
			cfg: db.Config{
				Host:     "myserver.database.windows.net",
				Port:     1433,
				User:     "alice@contoso.com",
				Password: "pwd123",
				Database: "appdb",
				Options:  map[string]string{"fedauth": FedauthPassword},
			},
			wantParams: map[string]string{
				"fedauth":  FedauthPassword,
				"user id":  "alice@contoso.com",
				"password": "pwd123",
				"database": "appdb",
				"encrypt":  "true",
			},
			wantHost: "myserver.database.windows.net:1433",
		},
		{
			name: "service_principal_secret",
			cfg: db.Config{
				Host:     "myserver.database.windows.net",
				Port:     1433,
				User:     "11111111-2222-3333-4444-555555555555",
				Password: "client-secret-abc",
				Database: "appdb",
				Options: map[string]string{
					"fedauth":   FedauthServicePrincipal,
					"tenant_id": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
				},
			},
			wantParams: map[string]string{
				"fedauth":  FedauthServicePrincipal,
				"user id":  "11111111-2222-3333-4444-555555555555@aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
				"password": "client-secret-abc",
				"database": "appdb",
				"encrypt":  "true",
			},
			absentParams: []string{"clientcertpath", "tenant_id", "cert_password", "cert_path"},
		},
		{
			name: "service_principal_cert_with_cert_password",
			cfg: db.Config{
				Host:     "myserver.database.windows.net",
				Port:     1433,
				User:     "11111111-2222-3333-4444-555555555555",
				Password: "core-password-ignored-when-cert-password-set",
				Database: "appdb",
				Options: map[string]string{
					"fedauth":       FedauthServicePrincipal,
					"tenant_id":     "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
					"cert_path":     "/secure/sp.pfx",
					"cert_password": "cert-unlock-pwd",
				},
			},
			wantParams: map[string]string{
				"fedauth":        FedauthServicePrincipal,
				"user id":        "11111111-2222-3333-4444-555555555555@aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
				"clientcertpath": "/secure/sp.pfx",
				"password":       "cert-unlock-pwd",
				"database":       "appdb",
				"encrypt":        "true",
			},
			absentParams: []string{"tenant_id", "cert_path", "cert_password"},
		},
		{
			name: "service_principal_cert_fallback_to_core_password",
			cfg: db.Config{
				Host:     "myserver.database.windows.net",
				Port:     1433,
				User:     "11111111-2222-3333-4444-555555555555",
				Password: "fallback-cert-pwd",
				Database: "appdb",
				Options: map[string]string{
					"fedauth":   FedauthServicePrincipal,
					"tenant_id": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
					"cert_path": "/secure/sp.pfx",
				},
			},
			wantParams: map[string]string{
				"fedauth":        FedauthServicePrincipal,
				"clientcertpath": "/secure/sp.pfx",
				"password":       "fallback-cert-pwd",
			},
		},
		{
			name: "managed_identity_user_assigned",
			cfg: db.Config{
				Host:     "myserver.database.windows.net",
				Port:     1433,
				User:     "99999999-aaaa-bbbb-cccc-dddddddddddd",
				Password: "should-not-leak",
				Database: "appdb",
				Options:  map[string]string{"fedauth": FedauthManagedIdentity},
			},
			wantParams: map[string]string{
				"fedauth": FedauthManagedIdentity,
				"user id": "99999999-aaaa-bbbb-cccc-dddddddddddd",
				"encrypt": "true",
			},
			absentParams: []string{"password"},
		},
		{
			name: "managed_identity_system_assigned",
			cfg: db.Config{
				Host:     "myserver.database.windows.net",
				Port:     1433,
				Database: "appdb",
				Options:  map[string]string{"fedauth": FedauthManagedIdentity},
			},
			wantParams: map[string]string{
				"fedauth": FedauthManagedIdentity,
				"encrypt": "true",
			},
			absentParams: []string{"password", "user id"},
		},
		{
			name: "interactive_with_hint",
			cfg: db.Config{
				Host:    "myserver.database.windows.net",
				Port:    1433,
				User:    "alice@contoso.com",
				Options: map[string]string{"fedauth": FedauthInteractive},
			},
			wantParams: map[string]string{
				"fedauth": FedauthInteractive,
				"user id": "alice@contoso.com",
			},
			absentParams: []string{"password"},
		},
		{
			name: "default_no_creds",
			cfg: db.Config{
				Host:     "myserver.database.windows.net",
				Port:     1433,
				User:     "should-not-appear",
				Password: "should-not-appear",
				Options:  map[string]string{"fedauth": FedauthDefault},
			},
			wantParams: map[string]string{
				"fedauth": FedauthDefault,
				"encrypt": "true",
			},
			absentParams: []string{"user id", "password"},
		},
		{
			name: "empty_fedauth_sql_auth_fallback",
			cfg: db.Config{
				Host:     "myserver.database.windows.net",
				Port:     1433,
				User:     "sqladmin",
				Password: "sql-pwd",
				Database: "appdb",
			},
			wantParams: map[string]string{
				"user id":  "sqladmin",
				"password": "sql-pwd",
				"encrypt":  "true",
			},
			absentParams: []string{"fedauth"},
		},
		{
			name: "explicit_encrypt_override",
			cfg: db.Config{
				Host: "myserver.database.windows.net",
				Port: 1433,
				Options: map[string]string{
					"fedauth": FedauthDefault,
					"encrypt": "strict",
				},
			},
			wantParams: map[string]string{
				"encrypt": "strict",
			},
		},
		{
			name: "passthrough_unknown_option",
			cfg: db.Config{
				Host: "myserver.database.windows.net",
				Port: 1433,
				Options: map[string]string{
					"fedauth":  FedauthDefault,
					"app name": "sqlgo-test",
				},
			},
			wantParams: map[string]string{
				"app name": "sqlgo-test",
			},
		},
		{
			name: "default_host_and_port",
			cfg: db.Config{
				Options: map[string]string{"fedauth": FedauthDefault},
			},
			wantHost: "localhost:1433",
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
			if u.Scheme != "sqlserver" {
				t.Errorf("scheme = %q, want sqlserver", u.Scheme)
			}
			if tc.wantHost != "" && u.Host != tc.wantHost {
				t.Errorf("host = %q, want %q", u.Host, tc.wantHost)
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

// TestFedauthModes_OrderStable pins the exported cycler order so the
// TUI fedauth cycler doesn't silently reshuffle on driver upgrades.
// Adding a new mode should be deliberate; append at the end.
func TestFedauthModes_OrderStable(t *testing.T) {
	t.Parallel()
	want := []string{
		"",
		FedauthPassword,
		FedauthServicePrincipal,
		FedauthManagedIdentity,
		FedauthInteractive,
		FedauthDefault,
	}
	if len(FedauthModes) != len(want) {
		t.Fatalf("FedauthModes len = %d, want %d", len(FedauthModes), len(want))
	}
	for i := range want {
		if FedauthModes[i] != want[i] {
			t.Errorf("FedauthModes[%d] = %q, want %q", i, FedauthModes[i], want[i])
		}
	}
}

// TestPreset_Capabilities confirms the azuresql preset surfaces the
// mssql profile capabilities unchanged -- dialect, cross-DB, and the
// MSSQL XML explain format are inherited, not redefined.
func TestPreset_Capabilities(t *testing.T) {
	t.Parallel()
	got := preset{}.Capabilities()
	if !got.SupportsTLS {
		t.Error("expected SupportsTLS=true")
	}
	if !got.SupportsCrossDatabase {
		t.Error("expected SupportsCrossDatabase=true (inherited from mssql profile)")
	}
	if got.IdentifierQuote != '[' {
		t.Errorf("IdentifierQuote = %q, want '['", got.IdentifierQuote)
	}
}
