package hana

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
	"testing"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

// TestBuildDSN covers host/port defaults, userinfo encoding,
// Database -> databaseName tenant mapping, semantic Options
// translation, and raw passthrough. An empty want value means
// "key must NOT be present".
func TestBuildDSN(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		cfg          db.Config
		wantScheme   string
		wantHost     string
		wantUser     string
		wantPassword string
		wantParams   map[string]string
		absentParams []string
	}{
		{
			name: "explicit_host_port_user_db",
			cfg: db.Config{
				Host:     "hana.internal",
				Port:     39017,
				User:     "SYSTEM",
				Password: "Hxe12345",
				Database: "HXE",
			},
			wantScheme:   "hdb",
			wantHost:     "hana.internal:39017",
			wantUser:     "SYSTEM",
			wantPassword: "Hxe12345",
			wantParams:   map[string]string{"databaseName": "HXE"},
		},
		{
			name:       "default_host_and_port",
			cfg:        db.Config{User: "SYSTEM"},
			wantScheme: "hdb",
			wantHost:   "localhost:39017",
			wantUser:   "SYSTEM",
		},
		{
			name: "semantic_default_schema_and_locale",
			cfg: db.Config{
				Host: "hana.internal",
				Port: 39017,
				User: "SYSTEM",
				Options: map[string]string{
					"default_schema": "SQLGO",
					"locale":         "en_US",
				},
			},
			wantParams: map[string]string{
				"defaultSchema": "SQLGO",
				"locale":        "en_US",
			},
			absentParams: []string{"default_schema"},
		},
		{
			name: "tls_options_mapping",
			cfg: db.Config{
				Host: "hana.internal",
				Port: 39017,
				Options: map[string]string{
					"tls_server_name":          "hana.example.com",
					"tls_insecure_skip_verify": "true",
					"tls_root_ca_file":         "/etc/ssl/hana-ca.pem",
				},
			},
			wantParams: map[string]string{
				"TLSServerName":         "hana.example.com",
				"TLSInsecureSkipVerify": "true",
				"TLSRootCAFile":         "/etc/ssl/hana-ca.pem",
			},
			absentParams: []string{
				"tls_server_name", "tls_insecure_skip_verify", "tls_root_ca_file",
			},
		},
		{
			name: "tuning_options_camelcase",
			cfg: db.Config{
				Host: "hana.internal",
				Options: map[string]string{
					"ping_interval":  "30s",
					"timeout":        "10s",
					"fetch_size":     "1024",
					"bulk_size":      "128",
					"lob_chunk_size": "4096",
					"dfv":            "8",
				},
			},
			wantParams: map[string]string{
				"pingInterval": "30s",
				"timeout":      "10s",
				"fetchSize":    "1024",
				"bulkSize":     "128",
				"lobChunkSize": "4096",
				"dfv":          "8",
			},
			absentParams: []string{
				"ping_interval", "fetch_size", "bulk_size", "lob_chunk_size",
			},
		},
		{
			name: "empty_option_value_absent",
			cfg: db.Config{
				Host: "hana.internal",
				Options: map[string]string{
					"default_schema": "",
					"locale":         "en_US",
				},
			},
			wantParams:   map[string]string{"locale": "en_US"},
			absentParams: []string{"defaultSchema", "default_schema"},
		},
		{
			name: "passthrough_unknown_option",
			cfg: db.Config{
				Host: "hana.internal",
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
	if !got.SupportsTransactions {
		t.Error("SupportsTransactions should be true (HANA supports ACID transactions)")
	}
	if got.Dialect != sqltok.DialectHANA {
		t.Errorf("Dialect = %v, want DialectHANA", got.Dialect)
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
		t.Error("SupportsTLS should be true (HANA uses TLS by default)")
	}
	if !got.SupportsCancel {
		t.Error("SupportsCancel should be true")
	}
	if got.SupportsCrossDatabase {
		t.Error("SupportsCrossDatabase should be false (connection pinned to one tenant DB)")
	}
}

// TestAuthMode pins the normalization table: anything outside jwt /
// x509 aliases maps back to "basic" so existing configs keep working.
func TestAuthMode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"", "basic"},
		{"basic", "basic"},
		{"BASIC", "basic"},
		{"unknown", "basic"},
		{"jwt", "jwt"},
		{"JWT", "jwt"},
		{" jwt ", "jwt"},
		{"x509", "x509"},
		{"X509", "x509"},
		{"cert", "x509"},
		{"client_cert", "x509"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := authMode(map[string]string{"auth_method": tc.in}); got != tc.want {
				t.Errorf("authMode(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestResolveJWTToken covers the three token sources in priority
// order: inline option, token file, password fallback. Trailing
// whitespace (the newline most token files end on) must be stripped.
func TestResolveJWTToken(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	tokenPath := filepath.Join(tmp, "token")
	if err := os.WriteFile(tokenPath, []byte("ey-token-from-file\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	emptyPath := filepath.Join(tmp, "empty")
	if err := os.WriteFile(emptyPath, []byte("   \n"), 0o600); err != nil {
		t.Fatalf("write empty token file: %v", err)
	}

	cases := []struct {
		name     string
		opts     map[string]string
		password string
		want     string
		wantErr  bool
	}{
		{
			name: "inline_beats_file_beats_password",
			opts: map[string]string{
				"jwt_token":      "ey-inline",
				"jwt_token_file": tokenPath,
			},
			password: "ey-password",
			want:     "ey-inline",
		},
		{
			name:     "file_beats_password",
			opts:     map[string]string{"jwt_token_file": tokenPath},
			password: "ey-password",
			want:     "ey-token-from-file",
		},
		{
			name:     "password_fallback",
			opts:     map[string]string{},
			password: "ey-password",
			want:     "ey-password",
		},
		{
			name:    "empty_file_errors",
			opts:    map[string]string{"jwt_token_file": emptyPath},
			wantErr: true,
		},
		{
			name:    "missing_file_errors",
			opts:    map[string]string{"jwt_token_file": filepath.Join(tmp, "no-such-file")},
			wantErr: true,
		},
		{
			name:    "nothing_set_errors",
			opts:    map[string]string{},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveJWTToken(tc.opts, tc.password)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("token = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBuildConnectorJWT exercises the JWT Connector branch and checks
// the fields we care about (host, tenant DB, token) survive into the
// go-hdb Connector. Exercises the actual library path so a go-hdb
// API rename would surface here immediately.
func TestBuildConnectorJWT(t *testing.T) {
	t.Parallel()
	cfg := db.Config{
		Host:     "hana.internal",
		Port:     39017,
		User:     "",
		Password: "ey-password-fallback",
		Database: "HXE",
		Options: map[string]string{
			"auth_method":    "jwt",
			"default_schema": "SQLGO",
		},
	}
	c, err := buildConnector("jwt", cfg)
	if err != nil {
		t.Fatalf("buildConnector: %v", err)
	}
	if got := c.Host(); got != "hana.internal:39017" {
		t.Errorf("Host = %q, want hana.internal:39017", got)
	}
	if got := c.DatabaseName(); got != "HXE" {
		t.Errorf("DatabaseName = %q, want HXE", got)
	}
	if got := c.Token(); got != "ey-password-fallback" {
		t.Errorf("Token = %q, want password fallback", got)
	}
	if got := c.DefaultSchema(); got != "SQLGO" {
		t.Errorf("DefaultSchema = %q, want SQLGO", got)
	}
}

// TestBuildConnectorX509 covers the X.509 client-cert branch. A dummy
// PEM pair is enough for the go-hdb loader to accept the files; we
// aren't doing a TLS handshake here, just confirming the Connector is
// wired with cert + key paths and the tenant DB flows through.
func TestBuildConnectorX509(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	certPath := filepath.Join(tmp, "client.crt")
	keyPath := filepath.Join(tmp, "client.key")
	// Generate a fresh self-signed ECDSA pair at test time. go-hdb's
	// NewX509AuthConnectorByFiles runs tls.LoadX509KeyPair under the
	// hood which rejects hand-crafted PEM bytes; generating an actual
	// ECDSA-P256 cert keeps the test hermetic while staying valid.
	certPEM, keyPEM := generateTestCertKey(t)
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	cfg := db.Config{
		Host:     "hana.internal",
		Port:     39017,
		Database: "HXE",
		Options: map[string]string{
			"auth_method":      "x509",
			"client_cert_file": certPath,
			"client_key_file":  keyPath,
		},
	}
	c, err := buildConnector("x509", cfg)
	if err != nil {
		t.Fatalf("buildConnector: %v", err)
	}
	if got := c.Host(); got != "hana.internal:39017" {
		t.Errorf("Host = %q, want hana.internal:39017", got)
	}
	if got := c.DatabaseName(); got != "HXE" {
		t.Errorf("DatabaseName = %q, want HXE", got)
	}
	gotCert, gotKey := c.ClientCert()
	if len(gotCert) == 0 || len(gotKey) == 0 {
		t.Errorf("ClientCert returned empty pair")
	}
}

// TestBuildConnectorX509_MissingFiles confirms the helper surfaces a
// clear error when either cert or key path is blank -- the UI has no
// way to know this without asking go-hdb, and go-hdb's error at that
// layer is less specific.
func TestBuildConnectorX509_MissingFiles(t *testing.T) {
	t.Parallel()
	_, err := buildConnector("x509", db.Config{
		Host: "h",
		Port: 39017,
		Options: map[string]string{
			"auth_method":      "x509",
			"client_cert_file": "/tmp/x.crt",
			// key missing
		},
	})
	if err == nil {
		t.Fatal("want error for missing client_key_file")
	}
}

// generateTestCertKey returns a self-signed ECDSA-P256 cert + key pair
// suitable for tls.LoadX509KeyPair. Generated per-test so we don't have
// to check an expiring cert into the repo.
func generateTestCertKey(t *testing.T) ([]byte, []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa generate: %v", err)
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
		t.Fatalf("create certificate: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal ec key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

// TestQuoteIdent ensures double-quote escaping matches ANSI/HANA:
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
