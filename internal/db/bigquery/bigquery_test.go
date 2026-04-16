package bigquery

import (
	"testing"
	"time"

	"cloud.google.com/go/bigquery"
	"google.golang.org/api/option"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

// TestDriver_Capabilities pins the capability fingerprint so drift in
// the driver's advertised behaviour surfaces as a test failure. The
// exact values here matter because they drive TUI branching.
func TestDriver_Capabilities(t *testing.T) {
	t.Parallel()
	got := driver{}.Capabilities()
	if got.IdentifierQuote != '`' {
		t.Errorf("IdentifierQuote = %q, want '`' (GoogleSQL grammar)", got.IdentifierQuote)
	}
	if got.SupportsTransactions {
		t.Error("SupportsTransactions should be false (BigQuery txns are script-only)")
	}
	if got.Dialect != sqltok.DialectBigQuery {
		t.Errorf("Dialect = %v, want DialectBigQuery", got.Dialect)
	}
	if got.SchemaDepth != db.SchemaDepthSchemas {
		t.Errorf("SchemaDepth = %v, want SchemaDepthSchemas (datasets = schemas)", got.SchemaDepth)
	}
	if got.LimitSyntax != db.LimitSyntaxLimit {
		t.Errorf("LimitSyntax = %v, want LimitSyntaxLimit", got.LimitSyntax)
	}
	if got.ExplainFormat != db.ExplainFormatNone {
		t.Errorf("ExplainFormat = %v, want ExplainFormatNone (no plan fetch)", got.ExplainFormat)
	}
	if !got.SupportsTLS {
		t.Error("SupportsTLS should be true (REST over HTTPS)")
	}
	if !got.SupportsCancel {
		t.Error("SupportsCancel should be true")
	}
	if got.SupportsCrossDatabase {
		t.Error("SupportsCrossDatabase should be false (conn pinned to one project)")
	}
}

// TestBuildClientOptions verifies the options mapping: endpoint from
// Host+Port, disable_auth implied by emulator endpoint, credentials
// files/JSON passed through when explicit. The returned slice is typed
// as []option.ClientOption but the library's constructors don't export
// their internals, so we assert by length + pattern rather than deep
// content -- length drift catches regressions cheaply.
func TestBuildClientOptions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		cfg          db.Config
		wantLen      int
		wantImplicit bool // emulator implies WithoutAuthentication
	}{
		{
			name: "production_no_options",
			cfg: db.Config{
				Options: map[string]string{"project": "p"},
			},
			wantLen: 0,
		},
		{
			name: "emulator_host_port",
			cfg: db.Config{
				Host:    "localhost",
				Port:    19050,
				Options: map[string]string{"project": "p"},
			},
			// WithEndpoint + WithoutAuthentication (implicit).
			wantLen:      2,
			wantImplicit: true,
		},
		{
			name: "explicit_endpoint_with_creds",
			cfg: db.Config{
				Options: map[string]string{
					"project":     "p",
					"endpoint":    "http://bq-proxy:9050",
					"credentials": "/etc/sa.json",
				},
			},
			// WithEndpoint + WithCredentialsFile. Auth NOT skipped because
			// user supplied credentials explicitly.
			wantLen: 2,
		},
		{
			name: "credentials_json_inline",
			cfg: db.Config{
				Options: map[string]string{
					"project":          "p",
					"credentials_json": `{"type":"service_account"}`,
				},
			},
			wantLen: 1,
		},
		{
			name: "explicit_disable_auth",
			cfg: db.Config{
				Options: map[string]string{
					"project":      "p",
					"disable_auth": "true",
				},
			},
			wantLen: 1,
		},
		{
			// Short-lived OAuth bearer token injected via StaticTokenSource.
			// The emulator-implicit WithoutAuthentication branch must NOT
			// trigger here even though the other creds options are empty --
			// the access token itself counts as explicit credentials.
			name: "oauth_access_token",
			cfg: db.Config{
				Options: map[string]string{
					"project":      "p",
					"access_token": "ya29.a0AfH6SMC-token",
				},
			},
			wantLen: 1,
		},
		{
			// access_token alongside an explicit emulator endpoint still
			// produces exactly endpoint + token-source. disable_auth must
			// stay off because the bearer is real credentials.
			name: "oauth_access_token_with_endpoint",
			cfg: db.Config{
				Host: "localhost",
				Port: 19050,
				Options: map[string]string{
					"project":      "p",
					"access_token": "ya29.a0AfH6SMC-token",
				},
			},
			wantLen: 2,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts, err := buildClientOptions(tc.cfg)
			if err != nil {
				t.Fatalf("buildClientOptions: %v", err)
			}
			if len(opts) != tc.wantLen {
				t.Errorf("len(opts) = %d, want %d (%+v)", len(opts), tc.wantLen, opts)
			}
			// Sanity-check that option.ClientOption is actually what we
			// return -- this keeps the type wiring from silently drifting
			// to *option.ClientOption or similar.
			var _ []option.ClientOption = opts
		})
	}
}

// TestConvertValue covers the BigQuery-native types the Go client hands
// back in []bigquery.Value row slices: nested ARRAY, nested RECORD/
// STRUCT, time.Time, and simple scalar passthrough. Everything else
// falls through to the default branch and stays as-is.
func TestConvertValue(t *testing.T) {
	t.Parallel()

	if got := convertValue(nil); got != nil {
		t.Errorf("nil: got %v, want nil", got)
	}

	if got := convertValue(int64(42)); got != int64(42) {
		t.Errorf("int64: got %v (%T)", got, got)
	}
	if got := convertValue("hi"); got != "hi" {
		t.Errorf("string: got %v", got)
	}

	ts := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	if got := convertValue(ts); !got.(time.Time).Equal(ts) {
		t.Errorf("time: got %v, want %v", got, ts)
	}

	// ARRAY<INT64>
	arr := []bigquery.Value{int64(1), int64(2), int64(3)}
	got := convertValue(arr).([]any)
	if len(got) != 3 || got[0] != int64(1) || got[2] != int64(3) {
		t.Errorf("array: got %v", got)
	}

	// STRUCT<a INT64, b STRING>
	rec := map[string]bigquery.Value{"a": int64(7), "b": "hello"}
	gotMap := convertValue(rec).(map[string]any)
	if gotMap["a"] != int64(7) || gotMap["b"] != "hello" {
		t.Errorf("struct: got %v", gotMap)
	}

	// Nested: ARRAY<STRUCT<k INT64>>
	nested := []bigquery.Value{
		map[string]bigquery.Value{"k": int64(1)},
		map[string]bigquery.Value{"k": int64(2)},
	}
	gotNested := convertValue(nested).([]any)
	if len(gotNested) != 2 {
		t.Fatalf("nested len: got %d", len(gotNested))
	}
	first := gotNested[0].(map[string]any)
	if first["k"] != int64(1) {
		t.Errorf("nested[0].k: got %v", first["k"])
	}
}

// TestFieldTypeName checks the repeated/array rendering and the
// simple-type passthrough. BigQuery's FieldType is a string-typed enum,
// and we surface it as-is for non-array fields.
func TestFieldTypeName(t *testing.T) {
	t.Parallel()
	scalar := &bigquery.FieldSchema{Name: "c", Type: bigquery.StringFieldType}
	if got := fieldTypeName(scalar); got != "STRING" {
		t.Errorf("scalar: got %q", got)
	}
	repeated := &bigquery.FieldSchema{Name: "c", Type: bigquery.IntegerFieldType, Repeated: true}
	if got := fieldTypeName(repeated); got != "ARRAY<INTEGER>" {
		t.Errorf("repeated: got %q", got)
	}
}

// TestBoolish covers the truthy spellings accepted for toggle-style
// options. Non-matching strings must stay false so blank defaults
// don't accidentally enable auth skipping.
func TestBoolish(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"1", "t", "true", "TRUE", "y", "yes", "on", " true "} {
		if !boolish(v) {
			t.Errorf("boolish(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "0", "f", "false", "no", "off", "maybe"} {
		if boolish(v) {
			t.Errorf("boolish(%q) = true, want false", v)
		}
	}
}

// TestFirstNonEmpty confirms the tie-breaking order used by Open to
// pick project from project/project_id aliases and dataset from
// Database/Options["dataset"].
func TestFirstNonEmpty(t *testing.T) {
	t.Parallel()
	if got := firstNonEmpty("", " ", "", "third"); got != "third" {
		t.Errorf("got %q", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("empty: got %q", got)
	}
}
