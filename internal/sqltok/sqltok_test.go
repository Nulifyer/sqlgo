package sqltok

import "testing"

// kindsOf returns the kinds of non-whitespace tokens produced by
// tokenizing s. Whitespace-only tests are covered by dedicated cases;
// everything else wants the non-whitespace kinds for concise asserts.
func kindsOf(s string) []Kind {
	var out []Kind
	for _, t := range TokenizeText(s) {
		if t.Kind == Whitespace {
			continue
		}
		out = append(out, t.Kind)
	}
	return out
}

func eqKinds(a, b []Kind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSimpleSelect(t *testing.T) {
	t.Parallel()
	got := kindsOf("SELECT id, name FROM users")
	want := []Kind{Keyword, Ident, Punct, Ident, Keyword, Ident}
	if !eqKinds(got, want) {
		t.Errorf("kinds = %v, want %v", got, want)
	}
}

func TestKeywordsAreCaseInsensitive(t *testing.T) {
	t.Parallel()
	toks := TokenizeText("select Select SELECT sElEcT")
	seen := 0
	for _, tk := range toks {
		if tk.Kind == Keyword {
			seen++
		}
	}
	if seen != 4 {
		t.Errorf("keyword count = %d, want 4 in %+v", seen, toks)
	}
}

func TestStringLiterals(t *testing.T) {
	t.Parallel()
	cases := []string{
		`'plain'`,
		`'escaped '' inside'`,
		`'with \n backslash'`,
		`"double quoted"`,
		"`backticked`",
	}
	for _, c := range cases {
		toks := TokenizeText(c)
		if len(toks) != 1 || toks[0].Kind != String {
			t.Errorf("%q tokens = %+v", c, toks)
		}
	}
}

func TestBracketedIdentifierMSSQL(t *testing.T) {
	t.Parallel()
	toks := TokenizeText("SELECT [dbo].[Users]")
	// Keyword, String, Punct, String
	wantKinds := []Kind{Keyword, String, Punct, String}
	got := kindsOf("SELECT [dbo].[Users]")
	if !eqKinds(got, wantKinds) {
		t.Errorf("kinds = %v, want %v (tokens %+v)", got, wantKinds, toks)
	}
}

func TestNumberLiterals(t *testing.T) {
	t.Parallel()
	cases := []string{"0", "42", "3.14", "1e10", "2.5E-3", "0.0"}
	for _, c := range cases {
		toks := TokenizeText(c)
		if len(toks) != 1 || toks[0].Kind != Number {
			t.Errorf("%q tokens = %+v", c, toks)
		}
	}
}

func TestLineComment(t *testing.T) {
	t.Parallel()
	got := kindsOf("SELECT 1 -- trailing comment\nFROM t")
	want := []Kind{Keyword, Number, Comment, Keyword, Ident}
	if !eqKinds(got, want) {
		t.Errorf("kinds = %v, want %v", got, want)
	}
}

func TestBlockComment(t *testing.T) {
	t.Parallel()
	got := kindsOf("SELECT /* inner\nmulti */ 1")
	want := []Kind{Keyword, Comment, Number}
	if !eqKinds(got, want) {
		t.Errorf("kinds = %v, want %v", got, want)
	}
}

func TestMultiCharOperators(t *testing.T) {
	t.Parallel()
	toks := TokenizeText("a <= b AND c != d")
	var ops []string
	for _, t := range toks {
		if t.Kind == Operator {
			ops = append(ops, t.Text)
		}
	}
	if len(ops) != 2 || ops[0] != "<=" || ops[1] != "!=" {
		t.Errorf("operators = %v, want [<= !=]", ops)
	}
}

func TestIdentNotKeyword(t *testing.T) {
	t.Parallel()
	toks := TokenizeText("SELECT my_column FROM my_table")
	// Positions 1 and 3 are idents, not keywords.
	want := []Kind{Keyword, Ident, Keyword, Ident}
	got := kindsOf("SELECT my_column FROM my_table")
	if !eqKinds(got, want) {
		t.Errorf("kinds = %v, want %v (tokens %+v)", got, want, toks)
	}
}
