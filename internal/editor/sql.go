package editor

import (
	"strings"
	"unicode"

	"github.com/rivo/tview"
)

const indentUnit = "    "

type tokenKind int

const (
	tokenWord tokenKind = iota
	tokenNumber
	tokenString
	tokenQuotedIdentifier
	tokenComment
	tokenOperator
	tokenPunctuation
)

type token struct {
	kind tokenKind
	text string
}

var keywords = map[string]struct{}{
	"ADD": {}, "ALL": {}, "ALTER": {}, "AND": {}, "AS": {}, "ASC": {}, "BETWEEN": {},
	"BY": {}, "CASE": {}, "CAST": {}, "CREATE": {}, "CROSS": {}, "CURRENT_DATE": {},
	"CURRENT_TIME": {}, "CURRENT_TIMESTAMP": {}, "DATABASE": {}, "DEFAULT": {},
	"DELETE": {}, "DESC": {}, "DISTINCT": {}, "DROP": {}, "ELSE": {}, "END": {},
	"EXISTS": {}, "FROM": {}, "FULL": {}, "GROUP": {}, "HAVING": {}, "IN": {},
	"INDEX": {}, "INNER": {}, "INSERT": {}, "INTO": {}, "IS": {}, "JOIN": {},
	"LEFT": {}, "LIKE": {}, "LIMIT": {}, "NOT": {}, "NULL": {}, "OFFSET": {},
	"ON": {}, "OR": {}, "ORDER": {}, "OUTER": {}, "PRIMARY": {}, "RIGHT": {},
	"SELECT": {}, "SET": {}, "TABLE": {}, "THEN": {}, "TOP": {}, "UNION": {},
	"UPDATE": {}, "USING": {}, "VALUES": {}, "VIEW": {}, "WHEN": {}, "WHERE": {},
	"WITH": {},
}

func FormatSQL(input string) string {
	tokens := tokenize(input)
	if len(tokens) == 0 {
		return ""
	}

	var b strings.Builder
	lineStart := true
	indent := 0
	listIndent := 0
	parenDepth := 0
	var nestedBlockStack []bool
	inSelectList := false
	inSetList := false
	inValuesList := false

	writeIndent := func() {
		if !lineStart {
			return
		}
		b.WriteString(strings.Repeat(indentUnit, indent))
		lineStart = false
	}

	newLine := func() {
		trimTrailingSpaces(&b)
		if b.Len() == 0 {
			return
		}
		if !strings.HasSuffix(b.String(), "\n") {
			b.WriteByte('\n')
		}
		lineStart = true
	}

	writeToken := func(text string) {
		writeIndent()
		b.WriteString(text)
	}

	writeSpace := func() {
		if lineStart || b.Len() == 0 {
			return
		}
		last := b.String()[b.Len()-1]
		if last != ' ' && last != '\n' && last != '(' && last != '.' {
			b.WriteByte(' ')
		}
	}

	for i := 0; i < len(tokens); i++ {
		if matched, size, replacement := clauseAt(tokens, i); matched {
			baseIndent := parenDepth
			switch replacement {
			case "SELECT":
				if b.Len() > 0 {
					newLine()
				}
				indent = baseIndent
				inSelectList, inSetList, inValuesList = true, false, false
				listIndent = indent + 1
			case "SET":
				newLine()
				indent = baseIndent + 1
				inSelectList, inSetList, inValuesList = false, true, false
				listIndent = indent
			case "VALUES":
				newLine()
				indent = baseIndent + 1
				inSelectList, inSetList, inValuesList = false, false, true
				listIndent = indent
			case "FROM", "WHERE", "GROUP BY", "HAVING", "ORDER BY", "LIMIT", "OFFSET", "UNION", "UNION ALL":
				newLine()
				indent = baseIndent
				inSelectList, inSetList, inValuesList = false, false, false
			case "LEFT JOIN", "RIGHT JOIN", "INNER JOIN", "FULL JOIN", "CROSS JOIN", "JOIN":
				if b.Len() > 0 {
					newLine()
				}
				indent = baseIndent + 1
				inSelectList, inSetList, inValuesList = false, false, false
			case "ON":
				newLine()
				indent = baseIndent + 2
				inSelectList, inSetList, inValuesList = false, false, false
			case "INSERT INTO", "UPDATE", "DELETE FROM", "CREATE TABLE", "ALTER TABLE", "DROP TABLE":
				if b.Len() > 0 {
					newLine()
				}
				indent = baseIndent
				inSelectList, inSetList, inValuesList = false, false, false
			case "WITH":
				if b.Len() > 0 {
					newLine()
				}
				indent = baseIndent
			}

			writeToken(replacement)
			i += size - 1
			writeSpace()
			continue
		}

		tok := tokens[i]
		if isWhitespaceToken(tok) {
			if strings.Contains(tok.text, "\n") && !lineStart {
				newLine()
			} else {
				writeSpace()
			}
			continue
		}

		switch tok.kind {
		case tokenComment:
			if !lineStart {
				newLine()
			}
			writeToken(strings.TrimRight(tok.text, " "))
			newLine()
		case tokenString, tokenQuotedIdentifier:
			writeSpace()
			writeToken(tok.text)
		case tokenWord:
			writeSpace()
			writeToken(normalizeKeyword(tok.text))
		case tokenNumber:
			writeSpace()
			writeToken(tok.text)
		case tokenOperator:
			writeSpace()
			writeToken(tok.text)
			writeSpace()
		case tokenPunctuation:
			switch tok.text {
			case ",":
				writeToken(",")
				if inSelectList || inSetList || inValuesList {
					newLine()
					indent = listIndent
				} else {
					writeSpace()
				}
			case "(":
				nestedBlock := opensNestedBlock(tokens, i)
				if needsSpaceBeforeParen(i, tokens) {
					writeSpace()
				}
				writeToken("(")
				parenDepth++
				nestedBlockStack = append(nestedBlockStack, nestedBlock)
				if nestedBlock {
					newLine()
				}
			case ")":
				nestedBlock := false
				if len(nestedBlockStack) > 0 {
					nestedBlock = nestedBlockStack[len(nestedBlockStack)-1]
					nestedBlockStack = nestedBlockStack[:len(nestedBlockStack)-1]
				}
				parenDepth = max(parenDepth-1, 0)
				if nestedBlock && !lineStart {
					newLine()
				}
				if strings.HasSuffix(b.String(), "\n") {
					indent = parenDepth
					writeIndent()
				}
				writeToken(")")
			case ";":
				writeToken(";")
				newLine()
				inSelectList, inSetList, inValuesList = false, false, false
				indent = 0
				listIndent = 0
			case ".":
				writeToken(".")
			default:
				writeToken(tok.text)
			}
		}
	}

	return strings.TrimSpace(b.String())
}

func NextLineIndent(text string, cursor int) string {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(text) {
		cursor = len(text)
	}

	lineStart := strings.LastIndex(text[:cursor], "\n") + 1
	linePrefix := text[lineStart:cursor]
	leading := leadingIndentation(linePrefix)
	trimmed := strings.TrimSpace(linePrefix)
	if trimmed == "" {
		return leading
	}

	indent := leading
	if shouldIncreaseNextLineIndent(trimmed) {
		indent += indentUnit
	}
	return indent
}

func HighlightSQL(input string) string {
	tokens := tokenize(input)
	if len(tokens) == 0 {
		return "[gray]-- empty editor --[-]"
	}

	var b strings.Builder
	for _, tok := range tokens {
		switch tok.kind {
		case tokenComment:
			b.WriteString("[gray]")
			b.WriteString(tview.Escape(tok.text))
			b.WriteString("[-]")
		case tokenString:
			b.WriteString("[green]")
			b.WriteString(tview.Escape(tok.text))
			b.WriteString("[-]")
		case tokenQuotedIdentifier:
			b.WriteString("[teal]")
			b.WriteString(tview.Escape(tok.text))
			b.WriteString("[-]")
		case tokenNumber:
			b.WriteString("[blue]")
			b.WriteString(tview.Escape(tok.text))
			b.WriteString("[-]")
		case tokenWord:
			upper := strings.ToUpper(tok.text)
			if _, ok := keywords[upper]; ok {
				b.WriteString("[yellow::b]")
				b.WriteString(tview.Escape(upper))
				b.WriteString("[-:-:-]")
			} else {
				b.WriteString(tview.Escape(tok.text))
			}
		default:
			b.WriteString(tview.Escape(tok.text))
		}
	}
	return b.String()
}

func tokenize(input string) []token {
	var tokens []token
	for i := 0; i < len(input); {
		ch := input[i]

		if isSpace(ch) {
			start := i
			for i < len(input) && isSpace(input[i]) {
				i++
			}
			tokens = append(tokens, token{kind: tokenPunctuation, text: input[start:i]})
			continue
		}

		if ch == '-' && i+1 < len(input) && input[i+1] == '-' {
			start := i
			i += 2
			for i < len(input) && input[i] != '\n' {
				i++
			}
			tokens = append(tokens, token{kind: tokenComment, text: input[start:i]})
			continue
		}

		if ch == '/' && i+1 < len(input) && input[i+1] == '*' {
			start := i
			i += 2
			for i+1 < len(input) && !(input[i] == '*' && input[i+1] == '/') {
				i++
			}
			if i+1 < len(input) {
				i += 2
			}
			tokens = append(tokens, token{kind: tokenComment, text: input[start:i]})
			continue
		}

		if ch == '\'' {
			start := i
			i++
			for i < len(input) {
				if input[i] == '\'' {
					i++
					if i < len(input) && input[i] == '\'' {
						i++
						continue
					}
					break
				}
				i++
			}
			tokens = append(tokens, token{kind: tokenString, text: input[start:i]})
			continue
		}

		if ch == '"' {
			start := i
			i++
			for i < len(input) {
				if input[i] == '"' {
					i++
					if i < len(input) && input[i] == '"' {
						i++
						continue
					}
					break
				}
				i++
			}
			tokens = append(tokens, token{kind: tokenQuotedIdentifier, text: input[start:i]})
			continue
		}

		if ch == '[' {
			start := i
			i++
			for i < len(input) && input[i] != ']' {
				i++
			}
			if i < len(input) {
				i++
			}
			tokens = append(tokens, token{kind: tokenQuotedIdentifier, text: input[start:i]})
			continue
		}

		if isWordStart(ch) {
			start := i
			i++
			for i < len(input) && isWordPart(input[i]) {
				i++
			}
			tokens = append(tokens, token{kind: tokenWord, text: input[start:i]})
			continue
		}

		if isDigit(ch) {
			start := i
			i++
			for i < len(input) && (isDigit(input[i]) || input[i] == '.') {
				i++
			}
			tokens = append(tokens, token{kind: tokenNumber, text: input[start:i]})
			continue
		}

		if strings.ContainsRune("=<>!+-*/%", rune(ch)) {
			start := i
			i++
			if i < len(input) && strings.ContainsRune("=<>", rune(input[i])) {
				i++
			}
			tokens = append(tokens, token{kind: tokenOperator, text: input[start:i]})
			continue
		}

		tokens = append(tokens, token{kind: tokenPunctuation, text: input[i : i+1]})
		i++
	}
	return mergeWhitespace(tokens)
}

func mergeWhitespace(tokens []token) []token {
	if len(tokens) == 0 {
		return nil
	}
	merged := make([]token, 0, len(tokens))
	for _, tok := range tokens {
		if tok.kind == tokenPunctuation && strings.TrimSpace(tok.text) == "" {
			if len(merged) == 0 {
				continue
			}
			if merged[len(merged)-1].kind == tokenPunctuation && strings.TrimSpace(merged[len(merged)-1].text) == "" {
				merged[len(merged)-1].text += tok.text
				continue
			}
		}
		merged = append(merged, tok)
	}
	return merged
}

func clauseAt(tokens []token, index int) (bool, int, string) {
	clauses := []struct {
		words []string
		text  string
	}{
		{[]string{"UNION", "ALL"}, "UNION ALL"},
		{[]string{"GROUP", "BY"}, "GROUP BY"},
		{[]string{"ORDER", "BY"}, "ORDER BY"},
		{[]string{"INSERT", "INTO"}, "INSERT INTO"},
		{[]string{"DELETE", "FROM"}, "DELETE FROM"},
		{[]string{"CREATE", "TABLE"}, "CREATE TABLE"},
		{[]string{"ALTER", "TABLE"}, "ALTER TABLE"},
		{[]string{"DROP", "TABLE"}, "DROP TABLE"},
		{[]string{"LEFT", "JOIN"}, "LEFT JOIN"},
		{[]string{"RIGHT", "JOIN"}, "RIGHT JOIN"},
		{[]string{"INNER", "JOIN"}, "INNER JOIN"},
		{[]string{"FULL", "JOIN"}, "FULL JOIN"},
		{[]string{"CROSS", "JOIN"}, "CROSS JOIN"},
		{[]string{"SELECT"}, "SELECT"},
		{[]string{"FROM"}, "FROM"},
		{[]string{"WHERE"}, "WHERE"},
		{[]string{"HAVING"}, "HAVING"},
		{[]string{"LIMIT"}, "LIMIT"},
		{[]string{"OFFSET"}, "OFFSET"},
		{[]string{"UPDATE"}, "UPDATE"},
		{[]string{"SET"}, "SET"},
		{[]string{"VALUES"}, "VALUES"},
		{[]string{"JOIN"}, "JOIN"},
		{[]string{"ON"}, "ON"},
		{[]string{"UNION"}, "UNION"},
		{[]string{"WITH"}, "WITH"},
	}
	for _, clause := range clauses {
		if phraseMatches(tokens, index, clause.words...) {
			return true, phraseTokenSpan(tokens, index, len(clause.words)), clause.text
		}
	}
	return false, 0, ""
}

func phraseMatches(tokens []token, index int, words ...string) bool {
	j := index
	for _, word := range words {
		for j < len(tokens) && isWhitespaceToken(tokens[j]) {
			j++
		}
		if j >= len(tokens) || tokens[j].kind != tokenWord || strings.ToUpper(tokens[j].text) != word {
			return false
		}
		j++
	}
	return true
}

func phraseTokenSpan(tokens []token, index, words int) int {
	j := index
	seen := 0
	for j < len(tokens) && seen < words {
		if tokens[j].kind == tokenWord {
			seen++
		}
		j++
	}
	return j - index
}

func normalizeKeyword(word string) string {
	upper := strings.ToUpper(word)
	if _, ok := keywords[upper]; ok {
		return upper
	}
	return word
}

func needsSpaceBeforeParen(index int, tokens []token) bool {
	if index == 0 {
		return false
	}
	prev := previousNonWhitespace(tokens, index-1)
	if prev < 0 {
		return false
	}
	return tokens[prev].kind == tokenWord && !isFunctionLikeWord(tokens[prev].text)
}

func previousNonWhitespace(tokens []token, index int) int {
	for index >= 0 {
		if !isWhitespaceToken(tokens[index]) {
			return index
		}
		index--
	}
	return -1
}

func isWhitespaceToken(tok token) bool {
	return tok.kind == tokenPunctuation && strings.TrimSpace(tok.text) == ""
}

func isFunctionLikeWord(word string) bool {
	switch strings.ToUpper(word) {
	case "COUNT", "SUM", "AVG", "MIN", "MAX", "COALESCE", "CAST", "UPPER", "LOWER":
		return true
	default:
		return false
	}
}

func isClauseKeyword(tok token, words ...string) bool {
	if tok.kind != tokenWord {
		return false
	}
	for _, word := range words {
		if strings.EqualFold(tok.text, word) {
			return true
		}
	}
	return false
}

func opensNestedBlock(tokens []token, index int) bool {
	next := nextNonWhitespace(tokens, index+1)
	if next < 0 {
		return false
	}
	return isClauseKeyword(tokens[next], "SELECT", "WITH")
}

func nextNonWhitespace(tokens []token, index int) int {
	for index < len(tokens) {
		if !isWhitespaceToken(tokens[index]) {
			return index
		}
		index++
	}
	return -1
}

func leadingIndentation(line string) string {
	end := 0
	for end < len(line) {
		if line[end] != ' ' && line[end] != '\t' {
			break
		}
		end++
	}
	return line[:end]
}

func shouldIncreaseNextLineIndent(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}

	trimmed = strings.TrimRight(trimmed, " \t")
	if strings.HasSuffix(trimmed, "(") {
		return true
	}

	switch strings.ToUpper(trimmed) {
	case "SELECT", "FROM", "WHERE", "GROUP BY", "ORDER BY", "HAVING", "VALUES", "SET", "WITH", "ON",
		"JOIN", "LEFT JOIN", "RIGHT JOIN", "INNER JOIN", "FULL JOIN", "CROSS JOIN":
		return true
	default:
		return false
	}
}

func trimTrailingSpaces(b *strings.Builder) {
	text := strings.TrimRight(b.String(), " ")
	b.Reset()
	b.WriteString(text)
}

func isSpace(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r'
}

func isDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

func isWordStart(ch byte) bool {
	r := rune(ch)
	return ch == '_' || unicode.IsLetter(r)
}

func isWordPart(ch byte) bool {
	r := rune(ch)
	return ch == '_' || ch == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
