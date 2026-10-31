package tui

import (
	"math/rand/v2"
	"testing"
)

// Exercise overlapping, multiline and Unicode edits against an independent
// flat-rune model, then replay the entire transaction in both directions.
func TestRangeTransactionsAgainstFlatModel(t *testing.T) {
	rng := rand.New(rand.NewPCG(17, 42))
	for trial := 0; trial < 150; trial++ {
		b := newBuffer()
		initial := "SELECT 'é😀';\nsecond line\n"
		b.ResetText(initial)
		b.SetAnchor(0, 0)
		b.SetCursor(0, 6)
		before := b.cursorState()
		model := []rune(initial)
		b.Transaction(func() {
			for step := 0; step < 25; step++ {
				a, z := rng.IntN(len(model)+1), rng.IntN(len(model)+1)
				if a > z {
					a, z = z, a
				}
				position := func(offset int) (row, col int) {
					for _, r := range model[:offset] {
						if r == '\n' {
							row++
							col = 0
						} else {
							col++
						}
					}
					return
				}
				r1, c1 := position(a)
				r2, c2 := position(z)
				text := []string{"", "x", "é😀", "\n", "ab\ncd"}[rng.IntN(5)]
				b.ReplaceRange(r1, c1, r2, c2, text)
				want := append([]rune(nil), model[:a]...)
				want = append(want, []rune(text)...)
				model = append(want, model[z:]...)
				if b.Text() != string(model) {
					t.Fatalf("trial %d step %d: %q != %q", trial, step, b.Text(), string(model))
				}
			}
		})
		after := b.cursorState()
		if !b.Undo() || b.Text() != initial || b.cursorState() != before {
			t.Fatalf("trial %d undo lost original text or selection", trial)
		}
		if b.Undo() {
			t.Fatal("one transaction created multiple undo entries")
		}
		if !b.Redo() || b.Text() != string(model) || b.cursorState() != after {
			t.Fatalf("trial %d redo lost final text or selection", trial)
		}
	}
}

func TestTabReplacesSelectionAsOneUndoCommand(t *testing.T) {
	e := newEditor()
	e.buf.ResetText("abcd")
	e.buf.SetAnchor(0, 1)
	e.buf.SetCursor(0, 3)
	e.handleInsert(nil, Key{Kind: KeyTab})
	if !e.buf.Undo() || e.buf.Text() != "abcd" || e.buf.Selection() != "bc" {
		t.Fatalf("undo text=%q selection=%q", e.buf.Text(), e.buf.Selection())
	}
}
