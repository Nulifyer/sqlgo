package tui

import "github.com/Nulifyer/sqlgo/internal/sqltok"

type editorSyntaxLine struct {
	tokens               []sqltok.Token
	end                  sqltok.LineState
	tableStart, tableEnd int
}

func syntaxLine(line []rune, state sqltok.LineState, table int) editorSyntaxLine {
	tokens, end := sqltok.TokenizeLineState(line, state)
	return editorSyntaxLine{tokens: tokens, end: end, tableStart: table, tableEnd: advanceTableState(table, tokens)}
}
func buildEditorSyntax(text string) []editorSyntaxLine {
	lines := splitBufferText(text)
	out := make([]editorSyntaxLine, len(lines))
	state := sqltok.LineState{}
	table := tblNormal
	for row, line := range lines {
		out[row] = syntaxLine(line, state, table)
		state, table = out[row].end, out[row].tableEnd
	}
	return out
}
func (e *editor) syntaxForDraw() {
	rev := e.buf.Revision()
	if e.syntaxRevision == rev && len(e.syntax) == e.buf.LineCount() {
		return
	}
	if e.largeAnalysis() {
		return
	} // The app schedules large-document analysis off the event loop.
	start := max(0, e.buf.dirtyFrom)
	if len(e.syntax) != e.buf.LineCount() || start > len(e.syntax) {
		start = 0
		e.syntax = make([]editorSyntaxLine, e.buf.LineCount())
	}
	state := sqltok.LineState{}
	table := tblNormal
	if start > 0 {
		state = e.syntax[start-1].end
		table = e.syntax[start-1].tableEnd
	}
	for row := start; row < e.buf.LineCount(); row++ {
		e.syntax[row] = syntaxLine(e.buf.Line(row), state, table)
		state, table = e.syntax[row].end, e.syntax[row].tableEnd
	}
	e.syntaxRevision = rev
	e.buf.dirtyFrom = -1
}
func (e *editor) scheduleSyntax(a *app) {
	if !e.largeAnalysis() || e.syntaxBusy || (e.syntaxRevision == e.buf.Revision() && len(e.syntax) == e.buf.LineCount()) {
		return
	}
	rev := e.buf.Revision()
	text := e.buf.Text()
	e.syntaxBusy = true
	a.runBackground(func() func(*app) {
		syntax := buildEditorSyntax(text)
		return func(a *app) {
			e.syntaxBusy = false
			if e.buf.Revision() != rev {
				return
			}
			e.syntax = syntax
			e.syntaxRevision = rev
			e.buf.dirtyFrom = -1
		}
	})
}

func (e *editor) largeAnalysis() bool { return e.buf.LineCount() > 1000 || len(e.buf.Text()) > 128<<10 }
