package d1

import (
	"encoding/json"
	"testing"
)

func TestDecodeRowOrdered(t *testing.T) {
	t.Parallel()
	blob := json.RawMessage(`{"id":1,"name":"alice","email":"a@x"}`)
	cols, vals, err := decodeRowOrdered(blob)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"id", "name", "email"}
	if len(cols) != len(want) {
		t.Fatalf("len: got %d want %d", len(cols), len(want))
	}
	for i, c := range cols {
		if c.Name != want[i] {
			t.Fatalf("col[%d]: got %q want %q", i, c.Name, want[i])
		}
	}
	if n, ok := vals[0].(int64); !ok || n != 1 {
		t.Fatalf("val[0]: got %v (%T)", vals[0], vals[0])
	}
	if s, _ := vals[1].(string); s != "alice" {
		t.Fatalf("val[1]: got %v", vals[1])
	}
}

func TestDecodeJSONIntegerCollapse(t *testing.T) {
	t.Parallel()
	got := decodeJSON(json.RawMessage(`42`))
	if n, ok := got.(int64); !ok || n != 42 {
		t.Fatalf("int collapse: got %v (%T)", got, got)
	}
	got = decodeJSON(json.RawMessage(`3.5`))
	if f, ok := got.(float64); !ok || f != 3.5 {
		t.Fatalf("float passthrough: got %v (%T)", got, got)
	}
	if decodeJSON(json.RawMessage(`null`)) != nil {
		t.Fatalf("null should decode to nil")
	}
}

func TestParseTriggerBody(t *testing.T) {
	t.Parallel()
	timing, event := parseTriggerBody("CREATE TRIGGER t BEFORE UPDATE ON foo BEGIN SELECT 1; END")
	if timing != "BEFORE" || event != "UPDATE" {
		t.Fatalf("got timing=%q event=%q", timing, event)
	}
}
