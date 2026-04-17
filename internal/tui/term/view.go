package term

// View declares terminal modes a layer wants active while it is on top
// of the stack. Mode flags with zero values mean "off"; the one
// exception is AltScreen, whose default is "on" via the tui layer system
// so layers that don't implement ViewProvider get the existing behavior.
//
// The screen tracks the last applied View and emits only the sequences
// for flags that flipped, so per-frame View() calls are cheap when
// nothing changes.
type View struct {
	AltScreen    bool
	MouseEnabled bool
	PasteEnabled bool
	WindowTitle  string
}
