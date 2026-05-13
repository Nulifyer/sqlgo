package tui

import "strings"

func sanitizeEditorPasteText(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\t', '\n', '\r':
			b.WriteRune(r)
			continue
		}
		if isPasteControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isPasteControl(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}
