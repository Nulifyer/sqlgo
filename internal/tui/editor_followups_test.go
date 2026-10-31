package tui

import (
	"context"
	"math/rand"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestIncrementalSyntaxMatchesFreshAfterMixedEdits(t *testing.T) {
	e := newEditor()
	e.buf.ResetText("SELECT 'a\nb';\n/* c\ncomment */\nSELECT * FROM t;\n-- end")
	e.syntaxForDraw()
	rng := rand.New(rand.NewSource(41))
	fragments := []string{"'", "/*", "*/", "\n", "select x", "", "--", "表", "\n\n"}
	for step := 0; step < 200; step++ {
		// Several edits before analysis exercise dirty-range alignment after splices.
		for n := 0; n < 3; n++ {
			r1 := rng.Intn(e.buf.LineCount())
			r2 := min(e.buf.LineCount()-1, r1+rng.Intn(3))
			c1, c2 := rng.Intn(len(e.buf.Line(r1))+1), rng.Intn(len(e.buf.Line(r2))+1)
			if r1 == r2 && c1 > c2 {
				c1, c2 = c2, c1
			}
			e.buf.ReplaceRange(r1, c1, r2, c2, fragments[rng.Intn(len(fragments))])
		}
		e.syntaxForDraw()
		fresh := buildEditorSyntax(e.buf.Text())
		if !reflect.DeepEqual(e.syntax, fresh) {
			t.Fatalf("incremental syntax diverged at step %d, text=%q", step, e.buf.Text())
		}
	}
}

func TestAsyncSaveIdentitySeparatesTypingAndUndoBranches(t *testing.T) {
	s := newSession()
	s.editor.buf.InsertText("select 1")
	d := documentService{}
	snapshot, err := d.Snapshot(s, filepath.Join(t.TempDir(), "query.sql"))
	if err != nil {
		t.Fatal(err)
	}
	s.editor.buf.Insert(';')
	doc, err := d.Write(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	d.ApplySaved(s, doc)
	if !s.IsDirty() {
		t.Fatal("save cleared edits made after snapshot")
	}
	s.editor.buf.Undo()
	if s.IsDirty() || s.editor.buf.Text() != "select 1" {
		t.Fatal("undo could not reach saved snapshot")
	}
	s.editor.buf.Insert('!')
	if !s.IsDirty() {
		t.Fatal("new branch reused saved identity")
	}
	s.editor.buf.Undo()
	if s.IsDirty() {
		t.Fatal("undo on branch did not restore saved version")
	}
}

func TestLongUnicodeLayoutIsLazyAndMatchesDenseLayout(t *testing.T) {
	unit := "a\t表e\u0301👩‍💻\x01"
	line := []rune(strings.Repeat(unit, 4000))
	sparse := newTextLayout(line)
	if !sparse.sparse || len(sparse.points) != 1 || len(sparse.visual) != 0 {
		t.Fatal("long line eagerly allocated mappings")
	}
	sparse.display(0)
	if len(sparse.points) != 1 {
		t.Fatal("drawing first grapheme scanned full line")
	}
	// A dense reference over a short prefix shares the same grapheme semantics.
	dense := newTextLayout([]rune(strings.Repeat(unit, 10)))
	for col := len([]rune(strings.Repeat(unit, 10))) - 1; col >= 0; col-- {
		if sparse.column(col) != dense.column(col) || sparse.next(col) != dense.next(col) || sparse.prev(col) != dense.prev(col) {
			t.Fatalf("layout differs at rune %d", col)
		}
	}
	for cells := 0; cells < dense.column(len([]rune(strings.Repeat(unit, 10)))); cells++ {
		if sparse.runeColumn(cells) != dense.runeColumn(cells) {
			t.Fatalf("cell mapping differs at %d", cells)
		}
	}
	endCells := sparse.column(len(line))
	if sparse.runeColumn(endCells) != len(line) || len(sparse.points) > len(line)/sparseLayoutStride+1 {
		t.Fatal("sparse end mapping or index size incorrect")
	}
	e := newEditor()
	e.buf.ResetText(string(line))
	e.buf.MoveBufferStart()
	drawEditorForTest(t, e)
	if e.buf.Text() != string(line) || !e.syntaxLimited {
		t.Fatal("long-line controls lost contents or failed to report highlighting limit")
	}
}

func TestSearchBoundCancellationAndNoPartialReplaceAll(t *testing.T) {
	e := newEditor()
	original := strings.Repeat("a ", maxEditorSearchMatches+1)
	e.buf.ResetText(original)
	e.SetSearch("a")
	if !e.searchLimited || e.MatchCount() != maxEditorSearchMatches {
		t.Fatal("search result limit missing")
	}
	if e.ReplaceAll("x") != 0 || e.buf.Text() != original {
		t.Fatal("replace-all partially replaced capped results")
	}
	a := &app{asyncCh: make(chan func(*app), 4)}
	var replaceErr error
	e.replaceAllAsync(a, "x", func(_ int, err error) { replaceErr = err })
	documentCallback(t, a)(a)
	if replaceErr == nil || e.buf.Text() != original {
		t.Fatal("background replace-all accepted truncated matches")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if matches, _ := scanEditorMatches(ctx, e.buf.lines, "a", false, false); matches != nil {
		t.Fatal("canceled search ran")
	}
	e.SetSearch("absent")
	if e.searchLimited {
		t.Fatal("search cap survived a new complete search")
	}
}

func TestNextOccurrenceQueuesRequestsAndRejectsChangedSelections(t *testing.T) {
	m := newMainLayer()
	e := m.ensureActiveTab().editor
	e.buf.ResetText(strings.Repeat("foo\n", 1100))
	e.buf.MoveBufferStart()
	a := &app{layers: []Layer{m}, asyncCh: make(chan func(*app), 4)}
	e.addNextOccurrence(a) // select word synchronously
	for i := 0; i < 3; i++ {
		e.addNextOccurrence(a)
	}
	if !e.occurrenceBusy || e.occurrenceQueued != 2 {
		t.Fatal("occurrence requests not coalesced")
	}
	for i := 0; i < 3; i++ {
		documentCallback(t, a)(a)
	}
	if e.occurrenceBusy || len(e.extraCursors) != 3 {
		t.Fatalf("lost queued requests: %d extras", len(e.extraCursors))
	}
	e.addNextOccurrence(a)
	e.handleInsert(a, Key{Kind: KeyRight})
	after := e.navigation()
	documentCallback(t, a)(a)
	if !sameNavigation(after, e.navigation()) || e.occurrenceQueued != 0 {
		t.Fatal("stale occurrence result changed navigation")
	}
}

func TestCommentEmptyLineAndUnicodeSelectionUndo(t *testing.T) {
	e := newEditor()
	e.toggleLineComments()
	if e.buf.Text() != "-- " {
		t.Fatal("empty line could not be commented")
	}
	keyCtrl(e, 'z')
	if e.buf.Text() != "" {
		t.Fatal("comment undo failed")
	}
	e.buf.ResetText("表 表")
	e.buf.MoveBufferStart()
	keyCtrl(e, 'd')
	keyCtrl(e, 'd')
	e.handleInsert(nil, Key{Kind: KeyRune, Rune: '['})
	if e.buf.Text() != "[表] [表]" {
		t.Fatalf("Unicode surround=%q", e.buf.Text())
	}
	e.handleInsert(nil, Key{Kind: KeyBackspace})
	if e.buf.Text() != "[] []" {
		t.Fatalf("selected delete=%q", e.buf.Text())
	}
	keyCtrl(e, 'z')
	if e.buf.Text() != "[表] [表]" {
		t.Fatal("Unicode delete did not undo atomically")
	}
}

func TestMultipleCursorsRetainIndependentVerticalGoals(t *testing.T) {
	e := newEditor()
	e.buf.ResetText("abcdef\nx\nabcdef\nx\nabcdef")
	e.buf.SetCursor(0, 5)
	e.extraCursors = []bufferCursor{{row: 2, col: 3}}
	e.handleInsert(nil, Key{Kind: KeyDown})
	e.handleInsert(nil, Key{Kind: KeyDown})
	if e.buf.col != 5 || len(e.extraCursors) != 1 || e.extraCursors[0].col != 3 {
		t.Fatalf("vertical goals shared: %+v", e.selections())
	}
}

func TestSyntaxLimitPersistsAfterSuffixEditsAndRecoversWhenShortened(t *testing.T) {
	e := newEditor()
	e.buf.ResetText(strings.Repeat("x", maxSyntaxLineRunes+1) + "\nSELECT 1;\nSELECT 2;")
	e.syntaxForDraw()
	if !e.syntaxLimited {
		t.Fatal("long-line limit not applied")
	}
	e.buf.ReplaceRange(2, 7, 2, 8, "3")
	e.syntaxForDraw()
	if !e.syntax[2].limited || len(e.syntax[2].tokens) != 0 {
		t.Fatal("unknown lexical state was treated as valid")
	}
	e.buf.ReplaceRange(0, 0, 0, len(e.buf.Line(0)), "-- short")
	e.syntaxForDraw()
	if e.syntaxLimited || len(e.syntax[2].tokens) == 0 {
		t.Fatal("highlighting did not recover after shortening long line")
	}
}

func TestSingleSelectionSurroundRedoRetainsInnerSelection(t *testing.T) {
	e := newEditor()
	e.buf.ResetText("e\u0301表")
	e.buf.SelectAll()
	e.handleInsert(nil, Key{Kind: KeyRune, Rune: '\''})
	if e.buf.Text() != "'e\u0301表'" || e.buf.Selection() != "e\u0301表" {
		t.Fatal("single surround lost selection")
	}
	keyCtrl(e, 'z')
	keyCtrl(e, 'y')
	if e.buf.Selection() != "e\u0301表" {
		t.Fatal("redo lost inner selection")
	}
}

func TestSearchLinearMatcherAgreesWithNaiveUnicodeReference(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	alphabet := []rune("aaAb B表éÉ_")
	for trial := 0; trial < 300; trial++ {
		line := make([]rune, rng.Intn(150))
		for i := range line {
			line[i] = alphabet[rng.Intn(len(alphabet))]
		}
		pattern := make([]rune, 1+rng.Intn(6))
		for i := range pattern {
			pattern[i] = alphabet[rng.Intn(len(alphabet))]
		}
		for _, matchCase := range []bool{false, true} {
			for _, wholeWord := range []bool{false, true} {
				var want []matchRange
				for col := 0; col+len(pattern) <= len(line); col++ {
					match := true
					for j, r := range pattern {
						left, right := line[col+j], r
						if !matchCase {
							left, right = toLowerRune(left), toLowerRune(right)
						}
						if left != right {
							match = false
							break
						}
					}
					if match && wholeWord {
						match = (col == 0 || !isWordChar(line[col-1])) && (col+len(pattern) == len(line) || !isWordChar(line[col+len(pattern)]))
					}
					if match {
						want = append(want, matchRange{row: 0, col: col, length: len(pattern)})
						col += len(pattern) - 1
					}
				}
				got, limited := scanEditorMatches(context.Background(), [][]rune{line}, string(pattern), matchCase, wholeWord)
				if limited || !reflect.DeepEqual(got, want) {
					t.Fatalf("search differs for %q / %q, case=%v word=%v: got %+v want %+v", string(line), string(pattern), matchCase, wholeWord, got, want)
				}
			}
		}
	}
}

func BenchmarkSavedIdentityDirtyCheck(b *testing.B) {
	s := newSession()
	s.editor.buf.ResetText(strings.Repeat("SELECT 1;\n", 100000))
	s.setSavedIdentity(s.editor.buf.Text(), s.editor.buf.stateID)
	s.editor.buf.Insert(';')
	s.editor.buf.textCached = false
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !s.IsDirty() {
			b.Fatal("lost dirty identity")
		}
	}
}

func BenchmarkIncrementalSyntaxStableSuffix(b *testing.B) {
	text := strings.Repeat("SELECT 1;\n", 10000)
	old := buildEditorSyntax(text)
	updated := strings.Replace(text, "SELECT 1;", "SELECT 2;", 1)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, count := updateEditorSyntax(context.Background(), updated, old, 0, 0)
		if count != 1 {
			b.Fatal(count)
		}
	}
}

func BenchmarkLongUnicodeLayoutFirstViewport(b *testing.B) {
	line := []rune(strings.Repeat("表e\u0301👩‍💻\t", 100000))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l := newTextLayout(line)
		for col, cells := 0, 0; cells < 80; {
			_, width, next := l.display(col)
			col = next
			cells += width
		}
	}
}

func TestTerminalBytesReachCommentAndCursorUndoCommands(t *testing.T) {
	e := newEditor()
	e.buf.ResetText("SELECT 1;")
	reader := newKeyReader(strings.NewReader("\x1f\x1f\x1b[D\x15\x1bu"))
	for i := 0; i < 5; i++ {
		msg, err := reader.Read()
		if err != nil {
			t.Fatal(err)
		}
		key, ok := msg.(Key)
		if !ok {
			t.Fatalf("unexpected input %T", msg)
		}
		e.handleInsert(nil, key)
		if i == 0 && e.buf.Text() != "-- SELECT 1;" {
			t.Fatal("terminal Ctrl+/ did not toggle comments")
		}
	}
	if e.buf.Text() != "SELECT 1;" || e.buf.col != 8 {
		t.Fatalf("terminal cursor undo/redo failed: %q col=%d", e.buf.Text(), e.buf.col)
	}
}

func TestBulkSelectionCommandRunsAsyncAndRejectsNewerEdits(t *testing.T) {
	for _, change := range []bool{false, true} {
		t.Run(map[bool]string{false: "apply and undo", true: "stale edit"}[change], func(t *testing.T) {
			m := newMainLayer()
			e := m.ensureActiveTab().editor
			original := strings.Repeat("  SELECT 1;\n", 1100)
			e.buf.ResetText(original)
			e.buf.SelectAll()
			a := &app{layers: []Layer{m}, asyncCh: make(chan func(*app), 4)}
			e.handleInsert(a, Key{Kind: KeyRune, Rune: '/', Alt: true})
			if !e.commandBusy || e.buf.Text() != original {
				t.Fatal("bulk comment command ran on event loop")
			}
			if change {
				e.handleInsert(a, Key{Kind: KeyEnd})
				e.handleInsert(a, Key{Kind: KeyRune, Rune: '!'})
			}
			documentCallback(t, a)(a)
			if e.commandBusy {
				t.Fatal("command progress did not clear")
			}
			if change {
				if !strings.HasSuffix(e.buf.Text(), "!") || strings.Contains(e.buf.Text(), "-- ") {
					t.Fatal("stale command overwrote newer edits")
				}
				return
			}
			if !strings.HasPrefix(e.buf.Text(), "  -- SELECT 1;") {
				t.Fatal("background comments missing")
			}
			keyCtrl(e, 'z')
			if e.buf.Text() != original || e.buf.Selection() != original {
				t.Fatal("bulk undo lost text or selection")
			}
			keyCtrl(e, 'y')
			if !strings.HasPrefix(e.buf.Text(), "  -- SELECT 1;") || !e.buf.HasSelection() {
				t.Fatal("bulk redo lost text or selection")
			}
		})
	}
}

func TestBulkCommandRejectsNavigationAndTabSwitch(t *testing.T) {
	for _, switchTab := range []bool{false, true} {
		t.Run(map[bool]string{false: "navigation", true: "tab switch"}[switchTab], func(t *testing.T) {
			m := newMainLayer()
			s := m.ensureActiveTab()
			e := s.editor
			original := strings.Repeat("foo\n", 1100)
			e.buf.ResetText(original)
			e.buf.SelectAll()
			a := &app{layers: []Layer{m}, asyncCh: make(chan func(*app), 4)}
			e.handleInsert(a, Key{Kind: KeyRune, Rune: '/', Alt: true})
			if switchTab {
				m.sessions = append(m.sessions, newSession())
				m.switchTab(1)
			} else {
				e.handleInsert(a, Key{Kind: KeyHome})
			}
			state := e.navigation()
			documentCallback(t, a)(a)
			if e.buf.Text() != original || !sameNavigation(state, e.navigation()) {
				t.Fatal("stale command changed text or selection")
			}
		})
	}
}
