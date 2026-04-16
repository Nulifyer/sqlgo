package flightsql

import (
	"testing"

	"github.com/apache/arrow-go/v18/arrow"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

func TestCapabilities(t *testing.T) {
	t.Parallel()
	d := driver{}
	if d.Name() != "flightsql" {
		t.Fatalf("Name = %q, want flightsql", d.Name())
	}
	cap := d.Capabilities()
	tests := []struct {
		name string
		got  any
		want any
	}{
		{"SchemaDepth", cap.SchemaDepth, db.SchemaDepthSchemas},
		{"LimitSyntax", cap.LimitSyntax, db.LimitSyntaxLimit},
		{"IdentifierQuote", cap.IdentifierQuote, '"'},
		{"SupportsCancel", cap.SupportsCancel, true},
		{"SupportsTLS", cap.SupportsTLS, true},
		{"ExplainFormat", cap.ExplainFormat, db.ExplainFormatNone},
		{"SupportsTransactions", cap.SupportsTransactions, false},
		{"Dialect", cap.Dialect, sqltok.DialectAll},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
		}
	}
}

func TestQuoteIdent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want string
	}{
		{"foo", `"foo"`},
		{`has"quote`, `"has""quote"`},
		{"", `""`},
		{`a""b`, `"a""""b"`},
	}
	for _, tt := range tests {
		got := quoteIdent(tt.in)
		if got != tt.want {
			t.Errorf("quoteIdent(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestBoolish(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"1", "t", "true", "True", "TRUE", "y", "yes", "Yes", "on", "ON"} {
		if !boolish(s) {
			t.Errorf("boolish(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "0", "f", "false", "no", "off", "random"} {
		if boolish(s) {
			t.Errorf("boolish(%q) = true, want false", s)
		}
	}
}

func TestBuildDialOpts_Insecure(t *testing.T) {
	t.Parallel()
	opts, err := buildDialOpts(nil)
	if err != nil {
		t.Fatalf("buildDialOpts(nil): %v", err)
	}
	if len(opts) != 1 {
		t.Fatalf("len(opts) = %d, want 1", len(opts))
	}
}

func TestBuildDialOpts_Secure(t *testing.T) {
	t.Parallel()
	opts, err := buildDialOpts(map[string]string{"secure": "true"})
	if err != nil {
		t.Fatalf("buildDialOpts secure: %v", err)
	}
	if len(opts) != 1 {
		t.Fatalf("len(opts) = %d, want 1", len(opts))
	}
}

func TestBuildDialOpts_BadCA(t *testing.T) {
	t.Parallel()
	_, err := buildDialOpts(map[string]string{
		"secure":      "true",
		"tls_ca_file": "/nonexistent/ca.crt",
	})
	if err == nil {
		t.Fatal("expected error for missing CA file")
	}
}

func TestTlsConfigFromOptions(t *testing.T) {
	t.Parallel()
	tc, err := tlsConfigFromOptions(map[string]string{
		"tls_server_name":          "myhost",
		"tls_insecure_skip_verify": "true",
	})
	if err != nil {
		t.Fatalf("tlsConfigFromOptions: %v", err)
	}
	if tc.ServerName != "myhost" {
		t.Errorf("ServerName = %q, want myhost", tc.ServerName)
	}
	if !tc.InsecureSkipVerify {
		t.Error("InsecureSkipVerify = false, want true")
	}
}

func TestFieldIndex(t *testing.T) {
	t.Parallel()
	// Build a minimal arrow schema to test fieldIndex.
	schema := arrowSchemaWith("catalog_name", "db_schema_name", "table_name", "table_type")
	tests := []struct {
		name string
		want int
	}{
		{"catalog_name", 0},
		{"table_type", 3},
		{"nonexistent", -1},
	}
	for _, tt := range tests {
		got := fieldIndex(schema, tt.name)
		if got != tt.want {
			t.Errorf("fieldIndex(%q) = %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestFieldsToColumns(t *testing.T) {
	t.Parallel()
	schema := arrowSchemaWith("id", "name", "val")
	cols := fieldsToColumns(schema.Fields())
	if len(cols) != 3 {
		t.Fatalf("len = %d, want 3", len(cols))
	}
	for i, want := range []string{"id", "name", "val"} {
		if cols[i].Name != want {
			t.Errorf("cols[%d].Name = %q, want %q", i, cols[i].Name, want)
		}
	}
}

// arrowSchemaWith builds a trivial utf8 schema for testing helpers.
func arrowSchemaWith(names ...string) *arrow.Schema {
	fields := make([]arrow.Field, len(names))
	for i, n := range names {
		fields[i] = arrow.Field{Name: n, Type: arrow.BinaryTypes.String}
	}
	return arrow.NewSchema(fields, nil)
}
