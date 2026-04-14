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

// TestKeywordsForDialect pins the dialect-overlay contract: core
// keywords appear for every engine, overlay keywords only appear for
// the engines they target.
func TestKeywordsForDialect(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		dialect Dialect
		want    []string // must appear
		absent  []string // must not appear
	}{
		{
			name:    "mssql has TOP not LIMIT",
			dialect: DialectMSSQL,
			want:    []string{"SELECT", "TRUNCATE", "TOP", "OUTPUT", "APPLY", "IDENTITY"},
			absent:  []string{"LIMIT", "PRAGMA", "RETURNING", "ILIKE", "AUTO_INCREMENT"},
		},
		{
			name:    "postgres has RETURNING not TOP",
			dialect: DialectPostgres,
			want:    []string{"SELECT", "TRUNCATE", "RETURNING", "ILIKE", "LATERAL", "LIMIT"},
			absent:  []string{"TOP", "PRAGMA", "OUTPUT", "AUTO_INCREMENT"},
		},
		{
			name:    "mysql has AUTO_INCREMENT not PRAGMA",
			dialect: DialectMySQL,
			want:    []string{"SELECT", "TRUNCATE", "AUTO_INCREMENT", "ENGINE", "LIMIT"},
			absent:  []string{"TOP", "PRAGMA", "RETURNING", "ILIKE"},
		},
		{
			name:    "sqlite has PRAGMA and RETURNING not TOP",
			dialect: DialectSQLite,
			want:    []string{"SELECT", "TRUNCATE", "PRAGMA", "RETURNING", "WITHOUT", "LIMIT"},
			absent:  []string{"TOP", "OUTPUT", "AUTO_INCREMENT", "ILIKE"},
		},
		{
			name:    "all returns everything",
			dialect: DialectAll,
			want:    []string{"SELECT", "TRUNCATE", "TOP", "LIMIT", "PRAGMA", "RETURNING"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := KeywordsFor(tc.dialect)
			gotSet := make(map[string]struct{}, len(got))
			for _, k := range got {
				gotSet[k] = struct{}{}
			}
			for _, k := range tc.want {
				if _, ok := gotSet[k]; !ok {
					t.Errorf("KeywordsFor(%v) missing %q", tc.dialect, k)
				}
			}
			for _, k := range tc.absent {
				if _, ok := gotSet[k]; ok {
					t.Errorf("KeywordsFor(%v) unexpectedly contains %q", tc.dialect, k)
				}
			}
		})
	}
}

// TestKeywordsForZero confirms a zero Dialect returns no keywords --
// callers that want a default fallback must pass DialectAll explicitly.
func TestKeywordsForZero(t *testing.T) {
	t.Parallel()
	if got := KeywordsFor(0); len(got) != 0 {
		t.Errorf("KeywordsFor(0) = %d entries, want 0", len(got))
	}
}

// TestTruncateIsKeyword is the direct regression guard for the Apr 2026
// user report: TRUNCATE must tokenize as a keyword (previously was
// Ident, which is why autocomplete never suggested it).
func TestTruncateIsKeyword(t *testing.T) {
	t.Parallel()
	if !IsKeyword("truncate") {
		t.Error("TRUNCATE should be recognized as a keyword")
	}
	toks := TokenizeText("TRUNCATE TABLE t")
	if toks[0].Kind != Keyword {
		t.Errorf("TRUNCATE kind = %v, want Keyword", toks[0].Kind)
	}
}
