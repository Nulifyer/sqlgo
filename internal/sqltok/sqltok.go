// Package sqltok is a minimal SQL lexer used by the TUI's editor for
// syntax highlighting and by the formatter for structural
// transformations. It is intentionally loose -- dialects differ too
// much for a strict grammar to be worth the complexity at this stage.
//
// Scope:
//   - Line-local tokenization via TokenizeLine for the highlight path.
//     Block comments that cross line boundaries are flagged on the
//     last token of the line so the caller can carry comment state
//     across lines if it wants.
//   - Whole-text tokenization via TokenizeText for the formatter.
//     Handles multi-line strings and block comments correctly.
//
// Keyword table is a small set of ANSI/common dialect keywords. Token
// kind for an identifier that happens to match a keyword is Keyword;
// otherwise Ident. Lookups are case-insensitive.
package sqltok

import (
	"sort"
	"strings"
	"unicode"
)

// Kind is the category a token belongs to. The zero value, Text, is
// what the editor paints with the default style.
type Kind int

const (
	Text Kind = iota
	Keyword
	Ident
	Number
	String  // single-quoted, double-quoted, backticked, or bracketed identifier literal
	Comment // -- line comment or /* block comment */
	Operator
	Punct    // , ; ( ) .
	Whitespace
)

// Token is a single lexed span. StartCol / EndCol are rune offsets
// into the original line (for TokenizeLine) or the flattened text
// (for TokenizeText). EndCol is exclusive.
type Token struct {
	Kind     Kind
	StartCol int
	EndCol   int
	Text     string
}

// TokenizeLine lexes a single line of SQL. It is intentionally ignorant
// of cross-line block comment state: a line that starts inside a
// block comment gets tokenized as if it doesn't, and the editor is
// responsible for passing carryover state if it wants correct wrap
// behavior inside a long comment. Most SQL people write in the editor
// is single-line anyway.
func TokenizeLine(line []rune) []Token {
	l := &lexer{src: line}
	return l.run()
}

// TokenizeText lexes a whole block of SQL text, handling multi-line
// strings and block comments correctly. Used by the formatter which
// needs to see the stream from top to bottom.
func TokenizeText(text string) []Token {
	return TokenizeLine([]rune(text))
}

// IsKeyword reports whether s (case-insensitive) is one of the
// recognized SQL keywords.
func IsKeyword(s string) bool {
	_, ok := keywordSet[strings.ToUpper(s)]
	return ok
}

// Keywords returns the full list of recognized SQL keywords, sorted
// alphabetically and already uppercase. Exposed for the editor's
// autocomplete provider so it doesn't have to reach into keywordSet.
func Keywords() []string {
	out := make([]string, 0, len(keywordSet))
	for k := range keywordSet {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// keywordSet is the recognized-keyword lookup table. Deliberately
// narrow: enough to make most queries light up without pulling in a
// dialect-specific vocabulary that would mark random identifiers.
var keywordSet = map[string]struct{}{
	"ADD": {}, "ALL": {}, "ALTER": {}, "AND": {}, "AS": {}, "ASC": {},
	"BEGIN": {}, "BETWEEN": {}, "BY": {},
	"CASE": {}, "CAST": {}, "COMMIT": {}, "CREATE": {}, "CROSS": {},
	"DATABASE": {}, "DEFAULT": {}, "DELETE": {}, "DESC": {}, "DISTINCT": {},
	"DROP": {},
	"ELSE": {}, "END": {}, "EXCEPT": {}, "EXISTS": {},
	"FALSE": {}, "FOR": {}, "FOREIGN": {}, "FROM": {}, "FULL": {},
	"GROUP": {},
	"HAVING": {},
	"IF": {}, "IN": {}, "INDEX": {}, "INNER": {}, "INSERT": {},
	"INTERSECT": {}, "INTO": {}, "IS": {},
	"JOIN": {},
	"KEY": {},
	"LEFT": {}, "LIKE": {}, "LIMIT": {},
	"NATURAL": {}, "NOT": {}, "NULL": {},
	"OFFSET": {}, "ON": {}, "OR": {}, "ORDER": {}, "OUTER": {}, "OVER": {},
	"PARTITION": {}, "PERCENT": {}, "PRIMARY": {}, "PROCEDURE": {},
	"REFERENCES": {}, "RETURNING": {}, "RIGHT": {}, "ROLLBACK": {},
	"SELECT": {}, "SET": {},
	"TABLE": {}, "THEN": {}, "TIES": {}, "TO": {}, "TOP": {}, "TRUE": {},
	"UNION": {}, "UNIQUE": {}, "UPDATE": {}, "USING": {},
	"VALUES": {}, "VIEW": {},
	"WHEN": {}, "WHERE": {}, "WITH": {},
}

// lexer is a scratch-pad scanner over a rune slice. The zero value is
// not usable; construct with src set.
type lexer struct {
	src    []rune
	i      int
	tokens []Token
}

func (l *lexer) run() []Token {
	for l.i < len(l.src) {
		start := l.i
		c := l.src[l.i]
		switch {
		case unicode.IsSpace(c):
			l.scanWhile(unicode.IsSpace)
			l.emit(Whitespace, start)
		case c == '-' && l.peek(1) == '-':
			l.scanLineComment()
			l.emit(Comment, start)
		case c == '/' && l.peek(1) == '*':
			l.scanBlockComment()
			l.emit(Comment, start)
		case c == '\'' || c == '"' || c == '`':
			l.scanString(c)
			l.emit(String, start)
		case c == '[':
			// MSSQL-style bracketed identifier.
			l.scanBracketed()
			l.emit(String, start)
		case isIdentStart(c):
			l.scanWhile(isIdentCont)
			word := string(l.src[start:l.i])
			if IsKeyword(word) {
				l.emit(Keyword, start)
			} else {
				l.emit(Ident, start)
			}
		case isDigit(c):
			l.scanNumber()
			l.emit(Number, start)
		case isPunct(c):
			l.i++
			l.emit(Punct, start)
		case isOperator(c):
			l.scanOperator()
			l.emit(Operator, start)
		default:
			l.i++
			l.emit(Text, start)
		}
	}
	return l.tokens
}

func (l *lexer) peek(offset int) rune {
	if l.i+offset >= len(l.src) {
		return 0
	}
	return l.src[l.i+offset]
}

func (l *lexer) scanWhile(pred func(rune) bool) {
	for l.i < len(l.src) && pred(l.src[l.i]) {
		l.i++
	}
}

func (l *lexer) scanLineComment() {
	// Consume through end of line (or end of input).
	for l.i < len(l.src) && l.src[l.i] != '\n' {
		l.i++
	}
}

func (l *lexer) scanBlockComment() {
	// Consume "/*"
	l.i += 2
	for l.i < len(l.src)-1 {
		if l.src[l.i] == '*' && l.src[l.i+1] == '/' {
			l.i += 2
			return
		}
		l.i++
	}
	// Unclosed: swallow the rest.
	l.i = len(l.src)
}

func (l *lexer) scanString(quote rune) {
	l.i++ // opening quote
	for l.i < len(l.src) {
		c := l.src[l.i]
		if c == '\\' && l.i+1 < len(l.src) {
			// Escape sequence -- skip both chars. SQL-standard
			// escaping is '' but most dialects accept backslash too.
			l.i += 2
			continue
		}
		if c == quote {
			// Check for doubled quote (SQL standard escape).
			if l.i+1 < len(l.src) && l.src[l.i+1] == quote {
				l.i += 2
				continue
			}
			l.i++
			return
		}
		l.i++
	}
}

func (l *lexer) scanBracketed() {
	l.i++ // [
	for l.i < len(l.src) && l.src[l.i] != ']' {
		l.i++
	}
	if l.i < len(l.src) {
		l.i++ // ]
	}
}

func (l *lexer) scanNumber() {
	for l.i < len(l.src) {
		c := l.src[l.i]
		if isDigit(c) || c == '.' {
			l.i++
			continue
		}
		// Scientific notation: 1e10, 2.5E-3, etc.
		if (c == 'e' || c == 'E') && l.i+1 < len(l.src) {
			next := l.src[l.i+1]
			if isDigit(next) || next == '+' || next == '-' {
				l.i += 2
				continue
			}
		}
		break
	}
}

func (l *lexer) scanOperator() {
	// Consume a run of operator chars so `<=`, `<>`, `!=`, `||`
	// come out as a single token.
	for l.i < len(l.src) && isOperator(l.src[l.i]) {
		l.i++
	}
}

func (l *lexer) emit(kind Kind, start int) {
	l.tokens = append(l.tokens, Token{
		Kind:     kind,
		StartCol: start,
		EndCol:   l.i,
		Text:     string(l.src[start:l.i]),
	})
}

func isIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isIdentCont(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

func isPunct(r rune) bool {
	switch r {
	case ',', ';', '(', ')', '.':
		return true
	}
	return false
}

func isOperator(r rune) bool {
	switch r {
	case '+', '-', '*', '/', '%', '=', '<', '>', '!', '|', '&', '^', '~':
		return true
	}
	return false
}
