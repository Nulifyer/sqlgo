package sqltok

import "strings"

// SplitStatements separates semicolon-delimited statements at parenthesis
// depth zero. Semicolons inside strings, quoted identifiers, dollar-quoted
// bodies, and comments remain part of their containing statement.
func SplitStatements(src string) []string {
	tokens := TokenizeText(src)
	if len(tokens) == 0 {
		return nil
	}
	runes := []rune(src)
	statements := make([]string, 0, 4)
	startRune := 0
	startToken := 0
	depth := 0
	emit := func(endRune, endToken int) {
		if !hasStatementContent(tokens[startToken:endToken]) {
			return
		}
		statement := strings.TrimSpace(string(runes[startRune:endRune]))
		if statement != "" {
			statements = append(statements, statement)
		}
	}

	for i, token := range tokens {
		if token.Kind != Punct {
			continue
		}
		switch token.Text {
		case "(":
			depth++
		case ")":
			if depth > 0 {
				depth--
			}
		case ";":
			if depth == 0 {
				emit(token.EndCol, i)
				startRune = token.EndCol
				startToken = i + 1
			}
		}
	}
	emit(len(runes), len(tokens))
	return statements
}

func hasStatementContent(tokens []Token) bool {
	for _, token := range tokens {
		if token.Kind != Whitespace && token.Kind != Comment {
			return true
		}
	}
	return false
}
