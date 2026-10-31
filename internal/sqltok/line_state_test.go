package sqltok

import "testing"

func TestLineStateCarriesSQLConstructs(t *testing.T) {
	for _, lines := range [][]string{{"SELECT 'first", "SELECT second';"}, {"SELECT $tag$first", "SELECT second$tag$;"}, {"/* outer /* nested", "SELECT 1; */ end */ SELECT 2;"}, {"SELECT [first", "second]]part];"}} {
		state := LineState{}
		_, state = TokenizeLineState([]rune(lines[0]), state)
		if state == (LineState{}) {
			t.Fatalf("no carry state for %q", lines[0])
		}
		tokens, end := TokenizeLineState([]rune(lines[1]), state)
		if end != (LineState{}) || len(tokens) == 0 || (tokens[0].Kind != String && tokens[0].Kind != Comment) {
			t.Fatalf("continuation %q state=%+v tokens=%+v", lines[1], end, tokens)
		}
	}
}
