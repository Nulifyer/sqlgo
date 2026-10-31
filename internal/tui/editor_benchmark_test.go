package tui

import (
	"fmt"
	"strings"
	"testing"
)

// The first three fixtures preserve the original audit workload for comparison.
func BenchmarkEditorPasteMiddle(b *testing.B) {
	for _, n := range []int{4096, 16384, 65536} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				buf := newBuffer()
				buf.SetText(strings.Repeat("x", n))
				buf.SetCursor(0, 0)
				b.StartTimer()
				buf.InsertText(strings.Repeat("y", n))
				b.StopTimer()
			}
		})
	}
}
func BenchmarkEditorUndoMemory(b *testing.B) {
	for _, n := range []int{1024, 1024 * 1024} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				buf := newBuffer()
				buf.SetText(strings.Repeat("x", n))
				for k := 0; k < 32; k++ {
					buf.InsertText(";")
				}
			}
		})
	}
}
func BenchmarkEditorDrawAtDepth(b *testing.B) {
	for _, n := range []int{100, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			e := newEditor()
			e.buf.SetText(strings.Repeat("SELECT a FROM table_name;\n", n))
			e.buf.SetCursor(n-1, 0)
			c := newCellbuf(100, 30)
			r := rect{Row: 1, Col: 1, W: 100, H: 30}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				c.Reset()
				e.draw(c, r, true)
			}
		})
	}
}

func BenchmarkEditorDrawCachedAtDepth(b *testing.B) {
	for _, n := range []int{100, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			e := newEditor()
			e.buf.ResetText(strings.Repeat("SELECT a FROM table_name;\n", n))
			e.buf.SetCursor(n-1, 0)
			e.syntax = buildEditorSyntax(e.buf.Text())
			e.syntaxRevision = e.buf.Revision()
			c := newCellbuf(100, 30)
			r := rect{Row: 1, Col: 1, W: 100, H: 30}
			e.draw(c, r, true)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				c.Reset()
				e.draw(c, r, true)
			}
		})
	}
}
