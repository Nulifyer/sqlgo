package sqltok

// LineState is the lexical context carried across display lines. It is a value
// so editors can cache it and stop invalidation when the state stabilizes.
type LineState struct {
	Quote      rune
	BlockDepth int
	DollarTag  string
	Bracket    bool
}

// TokenizeLineState shares the whole-text lexer's rules without requiring the
// renderer to concatenate or re-lex the entire document for each frame.
func TokenizeLineState(line []rune, state LineState) ([]Token, LineState) {
	l := &lexer{src: line, state: state}
	tokens := l.run()
	return tokens, l.state
}
