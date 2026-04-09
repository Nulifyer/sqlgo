package clipboard

import "sync"

// Memory is an in-process clipboard useful for tests and for headless
// environments (CI, SSH sessions without OSC 52) where the system
// clipboard isn't reachable. The TUI uses it as a fallback so "copy
// cell" still lets the user paste later with the built-in paste key,
// even when ErrUnsupported comes back from System().
type Memory struct {
	mu  sync.Mutex
	buf string
}

// NewMemory returns an empty in-memory clipboard.
func NewMemory() *Memory { return &Memory{} }

func (m *Memory) Copy(text string) error {
	m.mu.Lock()
	m.buf = text
	m.mu.Unlock()
	return nil
}

func (m *Memory) Paste() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buf, nil
}
