package widget

// JoinHints concatenates non-empty pieces with two spaces between
// them. Empty strings are dropped so callers can write HintIf
// helpers and pass their results straight in.
func JoinHints(parts ...string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out == "" {
			out = p
		} else {
			out += "  " + p
		}
	}
	return out
}

// HintIf returns h when cond is true, "" otherwise. Keeps branches in
// Hints builders readable.
func HintIf(cond bool, h string) string {
	if cond {
		return h
	}
	return ""
}
