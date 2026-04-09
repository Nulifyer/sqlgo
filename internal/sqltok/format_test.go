package sqltok

import "testing"

// These tests pin the formatter's high-level behavior. The exact
// indent and whitespace pattern is a heuristic, so tests assert on
// shape (what goes on which line) rather than full string equality.

func TestFormatUppercasesKeywords(t *testing.T) {
	t.Parallel()
	got := Format("select id from users")
	if !contains(got, "SELECT") || !contains(got, "FROM") {
		t.Errorf("keywords not uppercased: %q", got)
	}
}

func TestFormatSplitsSelectFromOnLines(t *testing.T) {
	t.Parallel()
	got := Format("SELECT id, name FROM users WHERE id = 1")
	if !contains(got, "SELECT\n") {
		t.Errorf("SELECT should begin its own line: %q", got)
	}
	if !contains(got, "FROM\n") {
		t.Errorf("FROM should begin its own line: %q", got)
	}
	if !contains(got, "WHERE\n") {
		t.Errorf("WHERE should begin its own line: %q", got)
	}
}

func TestFormatWrapsSelectListCommas(t *testing.T) {
	t.Parallel()
	got := Format("SELECT a, b, c FROM t")
	// Each column should sit on its own line.
	if countLines(got, "a") != 1 || countLines(got, "b") != 1 || countLines(got, "c") != 1 {
		t.Errorf("select list not wrapped: %q", got)
	}
}

func TestFormatWrapsWhereAndOr(t *testing.T) {
	t.Parallel()
	got := Format("SELECT 1 FROM t WHERE a = 1 AND b = 2 OR c = 3")
	// AND / OR at top level should wrap to the start of a new line
	// (preceded by whatever indent the formatter chose).
	if !hasLineStartingWith(got, "AND") {
		t.Errorf("AND not wrapped: %q", got)
	}
	if !hasLineStartingWith(got, "OR") {
		t.Errorf("OR not wrapped: %q", got)
	}
}

// hasLineStartingWith returns true if any line in s begins with
// prefix after stripping its leading whitespace. Used to sanity
// check that a token actually wrapped to its own line regardless of
// how much indent the formatter chose.
func hasLineStartingWith(s, prefix string) bool {
	for _, line := range splitLines(s) {
		i := 0
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i+len(prefix) <= len(line) && line[i:i+len(prefix)] == prefix {
			return true
		}
	}
	return false
}

func TestFormatKeepsFunctionCallsInline(t *testing.T) {
	t.Parallel()
	got := Format("SELECT COUNT(*) FROM t")
	if !contains(got, "COUNT(*)") {
		t.Errorf("function call broken across lines: %q", got)
	}
}

func TestFormatIndentsSubquery(t *testing.T) {
	t.Parallel()
	got := Format("SELECT id FROM (SELECT id FROM t) sub")
	// Subquery opens after the paren and the inner SELECT should be
	// indented relative to the outer SELECT.
	if !contains(got, "(\n") {
		t.Errorf("subquery paren did not increase indent: %q", got)
	}
}

func TestFormatPreservesStringsAndComments(t *testing.T) {
	t.Parallel()
	src := `SELECT 'hello, world' -- inline comment
FROM t`
	got := Format(src)
	if !contains(got, `'hello, world'`) {
		t.Errorf("string literal lost: %q", got)
	}
	if !contains(got, "-- inline comment") {
		t.Errorf("comment lost: %q", got)
	}
}

// TestFormatMajorClausesAtBaseIndent pins the exact layout of a
// small statement with SELECT / FROM / JOIN / WHERE so a regression
// in the indent machinery is caught immediately. This is the user
// example from the Feb 2026 format screenshot: major clauses live
// at column 0, their content lives at column 4, and JOIN lines up
// with the tables in the FROM clause.
func TestFormatMajorClausesAtBaseIndent(t *testing.T) {
	t.Parallel()
	src := `SELECT * FROM [dbo].[employees] JOIN dbo.other ON field1 = field2 WHERE a = 1`
	want := "SELECT\n    *\nFROM\n    [dbo].[employees]\n    JOIN dbo.other ON field1 = field2\nWHERE\n    a = 1"
	got := Format(src)
	if got != want {
		t.Errorf("format mismatch\ngot:\n%s\n---\nwant:\n%s", got, want)
	}
}

// TestFormatSecondClauseResetsIndent verifies the specific bug fixed
// by the Feb 2026 rewrite: consecutive major clauses used to stack
// each other's indents, so FROM landed inside SELECT's column-2 item
// indent. With the clause/item split, every major clause now resets
// to column 0.
func TestFormatSecondClauseResetsIndent(t *testing.T) {
	t.Parallel()
	got := Format("SELECT id FROM t WHERE id = 1")
	lines := splitLines(got)
	// Find the FROM line and confirm it starts at column 0.
	for _, line := range lines {
		if line == "FROM" {
			return
		}
	}
	t.Errorf("FROM not at column 0: %q", got)
}

// TestFormatUsesFourSpaceIndent makes sure content sits at column 4,
// not column 2.
func TestFormatUsesFourSpaceIndent(t *testing.T) {
	t.Parallel()
	got := Format("SELECT 1 FROM t")
	// The line containing "1" should have exactly 4 leading spaces.
	for _, line := range splitLines(got) {
		if indexOfSub(line, "1") >= 0 && line[:1] == " " {
			leading := 0
			for leading < len(line) && line[leading] == ' ' {
				leading++
			}
			if leading != 4 {
				t.Errorf("content line has %d leading spaces, want 4: %q", leading, line)
			}
			return
		}
	}
	t.Errorf("no content line found in %q", got)
}

func TestFormatEmptyInputUnchanged(t *testing.T) {
	t.Parallel()
	if got := Format("   "); got != "   " {
		t.Errorf("empty format changed input: %q", got)
	}
	if got := Format(""); got != "" {
		t.Errorf("empty string format: %q", got)
	}
}

// contains / countLines are tiny helpers so the assertions above
// aren't a wall of strings.Contains.
func contains(s, sub string) bool {
	return len(sub) == 0 || len(s) >= len(sub) && indexOfSub(s, sub) >= 0
}

func countLines(s, needle string) int {
	n := 0
	for _, line := range splitLines(s) {
		if indexOfSub(line, needle) >= 0 {
			n++
		}
	}
	return n
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func indexOfSub(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
