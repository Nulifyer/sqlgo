package libsql

import (
	"encoding/json"
	"testing"
)

func TestHranaValueUnmarshal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		typ  string
		want any
	}{
		{"integer", `{"type":"integer","value":"42"}`, "integer", int64(42)},
		{"negative", `{"type":"integer","value":"-7"}`, "integer", int64(-7)},
		{"float", `{"type":"float","value":3.5}`, "float", 3.5},
		{"text", `{"type":"text","value":"hi"}`, "text", "hi"},
		{"null", `{"type":"null"}`, "null", nil},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var v hranaValue
			if err := json.Unmarshal([]byte(tc.in), &v); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if v.Type != tc.typ {
				t.Fatalf("type: got %q want %q", v.Type, tc.typ)
			}
			if v.Value != tc.want {
				t.Fatalf("value: got %v (%T) want %v (%T)", v.Value, v.Value, tc.want, tc.want)
			}
		})
	}
}

func TestValueToAnyBlob(t *testing.T) {
	t.Parallel()
	v := hranaValue{Type: "blob", Base64: "aGVsbG8="} // "hello"
	got, ok := valueToAny(v).([]byte)
	if !ok || string(got) != "hello" {
		t.Fatalf("blob decode: got %v ok=%v", got, ok)
	}
}

func TestParseTriggerBody(t *testing.T) {
	t.Parallel()
	timing, event := parseTriggerBody("CREATE TRIGGER t AFTER INSERT ON foo BEGIN SELECT 1; END")
	if timing != "AFTER" || event != "INSERT" {
		t.Fatalf("got timing=%q event=%q", timing, event)
	}
}
