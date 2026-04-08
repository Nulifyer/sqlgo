package editor

// StatementRangeAt returns the byte range [start, end) of the SQL statement
// that contains the cursor position. Statements are split by `;` outside of
// strings, comments, and quoted identifiers. Leading whitespace inside the
// matched statement is trimmed from the returned start so the caller can hand
// the slice straight to the database driver.
//
// If the buffer is empty or contains no real statements, the function returns
// (0, 0). If the cursor sits past the final `;`, the last non-empty statement
// is returned.
func StatementRangeAt(text string, cursor int) (int, int) {
	if len(text) == 0 {
		return 0, 0
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(text) {
		cursor = len(text)
	}

	// Walk the tokenizer output to find the byte offset of every `;`
	// outside strings/comments. We rebuild positions ourselves because
	// the tokenizer does not retain offsets.
	tokens := tokenize(text)
	starts := []int{0}
	pos := 0
	for _, tok := range tokens {
		if tok.kind == tokenPunctuation && tok.text == ";" {
			starts = append(starts, pos+1)
		}
		pos += len(tok.text)
	}
	starts = append(starts, len(text))

	pickRange := func(s, e int) (int, int) {
		for s < e && isStatementWhitespace(text[s]) {
			s++
		}
		return s, e
	}

	for i := 0; i < len(starts)-1; i++ {
		s := starts[i]
		e := starts[i+1]
		if cursor >= s && cursor < e {
			return pickRange(s, e)
		}
	}

	// Cursor at end of buffer falls through to the final segment.
	for i := len(starts) - 2; i >= 0; i-- {
		s, e := pickRange(starts[i], starts[i+1])
		if s < e {
			return s, e
		}
	}
	return 0, 0
}

func isStatementWhitespace(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r'
}
