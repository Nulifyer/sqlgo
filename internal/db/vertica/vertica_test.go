package vertica

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

// TestBuildDSN covers host/port/user/password/database encoding and the
// semantic Options passthrough. Empty want entries mean "must be absent".
func TestBuildDSN(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		cfg          db.Config
		wantScheme   string
		wantHost     string
		wantUser     string
		wantPassword string
		wantPath     string
		wantParams   map[string]string
		absentParams []string
	}{
		{
			name: "explicit_host_port_user_db",
			cfg: db.Config{
				Host:     "vertica.internal",
				Port:     5433,
				User:     "dbadmin",
				Password: "hunter2",
				Database: "VMart",
			},
			wantScheme:   "vertica",
			wantHost:     "vertica.internal:5433",
			wantUser:     "dbadmin",
			wantPassword: "hunter2",
			wantPath:     "/VMart",
		},
		{
			name:       "default_host_and_port",
			cfg:        db.Config{User: "dbadmin"},
			wantScheme: "vertica",
			wantHost:   "localhost:5433",
			wantUser:   "dbadmin",
		},
		{
			name: "tlsmode_and_backup_nodes",
			cfg: db.Config{
				Host:     "vertica.internal",
				Port:     5433,
				User:     "dbadmin",
				Database: "VMart",
				Options: map[string]string{
					"tlsmode":            "server",
					"backup_server_node": "10.0.0.2:5433,10.0.0.3:5433",
				},
			},
			wantParams: map[string]string{
				"tlsmode":            "server",
				"backup_server_node": "10.0.0.2:5433,10.0.0.3:5433",
			},
		},
		{
			name: "autocommit_and_prepared_stmts",
			cfg: db.Config{
				Host: "vertica.internal",
				Port: 5433,
				User: "dbadmin",
				Options: map[string]string{
					"autocommit":         "false",
					"use_prepared_stmts": "1",
					"binary_parameters":  "true",
				},
			},
			wantParams: map[string]string{
				"autocommit":         "false",
				"use_prepared_stmts": "1",
				"binary_parameters":  "true",
			},
		},
		{
			name: "kerberos_and_oauth",
			cfg: db.Config{
				Host: "vertica.internal",
				Port: 5433,
				User: "dbadmin",
				Options: map[string]string{
					"kerberos_service_name": "vertica",
					"kerberos_host":         "kdc.example",
					"oauth_access_token":    "eyJhbGciOi...",
				},
			},
			wantParams: map[string]string{
				"kerberos_service_name": "vertica",
				"kerberos_host":         "kdc.example",
				"oauth_access_token":    "eyJhbGciOi...",
			},
		},
		{
			name: "empty_option_value_absent",
			cfg: db.Config{
				Host: "vertica.internal",
				Port: 5433,
				User: "dbadmin",
				Options: map[string]string{
					"tlsmode":      "",
					"client_label": "sqlgo",
				},
			},
			wantParams:   map[string]string{"client_label": "sqlgo"},
			absentParams: []string{"tlsmode"},
		},
		{
			name: "passthrough_unknown_option",
			cfg: db.Config{
				Host: "vertica.internal",
				Port: 5433,
				User: "dbadmin",
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
			if tc.wantPath != "" && u.Path != tc.wantPath {
				t.Errorf("path = %q, want %q", u.Path, tc.wantPath)
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

// TestPreset_Capabilities pins the capability fingerprint so drift in
// Profile -> preset wiring surfaces as a test failure.
func TestPreset_Capabilities(t *testing.T) {
	t.Parallel()
	got := preset{}.Capabilities()
	if got.IdentifierQuote != '"' {
		t.Errorf("IdentifierQuote = %q, want '\"'", got.IdentifierQuote)
	}
	if !got.SupportsTransactions {
		t.Error("SupportsTransactions should be true")
	}
	if got.Dialect != sqltok.DialectVertica {
		t.Errorf("Dialect = %v, want DialectVertica", got.Dialect)
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
		t.Error("SupportsTLS should be true")
	}
	if !got.SupportsCancel {
		t.Error("SupportsCancel should be true")
	}
	if got.SupportsCrossDatabase {
		t.Error("SupportsCrossDatabase should be false (Vertica pins one DB per connection)")
	}
}

// generateTestCertKey produces an ephemeral ECDSA-P256 self-signed
// cert + key PEM pair. We synthesize per-test rather than using a
// frozen blob because tls.LoadX509KeyPair rejects hand-authored PEM
// on some platforms with "x509: malformed algorithm identifier".
func generateTestCertKey(t *testing.T) ([]byte, []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "sqlgo-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		IsCA:         true,
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

// TestBuildCustomTLSConfig_Nil: no TLS options -> nil config, no
// registration side effect. This is the fast path every existing
// Vertica user hits today.
func TestBuildCustomTLSConfig_Nil(t *testing.T) {
	t.Parallel()
	cfg, err := buildCustomTLSConfig(nil)
	if err != nil {
		t.Fatalf("nil opts: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil, got %+v", cfg)
	}
	cfg, err = buildCustomTLSConfig(map[string]string{"unrelated": "x"})
	if err != nil {
		t.Fatalf("unrelated opts: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil for unrelated opts, got %+v", cfg)
	}
}

// TestBuildCustomTLSConfig_Mismatch: cert without key (and vice
// versa) is rejected early -- LoadX509KeyPair would surface a
// confusing file error otherwise.
func TestBuildCustomTLSConfig_Mismatch(t *testing.T) {
	t.Parallel()
	if _, err := buildCustomTLSConfig(map[string]string{"tls_cert_file": "/tmp/c.pem"}); err == nil {
		t.Error("expected error for cert without key")
	}
	if _, err := buildCustomTLSConfig(map[string]string{"tls_key_file": "/tmp/k.pem"}); err == nil {
		t.Error("expected error for key without cert")
	}
}

// TestBuildCustomTLSConfig_MTLS: full happy path -- cert+key+CA
// files on disk produce a *tls.Config with Certificates, RootCAs,
// ServerName, and InsecureSkipVerify populated.
func TestBuildCustomTLSConfig_MTLS(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	certPEM, keyPEM := generateTestCertKey(t)
	certPath := filepath.Join(dir, "client.pem")
	keyPath := filepath.Join(dir, "client.key")
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	// Reuse the client cert as the CA bundle -- it's self-signed so
	// it parses fine as a root; the test only cares that AppendCerts-
	// FromPEM accepts it.
	if err := os.WriteFile(caPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := buildCustomTLSConfig(map[string]string{
		"tls_cert_file":            certPath,
		"tls_key_file":             keyPath,
		"tls_ca_file":              caPath,
		"tls_server_name":          "vertica.internal",
		"tls_insecure_skip_verify": "true",
	})
	if err != nil {
		t.Fatalf("buildCustomTLSConfig: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if len(cfg.Certificates) != 1 {
		t.Errorf("Certificates = %d, want 1", len(cfg.Certificates))
	}
	if cfg.RootCAs == nil {
		t.Error("RootCAs is nil")
	}
	if cfg.ServerName != "vertica.internal" {
		t.Errorf("ServerName = %q", cfg.ServerName)
	}
	if !cfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be true")
	}
}

// TestBuildCustomTLSConfig_BadCert: LoadX509KeyPair error bubbles up
// with a sqlgo-prefixed message so operators can tell which driver
// is complaining.
func TestBuildCustomTLSConfig_BadCert(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(bad, []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := buildCustomTLSConfig(map[string]string{
		"tls_cert_file": bad,
		"tls_key_file":  bad,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "load vertica client cert") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestBuildDSNWithTLS_CustomRegistersName: end-to-end through the
// Open path. DSN must carry tlsmode=<registered name> so the driver
// can look up the config; base tlsmode (e.g. "server") is overridden.
func TestBuildDSNWithTLS_CustomRegistersName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	certPEM, keyPEM := generateTestCertKey(t)
	certPath := filepath.Join(dir, "c.pem")
	keyPath := filepath.Join(dir, "c.key")
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := db.Config{
		Host: "vertica.internal", Port: 5433, User: "dbadmin", Database: "VMart",
		Options: map[string]string{
			"tlsmode":       "server", // should be overridden
			"tls_cert_file": certPath,
			"tls_key_file":  keyPath,
		},
	}
	dsn, err := buildDSNWithTLS(cfg)
	if err != nil {
		t.Fatalf("buildDSNWithTLS: %v", err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	got := u.Query().Get("tlsmode")
	if !strings.HasPrefix(got, "sqlgo-") {
		t.Errorf("tlsmode = %q, want sqlgo-<hash>", got)
	}
	if got == "server" {
		t.Error("base tlsmode was not overridden")
	}
	// Second call with the same inputs is idempotent -- tlsConfigName
	// hashes options, and RegisterTLSConfig overwrites entries.
	dsn2, err := buildDSNWithTLS(cfg)
	if err != nil {
		t.Fatalf("buildDSNWithTLS 2: %v", err)
	}
	u2, _ := url.Parse(dsn2)
	if u2.Query().Get("tlsmode") != got {
		t.Errorf("second call name = %q, want %q", u2.Query().Get("tlsmode"), got)
	}
}

// TestBuildDSNWithTLS_NoCustomPassthrough: without any sqlgo-native
// TLS options, the base DSN flows through unchanged including any
// user-set tlsmode.
func TestBuildDSNWithTLS_NoCustomPassthrough(t *testing.T) {
	t.Parallel()
	cfg := db.Config{
		Host: "vertica.internal", Port: 5433, User: "dbadmin", Database: "VMart",
		Options: map[string]string{"tlsmode": "server-strict"},
	}
	dsn, err := buildDSNWithTLS(cfg)
	if err != nil {
		t.Fatalf("buildDSNWithTLS: %v", err)
	}
	if dsn != buildDSN(cfg) {
		t.Errorf("DSN changed without TLS options\n got=%s\nwant=%s", dsn, buildDSN(cfg))
	}
}

// TestQuoteIdent: ANSI double-quote with embedded-quote doubling.
func TestQuoteIdent(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
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
