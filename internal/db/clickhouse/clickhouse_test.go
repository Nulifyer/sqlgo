package clickhouse

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

// TestBuildDSN covers host/port defaults, user/password encoding,
// database path mapping, and arbitrary Options passthrough. An empty
// wanted value means "key must NOT be present".
func TestBuildDSN(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		cfg          db.Config
		wantHost     string
		wantPath     string
		wantUser     string
		wantPassword string
		wantParams   map[string]string
		absentParams []string
	}{
		{
			name: "explicit_host_port_db",
			cfg: db.Config{
				Host:     "ch.internal",
				Port:     9440,
				User:     "analyst",
				Password: "s3cret",
				Database: "events",
			},
			wantHost:     "ch.internal:9440",
			wantPath:     "/events",
			wantUser:     "analyst",
			wantPassword: "s3cret",
		},
		{
			name:     "default_host_and_port",
			cfg:      db.Config{},
			wantHost: "localhost:9000",
		},
		{
			name: "options_passthrough_secure_compress",
			cfg: db.Config{
				Host: "ch.internal",
				Port: 9440,
				User: "default",
				Options: map[string]string{
					"secure":   "true",
					"compress": "lz4",
				},
			},
			wantHost: "ch.internal:9440",
			wantUser: "default",
			wantParams: map[string]string{
				"secure":   "true",
				"compress": "lz4",
			},
		},
		{
			name: "no_database_no_path",
			cfg: db.Config{
				Host: "ch.internal",
				Port: 9000,
			},
			wantHost: "ch.internal:9000",
			wantPath: "",
		},
		{
			name: "dial_timeout_option",
			cfg: db.Config{
				Host: "ch.internal",
				Port: 9000,
				Options: map[string]string{
					"dial_timeout": "5s",
				},
			},
			wantParams: map[string]string{"dial_timeout": "5s"},
		},
		{
			// The sqlgo-native TLS option keys must be stripped from the
			// DSN -- clickhouse-go/v2 routes unknown query params into
			// server-side SETTINGS and would reject them on handshake.
			// openClickHouse consumes them separately into *tls.Config.
			name: "tls_options_stripped_from_dsn",
			cfg: db.Config{
				Host: "ch.internal",
				Port: 9440,
				Options: map[string]string{
					"secure":                   "true",
					"tls_cert_file":            "/etc/sqlgo/client.crt",
					"tls_key_file":             "/etc/sqlgo/client.key",
					"tls_ca_file":              "/etc/sqlgo/ca.crt",
					"tls_server_name":          "ch.example.com",
					"tls_insecure_skip_verify": "true",
				},
			},
			wantHost:   "ch.internal:9440",
			wantParams: map[string]string{"secure": "true"},
			absentParams: []string{
				"tls_cert_file",
				"tls_key_file",
				"tls_ca_file",
				"tls_server_name",
				"tls_insecure_skip_verify",
			},
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
			if u.Scheme != "clickhouse" {
				t.Errorf("scheme = %q, want clickhouse", u.Scheme)
			}
			if tc.wantHost != "" && u.Host != tc.wantHost {
				t.Errorf("host = %q, want %q", u.Host, tc.wantHost)
			}
			if u.Path != tc.wantPath {
				t.Errorf("path = %q, want %q", u.Path, tc.wantPath)
			}
			if tc.wantUser != "" && u.User.Username() != tc.wantUser {
				t.Errorf("user = %q, want %q", u.User.Username(), tc.wantUser)
			}
			if tc.wantPassword != "" {
				pw, _ := u.User.Password()
				if pw != tc.wantPassword {
					t.Errorf("password = %q, want %q", pw, tc.wantPassword)
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
	if got.IdentifierQuote != '`' {
		t.Errorf("IdentifierQuote = %q, want '`'", got.IdentifierQuote)
	}
	if got.SupportsTransactions {
		t.Error("SupportsTransactions should be false (ClickHouse is OLAP)")
	}
	if got.Dialect != sqltok.DialectClickhouse {
		t.Errorf("Dialect = %v, want DialectClickhouse", got.Dialect)
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
		t.Error("SupportsTLS should be true (ClickHouse supports TLS on native port)")
	}
	if !got.SupportsCancel {
		t.Error("SupportsCancel should be true")
	}
}

// TestBuildCustomTLSConfig covers the option-to-*tls.Config assembly.
// Empty options return (nil, nil) so the fast DSN path stays live.
// Cert/key must be supplied together. Valid PEM files produce a fully
// populated *tls.Config with Certificates, RootCAs, ServerName and
// InsecureSkipVerify set as requested.
func TestBuildCustomTLSConfig(t *testing.T) {
	t.Parallel()

	t.Run("empty_returns_nil", func(t *testing.T) {
		t.Parallel()
		cfg, err := buildCustomTLSConfig(map[string]string{})
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if cfg != nil {
			t.Errorf("cfg = %+v, want nil", cfg)
		}
	})

	t.Run("cert_without_key_errors", func(t *testing.T) {
		t.Parallel()
		_, err := buildCustomTLSConfig(map[string]string{"tls_cert_file": "/a.crt"})
		if err == nil {
			t.Fatal("want error, got nil")
		}
	})

	t.Run("key_without_cert_errors", func(t *testing.T) {
		t.Parallel()
		_, err := buildCustomTLSConfig(map[string]string{"tls_key_file": "/a.key"})
		if err == nil {
			t.Fatal("want error, got nil")
		}
	})

	t.Run("server_name_only", func(t *testing.T) {
		t.Parallel()
		cfg, err := buildCustomTLSConfig(map[string]string{"tls_server_name": "ch.example.com"})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if cfg == nil || cfg.ServerName != "ch.example.com" {
			t.Errorf("cfg = %+v, want ServerName=ch.example.com", cfg)
		}
	})

	t.Run("skip_verify_only", func(t *testing.T) {
		t.Parallel()
		cfg, err := buildCustomTLSConfig(map[string]string{"tls_insecure_skip_verify": "true"})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if cfg == nil || !cfg.InsecureSkipVerify {
			t.Errorf("cfg = %+v, want InsecureSkipVerify=true", cfg)
		}
	})

	t.Run("full_combo_with_pem_files", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		certPEM, keyPEM := genTestKeyPair(t)
		caPEM, _ := genTestKeyPair(t)

		certPath := filepath.Join(dir, "client.crt")
		keyPath := filepath.Join(dir, "client.key")
		caPath := filepath.Join(dir, "ca.crt")
		if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
			t.Fatalf("write cert: %v", err)
		}
		if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
			t.Fatalf("write key: %v", err)
		}
		if err := os.WriteFile(caPath, caPEM, 0600); err != nil {
			t.Fatalf("write ca: %v", err)
		}

		cfg, err := buildCustomTLSConfig(map[string]string{
			"tls_cert_file":            certPath,
			"tls_key_file":             keyPath,
			"tls_ca_file":              caPath,
			"tls_server_name":          "ch.example.com",
			"tls_insecure_skip_verify": "true",
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if cfg == nil {
			t.Fatal("cfg = nil")
		}
		if len(cfg.Certificates) != 1 {
			t.Errorf("Certificates len = %d, want 1", len(cfg.Certificates))
		}
		if cfg.RootCAs == nil {
			t.Error("RootCAs = nil, want populated pool")
		}
		if cfg.ServerName != "ch.example.com" {
			t.Errorf("ServerName = %q, want ch.example.com", cfg.ServerName)
		}
		if !cfg.InsecureSkipVerify {
			t.Error("InsecureSkipVerify = false, want true")
		}
	})

	t.Run("ca_file_with_no_certs_errors", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		caPath := filepath.Join(dir, "empty.crt")
		if err := os.WriteFile(caPath, []byte("not a pem block\n"), 0600); err != nil {
			t.Fatalf("write ca: %v", err)
		}
		_, err := buildCustomTLSConfig(map[string]string{"tls_ca_file": caPath})
		if err == nil {
			t.Fatal("want error for empty CA file, got nil")
		}
	})
}

// genTestKeyPair produces a minimal self-signed ECDSA P-256 certificate
// + private key in PEM form. The cert is only used to prove that
// tls.LoadX509KeyPair and x509.CertPool.AppendCertsFromPEM accept the
// bytes -- it is never presented to a real server.
func genTestKeyPair(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "sqlgo-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

// TestQuoteIdent ensures backtick escaping matches what ClickHouse
// expects: wrap in backticks, double any embedded ones.
func TestQuoteIdent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"events", "`events`"},
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
