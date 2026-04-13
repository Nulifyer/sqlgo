package sqltok

import (
	"strings"
)

// Format rewrites an SQL statement using a small set of heuristics
// driven by the tokenizer. It is deliberately not an AST-based
// formatter -- dialects differ too much for a strict grammar to be
// worth the cost at this stage. What it guarantees:
//
//   - Recognized keywords are normalized to uppercase.
//   - Strings and comments round-trip byte-for-byte.
//   - Major clauses (SELECT, FROM, WHERE, GROUP BY, HAVING, ORDER BY,
//     LIMIT, OFFSET, UNION, INSERT, UPDATE, DELETE, VALUES, SET) each
//     begin a new line at the current clause indent.
//   - Each clause's items (the SELECT list, FROM tables, WHERE
//     predicates) sit one indent level deeper than the clause keyword.
//   - JOIN forms line up with the tables they join, not with the
//     surrounding FROM keyword.
//   - AND / OR at parenthesis depth zero wrap to a new line at the
//     current item indent.
//   - Parentheses that are NOT immediately preceded by an identifier
//     (so: subqueries and grouping, not function calls) increase the
//     clause indent by one level for their contents.
//
// If the input is empty or can't be tokenized meaningfully, Format
// returns it unchanged so Ctrl+Z always restores the user's original
// text.
func Format(src string) string {
	if strings.TrimSpace(src) == "" {
		return src
	}
	tokens := TokenizeText(src)
	if len(tokens) == 0 {
		return src
	}
	f := &formatter{}
	// writeToken returns the next index to resume from; usually i+1
	// but SELECT-modifier consumption can advance farther in one
	// call. Keeping the step as the function's return value lets the
	// lookahead stay local to the case that cares about it.
	for i := 0; i < len(tokens); {
		i = f.writeToken(tokens, i)
	}
	return tidy(f.buf.String())
}

// indentWidth is the number of spaces per indent level. Four matches
// what most SQL style guides and the user's example request.
const indentWidth = 4

// majorClause lists keywords that always begin a new line at the
// current clause indent, resetting whatever item indent the previous
// clause was using.
var majorClause = map[string]struct{}{
	"SELECT": {}, "FROM": {}, "WHERE": {}, "HAVING": {}, "LIMIT": {},
	"OFFSET": {}, "UNION": {}, "INSERT": {}, "UPDATE": {}, "DELETE": {},
	"VALUES": {}, "SET": {}, "RETURNING": {},
}

// joinHead identifies the first word of a JOIN phrase. These wrap to
// the current item indent so `JOIN b ON ...` lines up with the tables
// it's joining against.
var joinHead = map[string]struct{}{
	"JOIN": {}, "INNER": {}, "LEFT": {}, "RIGHT": {}, "FULL": {}, "CROSS": {},
}

// isJoinModifier reports whether kw is a JOIN-phrase modifier that a
// following bare JOIN should stay inline with ("INNER JOIN", "LEFT
// OUTER JOIN"). OUTER isn't a joinHead itself -- it falls through the
// default keyword path and lands inline, and this keeps the JOIN that
// follows it on the same line as LEFT / RIGHT / FULL.
func isJoinModifier(kw string) bool {
	switch kw {
	case "INNER", "LEFT", "RIGHT", "FULL", "CROSS", "OUTER":
		return true
	}
	return false
}

// commaSplitters are clause keywords whose comma-separated lists
// should wrap onto separate lines at the item indent.
var commaSplitters = map[string]bool{
	"SELECT": true, "FROM": true, "SET": true, "VALUES": true,
	"GROUP": true, "ORDER": true,
}

type formatter struct {
	buf strings.Builder

	// baseIndent is where clause keywords (SELECT, FROM, ...) land on
	// the current paren level. Item indent is always baseIndent +
	// indentWidth; callers never raise the raw indent themselves.
	baseIndent int

	// Pending newline state. When atLine is true, the next writeRaw
	// emits pendingIndent spaces before the token. This lets
	// different parts of the emit path pick different line-start
	// indents (baseIndent for a clause keyword, itemIndent for its
	// content) without fighting each other.
	atLine        bool
	pendingIndent int

	// parenStack saves the baseIndent that was active when each open
	// paren was seen. -1 means "function call paren; don't change
	// indent". ')' pops and (if not -1) restores baseIndent.
	parenStack []int
	// splitStack mirrors parenStack: true if the paren opened inside
	// a comma-splitting context (so a subquery SELECT list also
	// wraps on commas).
	splitStack []bool
	// closeStack mirrors parenStack: the column ')' should land on when
	// it closes. -1 means the paren is a function call and ')' stays
	// inline (no newline). Populated only when parenStack[i] != -1.
	closeStack []int

	// currentSplit is the outermost split state at paren depth zero.
	// Flipped on SELECT/FROM/SET/VALUES/GROUP BY/ORDER BY.
	currentSplit bool
}

// itemIndent is the indent used for items inside the current clause
// (SELECT list, FROM tables, WHERE conditions, ...).
func (f *formatter) itemIndent() int {
	return f.baseIndent + indentWidth
}

// writeToken emits tokens[i] to the formatter and returns the index
// the caller should resume at. Most paths return i+1, but the SELECT
// branch can look ahead and consume modifier tokens (DISTINCT, ALL,
// TOP <n>) inline on the SELECT line, in which case it returns the
// index past the last consumed modifier.
func (f *formatter) writeToken(tokens []Token, i int) int {
	t := tokens[i]
	switch t.Kind {
	case Whitespace:
		return i + 1
	case Comment:
		f.writeRaw(t.Text)
		if strings.HasSuffix(t.Text, "\n") {
			f.newlineTo(f.itemIndent())
		} else {
			f.writeRaw(" ")
		}
		return i + 1
	case String:
		f.writeRaw(t.Text)
		f.writeRaw(" ")
		return i + 1
	case Number:
		f.writeRaw(t.Text)
		f.writeRaw(" ")
		return i + 1
	case Ident:
		f.writeRaw(t.Text)
		f.writeRaw(" ")
		return i + 1
	case Keyword:
		upper := strings.ToUpper(t.Text)

		// GROUP BY / ORDER BY are two-word clause heads. We detect the
		// pair and emit "GROUP BY" / "ORDER BY" together, then swallow
		// the follow-up BY token when it comes around below.
		if (upper == "GROUP" || upper == "ORDER") && nextKeyword(tokens, i) == "BY" {
			f.newlineTo(f.baseIndent)
			f.writeRaw(upper + " BY")
			f.newlineTo(f.itemIndent())
			f.currentSplit = true
			return i + 1
		}
		if upper == "BY" {
			if prev := prevKeyword(tokens, i); prev == "GROUP" || prev == "ORDER" {
				return i + 1
			}
		}

		// SELECT: special-cased so its modifiers (DISTINCT, ALL, TOP
		// <n>) stay on the same line as the keyword. Without this
		// lookahead the generic major-clause branch would push
		// everything after "SELECT" to the item indent line,
		// producing weird layouts like "SELECT\n    DISTINCT TOP 100 *".
		if upper == "SELECT" {
			f.newlineTo(f.baseIndent)
			f.writeRaw("SELECT")
			next := f.consumeSelectModifiers(tokens, i+1)
			f.newlineTo(f.itemIndent())
			f.currentSplit = true
			return next
		}

		// Major clause: reset to baseIndent, write the keyword, then
		// move down to itemIndent for its content. Because baseIndent
		// never changes between sibling major clauses at the same
		// paren level, consecutive clauses always line up with each
		// other instead of accumulating indent.
		if _, ok := majorClause[upper]; ok {
			f.newlineTo(f.baseIndent)
			f.writeRaw(upper)
			f.newlineTo(f.itemIndent())
			f.currentSplit = commaSplitters[upper]
			return i + 1
		}

		// JOIN phrase: wraps to itemIndent so joined tables line up
		// with the tables they join against. Bare JOIN stays on the
		// same line as any preceding modifier (INNER / LEFT OUTER /
		// etc.) so multi-word joins don't split across two lines.
		if _, ok := joinHead[upper]; ok {
			if upper == "JOIN" && isJoinModifier(prevKeyword(tokens, i)) {
				f.writeRaw("JOIN")
				f.writeRaw(" ")
				f.currentSplit = false
				return i + 1
			}
			f.newlineTo(f.itemIndent())
			f.writeRaw(upper)
			f.writeRaw(" ")
			// JOINs don't split the surrounding clause on commas.
			f.currentSplit = false
			return i + 1
		}

		// AND / OR at top level (paren depth zero) wrap to the
		// current item indent so long WHERE predicates read cleanly.
		if (upper == "AND" || upper == "OR") && len(f.parenStack) == 0 {
			f.newlineTo(f.itemIndent())
			f.writeRaw(upper)
			f.writeRaw(" ")
			return i + 1
		}

		// Default: inline uppercase with a trailing space.
		f.writeRaw(upper)
		f.writeRaw(" ")
		return i + 1

	case Punct:
		switch t.Text {
		case ",":
			f.trimTrailingSpace()
			f.writeRaw(",")
			// Wrap the comma if we're in a top-level split context or
			// inside a paren that opened from one (nested SELECT
			// list).
			split := f.currentSplit && len(f.parenStack) == 0
			if !split && len(f.splitStack) > 0 && f.splitStack[len(f.splitStack)-1] {
				split = true
			}
			if split {
				f.newlineTo(f.itemIndent())
			} else {
				f.writeRaw(" ")
			}
		case "(":
			if IsFunctionCall(tokens, i) {
				f.trimTrailingSpace()
				f.writeRaw("(")
				f.parenStack = append(f.parenStack, -1)
				f.splitStack = append(f.splitStack, false)
				f.closeStack = append(f.closeStack, -1)
			} else {
				f.writeRaw("(")
				// Two flavors of multi-line paren:
				//  - subquery (starts with SELECT / WITH / VALUES):
				//    bump two levels so the body sits one indent
				//    deeper than the parent clause's items (where
				//    the '(' tends to live), and close ')' aligned
				//    with those items;
				//  - grouping / value list (IN (...), AND (...)):
				//    bump one level so the body sits just inside
				//    the '(', and close ')' back at the '('s column.
				bump := indentWidth
				closeAt := f.baseIndent
				if isSubqueryStart(tokens, i+1) {
					bump = 2 * indentWidth
					closeAt = f.baseIndent + indentWidth
				}
				f.parenStack = append(f.parenStack, f.baseIndent)
				f.splitStack = append(f.splitStack, f.currentSplit)
				f.closeStack = append(f.closeStack, closeAt)
				f.baseIndent += bump
				f.newlineTo(f.baseIndent)
				// Reset the child context so the inner clauses can
				// pick their own state from scratch.
				f.currentSplit = false
			}
		case ")":
			f.trimTrailingSpace()
			if n := len(f.parenStack); n > 0 {
				saved := f.parenStack[n-1]
				prevSplit := f.splitStack[n-1]
				closeAt := f.closeStack[n-1]
				f.parenStack = f.parenStack[:n-1]
				f.splitStack = f.splitStack[:n-1]
				f.closeStack = f.closeStack[:n-1]
				if saved >= 0 {
					f.baseIndent = saved
					f.currentSplit = prevSplit
					f.newlineTo(closeAt)
				}
			}
			f.writeRaw(")")
			f.writeRaw(" ")
		case ";":
			// Semicolons sit on their own line at the base indent so
			// the statement terminator is visually distinct from the
			// preceding FROM / WHERE / etc content. We trim any
			// trailing space from the previous token first, drop to
			// a fresh line at baseIndent, then emit the ; and drop
			// to another line at baseIndent for whatever follows.
			f.trimTrailingSpace()
			f.newlineTo(f.baseIndent)
			f.writeRaw(";")
			f.newlineTo(f.baseIndent)
		case ".":
			f.trimTrailingSpace()
			f.writeRaw(".")
		default:
			f.writeRaw(t.Text)
		}
		return i + 1
	case Operator:
		f.writeRaw(t.Text)
		f.writeRaw(" ")
		return i + 1
	default:
		f.writeRaw(t.Text)
		return i + 1
	}
}

// consumeSelectModifiers emits any DISTINCT / ALL / TOP <n> tokens
// immediately after SELECT on the same line and returns the index of
// the first token that isn't a recognized modifier. Whitespace
// tokens between modifiers are skipped. TOP is followed by a number
// argument; a bare "TOP" with no number (unusual but syntactically
// valid in some dialects) is still emitted.
//
// Only a small set of modifiers is recognized. More exotic cases
// (TOP (expr), TOP n PERCENT, TOP n WITH TIES, DISTINCTROW) fall
// through to the normal keyword path, which isn't as pretty but
// doesn't produce invalid SQL.
func (f *formatter) consumeSelectModifiers(tokens []Token, start int) int {
	i := start
	skipWS := func() {
		for i < len(tokens) && tokens[i].Kind == Whitespace {
			i++
		}
	}
	for {
		skipWS()
		if i >= len(tokens) || tokens[i].Kind != Keyword {
			return i
		}
		upper := strings.ToUpper(tokens[i].Text)
		switch upper {
		case "DISTINCT", "ALL":
			f.writeRaw(" " + upper)
			i++
		case "TOP":
			f.writeRaw(" TOP")
			i++
			skipWS()
			if i < len(tokens) && tokens[i].Kind == Number {
				f.writeRaw(" " + tokens[i].Text)
				i++
			}
		default:
			return i
		}
	}
}

// isSubqueryStart reports whether the tokens starting at index start
// begin with a subquery-introducing keyword (SELECT, WITH, VALUES).
// Used to distinguish parens that wrap a nested query from grouping
// parens around an expression or an IN value list.
func isSubqueryStart(tokens []Token, start int) bool {
	for j := start; j < len(tokens); j++ {
		t := tokens[j]
		if t.Kind == Whitespace || t.Kind == Comment {
			continue
		}
		if t.Kind != Keyword {
			return false
		}
		switch strings.ToUpper(t.Text) {
		case "SELECT", "WITH", "VALUES":
			return true
		}
		return false
	}
	return false
}

// IsFunctionCall looks backward from tokens[i] (an open paren) and
// returns true if the preceding non-whitespace token is an identifier
// or a keyword that's conventionally followed by a function call
// (CAST, COUNT, etc). This keeps `COUNT(*)` and `CAST(x AS INT)`
// inline while `SELECT ... FROM (SELECT ...)` still indents.
func IsFunctionCall(tokens []Token, i int) bool {
	for j := i - 1; j >= 0; j-- {
		t := tokens[j]
		if t.Kind == Whitespace || t.Kind == Comment {
			continue
		}
		if t.Kind == Ident {
			return true
		}
		if t.Kind == Keyword {
			switch strings.ToUpper(t.Text) {
			case "CAST", "CONVERT", "COUNT", "SUM", "AVG", "MIN", "MAX",
				"COALESCE", "NULLIF", "SUBSTRING", "TRIM", "LOWER", "UPPER",
				"LENGTH", "ABS", "ROUND", "FLOOR", "CEIL", "CEILING",
				"EXTRACT", "DATE_PART", "NOW", "CURRENT_TIMESTAMP", "CURRENT_DATE":
				return true
			}
		}
		return false
	}
	return false
}

// nextKeyword returns the uppercase text of the next Keyword token
// after i, skipping whitespace. "" if none.
func nextKeyword(tokens []Token, i int) string {
	for j := i + 1; j < len(tokens); j++ {
		if tokens[j].Kind == Whitespace {
			continue
		}
		if tokens[j].Kind == Keyword {
			return strings.ToUpper(tokens[j].Text)
		}
		return ""
	}
	return ""
}

// prevKeyword is the mirror of nextKeyword for the token before i.
func prevKeyword(tokens []Token, i int) string {
	for j := i - 1; j >= 0; j-- {
		if tokens[j].Kind == Whitespace {
			continue
		}
		if tokens[j].Kind == Keyword {
			return strings.ToUpper(tokens[j].Text)
		}
		return ""
	}
	return ""
}

// writeRaw appends s to the output buffer, flushing any pending
// indent first. Callers are responsible for whitespace inside s.
func (f *formatter) writeRaw(s string) {
	if s == "" {
		return
	}
	if f.atLine {
		if f.pendingIndent > 0 {
			f.buf.WriteString(strings.Repeat(" ", f.pendingIndent))
		}
		f.atLine = false
	}
	f.buf.WriteString(s)
}

// newlineTo begins a new output line and arms pendingIndent so the
// next writeRaw emits that many leading spaces. Calling this while
// already at a line-start is idempotent except for the indent
// target, which is updated -- the last caller wins.
func (f *formatter) newlineTo(indent int) {
	if f.atLine {
		f.pendingIndent = indent
		return
	}
	f.trimTrailingSpace()
	b := f.buf.String()
	if b != "" && b[len(b)-1] != '\n' {
		f.buf.WriteByte('\n')
	}
	f.pendingIndent = indent
	f.atLine = true
}

// trimTrailingSpace drops any trailing space characters on the
// current output line so ",", "(", ";" etc don't leave holes.
func (f *formatter) trimTrailingSpace() {
	b := f.buf.String()
	i := len(b)
	for i > 0 && b[i-1] == ' ' {
		i--
	}
	if i < len(b) {
		f.buf.Reset()
		f.buf.WriteString(b[:i])
	}
}

// tidy strips trailing whitespace from each line and collapses runs
// of blank lines to at most one. Applied once at the end of Format
// so the rest of the formatter doesn't have to worry about
// intermediate artifacts.
func tidy(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blankStreak := 0
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if trimmed == "" {
			blankStreak++
			if blankStreak > 1 {
				continue
			}
		} else {
			blankStreak = 0
		}
		out = append(out, trimmed)
	}
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}
