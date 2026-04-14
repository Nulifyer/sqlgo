package sqltok

import "strings"

// UnsafeMutation is a single flagged statement in a batch. Reason is a
// short human-readable phrase ("UPDATE without WHERE") that drives the
// confirmation prompt; Statement is the first ~80 chars of the
// offending statement so the user can tell which one they're being
// asked about when several are queued.
type UnsafeMutation struct {
	Reason    string
	Statement string
}

// UnsafeMutations scans src for statements that are commonly-destructive
// when typed by mistake and returns one entry per offence. The current
// ruleset:
//
//   - UPDATE ... with no WHERE clause at statement depth
//   - DELETE FROM ... with no WHERE clause at statement depth
//   - TRUNCATE (always)
//   - DROP DATABASE / DROP SCHEMA / DROP TABLE (always)
//
// Statements are split on ';' at paren depth zero. The first keyword in
// each statement (skipping comments/whitespace) drives the rule check.
// Returns an empty slice when the batch is clean.
func UnsafeMutations(src string) []UnsafeMutation {
	toks := TokenizeText(src)
	if len(toks) == 0 {
		return nil
	}
	var out []UnsafeMutation
	start := 0
	depth := 0
	emit := func(end int) {
		stmt := statementSummary(toks[start:end])
		if stmt == "" {
			return
		}
		if r := classifyStatement(toks[start:end]); r != "" {
			out = append(out, UnsafeMutation{Reason: r, Statement: stmt})
		}
	}
	for i, t := range toks {
		if t.Kind == Punct {
			switch t.Text {
			case "(":
				depth++
			case ")":
				if depth > 0 {
					depth--
				}
			case ";":
				if depth == 0 {
					emit(i)
					start = i + 1
				}
			}
		}
	}
	emit(len(toks))
	return out
}

// classifyStatement returns the unsafe reason for the given token slice,
// or "" if the statement is safe. Expects the slice to contain exactly
// one statement (no trailing ';' required).
func classifyStatement(toks []Token) string {
	first, firstIdx := firstKeyword(toks, 0)
	switch first {
	case "UPDATE":
		if !hasWhereAtDepthZero(toks, firstIdx) {
			return "UPDATE without WHERE"
		}
	case "DELETE":
		// DELETE FROM ... — look past FROM for WHERE.
		if !hasWhereAtDepthZero(toks, firstIdx) {
			return "DELETE without WHERE"
		}
	case "TRUNCATE":
		return "TRUNCATE"
	case "DROP":
		second, _ := firstKeyword(toks, firstIdx+1)
		switch second {
		case "DATABASE", "SCHEMA":
			return "DROP " + second
		case "TABLE":
			return "DROP TABLE"
		}
	}
	return ""
}

// firstKeyword returns the uppercase text of the first Keyword or
// Ident token at or after start, skipping whitespace and comments.
// Idents are accepted so statements beginning with words that aren't
// in the keyword set (e.g. TRUNCATE) are still classifiable.
// Returns "" when no eligible token is found.
func firstKeyword(toks []Token, start int) (string, int) {
	for i := start; i < len(toks); i++ {
		switch toks[i].Kind {
		case Whitespace, Comment:
			continue
		case Keyword, Ident:
			return strings.ToUpper(toks[i].Text), i
		default:
			return "", i
		}
	}
	return "", len(toks)
}

// hasWhereAtDepthZero scans forward from start and reports whether a
// WHERE keyword appears at paren depth zero (i.e. a top-level WHERE,
// not one nested inside a subquery used as a value expression).
func hasWhereAtDepthZero(toks []Token, start int) bool {
	depth := 0
	for i := start; i < len(toks); i++ {
		t := toks[i]
		if t.Kind == Punct {
			switch t.Text {
			case "(":
				depth++
			case ")":
				if depth > 0 {
					depth--
				}
			}
			continue
		}
		if depth == 0 && t.Kind == Keyword && strings.EqualFold(t.Text, "WHERE") {
			return true
		}
	}
	return false
}

// statementSummary is a one-line excerpt of the statement for the
// confirm prompt. Collapses whitespace, caps at 80 runes with an ellipsis.
func statementSummary(toks []Token) string {
	var b strings.Builder
	sawSpace := false
	for _, t := range toks {
		if t.Kind == Whitespace || t.Kind == Comment {
			if b.Len() > 0 {
				sawSpace = true
			}
			continue
		}
		if sawSpace {
			b.WriteByte(' ')
			sawSpace = false
		}
		b.WriteString(t.Text)
	}
	s := strings.TrimSpace(b.String())
	const max = 80
	rs := []rune(s)
	if len(rs) > max {
		return string(rs[:max-1]) + "…"
	}
	return s
}
