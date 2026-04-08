package editor

import "strings"

type HighlightStyle int

const (
	HighlightStylePlain HighlightStyle = iota
	HighlightStyleComment
	HighlightStyleString
	HighlightStyleQuotedIdentifier
	HighlightStyleNumber
	HighlightStyleKeyword
)

type HighlightSpan struct {
	Text  string
	Style HighlightStyle
}

func HighlightSpans(input string) []HighlightSpan {
	tokens := tokenize(input)
	if len(tokens) == 0 {
		return nil
	}

	spans := make([]HighlightSpan, 0, len(tokens))
	for _, tok := range tokens {
		style := HighlightStylePlain
		switch tok.kind {
		case tokenComment:
			style = HighlightStyleComment
		case tokenString:
			style = HighlightStyleString
		case tokenQuotedIdentifier:
			style = HighlightStyleQuotedIdentifier
		case tokenNumber:
			style = HighlightStyleNumber
		case tokenWord:
			if _, ok := keywords[strings.ToUpper(tok.text)]; ok {
				style = HighlightStyleKeyword
			}
		}
		spans = append(spans, HighlightSpan{Text: tok.text, Style: style})
	}
	return spans
}
