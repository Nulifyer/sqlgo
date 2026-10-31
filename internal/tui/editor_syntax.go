package tui

import (
	"context"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

const (
	maxAutomaticAnalysisBytes = 8 << 20
	maxAutomaticAnalysisLines = 100000
	maxSyntaxLineRunes        = 32768
)

type editorSyntaxLine struct {
	text                 string
	tokens               []sqltok.Token
	start, end           sqltok.LineState
	tableStart, tableEnd int
	limited              bool
}

func syntaxLine(line []rune, state sqltok.LineState, table int) editorSyntaxLine {
	tokens, end := sqltok.TokenizeLineState(line, state)
	return editorSyntaxLine{text: string(line), tokens: tokens, start: state, end: end, tableStart: table, tableEnd: advanceTableState(table, tokens)}
}
func buildEditorSyntax(text string) []editorSyntaxLine {
	result, _ := updateEditorSyntax(context.Background(), text, nil, 0, 0)
	return result
}

// Old entries and their token slices are immutable. Workers only build a new
// result, so state-stable suffixes can be reused without racing the renderer.
func updateEditorSyntax(ctx context.Context, text string, old []editorSyntaxLine, from, through int) ([]editorSyntaxLine, int) {
	if ctx.Err() != nil {
		return nil, 0
	}
	lines := strings.Split(text, "\n")
	out := make([]editorSyntaxLine, len(lines))
	start := max(0, min(from, min(len(old), len(lines))))
	copy(out, old[:start])
	state, table := sqltok.LineState{}, tblNormal
	if start > 0 {
		state, table = old[start-1].end, old[start-1].tableEnd
		if old[start-1].limited {
			for row := start; row < len(lines); row++ {
				if ctx.Err() != nil {
					return nil, 0
				}
				out[row] = editorSyntaxLine{text: lines[row], limited: true}
			}
			return out, 0
		}
	}
	delta := len(lines) - len(old)
	computed := 0
	sliceStart := time.Now()
	for row := start; row < len(lines); row++ {
		if ctx.Err() != nil {
			return nil, computed
		}
		previous := row - delta
		if row > through && previous >= 0 && previous < len(old) && !old[previous].limited && old[previous].text == lines[row] && old[previous].start == state && old[previous].tableStart == table && len(out)-row == len(old)-previous {
			copy(out[row:], old[previous:])
			break
		}
		// A huge line can contain an unterminated construct. Leave its suffix plain
		// rather than claiming accurate lexical state without analyzing that line.
		if len(lines[row]) > maxSyntaxLineRunes && utf8.RuneCountInString(lines[row]) > maxSyntaxLineRunes {
			for rest := row; rest < len(lines); rest++ {
				if rest%128 == 0 && ctx.Err() != nil {
					return nil, computed
				}
				out[rest] = editorSyntaxLine{text: lines[rest], limited: true}
			}
			break
		}
		out[row] = syntaxLine([]rune(lines[row]), state, table)
		computed++
		state, table = out[row].end, out[row].tableEnd
		if computed%128 == 0 || time.Since(sliceStart) >= 2*time.Millisecond {
			runtime.Gosched()
			sliceStart = time.Now()
		}
	}
	return out, computed
}
func (e *editor) automaticAnalysisLimited() bool {
	return e.buf.textBytes > maxAutomaticAnalysisBytes || e.buf.LineCount() > maxAutomaticAnalysisLines
}
func (e *editor) installSyntax(rev uint64, syntax []editorSyntaxLine) {
	e.syntax, e.syntaxRevision = syntax, rev
	e.buf.dirtyFrom, e.buf.dirtyTo = -1, -1
	e.syntaxLimited = len(syntax) > 0 && syntax[len(syntax)-1].limited
}
func (e *editor) syntaxForDraw() {
	rev := e.buf.Revision()
	if e.syntaxRevision == rev && len(e.syntax) == e.buf.LineCount() {
		return
	}
	if e.largeAnalysis() || e.automaticAnalysisLimited() {
		return
	}
	syntax, _ := updateEditorSyntax(context.Background(), e.buf.Text(), e.syntax, e.buf.dirtyFrom, e.buf.dirtyTo)
	e.installSyntax(rev, syntax)
}
func (e *editor) scheduleSyntax(a *app) {
	if e.automaticAnalysisLimited() || !e.largeAnalysis() || e.syntaxBusy || (e.syntaxRevision == e.buf.Revision() && len(e.syntax) == e.buf.LineCount()) {
		return
	}
	rev, text := e.buf.Revision(), e.buf.Text()
	old, from, through := e.syntax, e.buf.dirtyFrom, e.buf.dirtyTo
	ctx, cancel := context.WithCancel(context.Background())
	e.syntaxCancel = cancel
	e.syntaxBusy = true
	a.runBackground(func() func(*app) {
		defer cancel()
		syntax, _ := updateEditorSyntax(ctx, text, old, from, through)
		return func(a *app) {
			e.syntaxBusy = false
			e.syntaxCancel = nil
			if syntax == nil || e.buf.Revision() != rev {
				return
			}
			e.installSyntax(rev, syntax)
		}
	})
}
func (e *editor) largeAnalysis() bool { return e.buf.LineCount() > 1000 || e.buf.textBytes > 128<<10 }
