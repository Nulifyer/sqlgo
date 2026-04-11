// Package clipboard is sqlgo's abstraction over the OS clipboard. It
// wraps github.com/atotto/clipboard (pure-Go on Windows/macOS, shells out
// to xclip/xsel/wl-copy on Linux) behind a small interface so the TUI
// call sites can stay clipboard-implementation-agnostic. A future OSC 52
// fallback for SSH sessions will plug in here without touching callers.
package clipboard

import (
	"errors"
	"strings"

	atotto "github.com/atotto/clipboard"
)

// ErrUnsupported is returned by Copy when no clipboard backend is
// available on the current system. On Linux this typically means neither
// xclip, xsel, nor wl-copy is installed; on headless CI machines it's
// expected and callers should treat it as a soft failure.
var ErrUnsupported = errors.New("clipboard: no backend available on this system")

// Clipboard is the minimal interface sqlgo needs from a clipboard
// backend. Kept tiny so the OSC 52 adapter we'll add for SSH sessions
// (Phase 2) can implement it without pulling in xclip-specific quirks.
type Clipboard interface {
	// Copy places text on the system clipboard. Large payloads are
	// fine -- atotto streams them straight through.
	Copy(text string) error
	// Paste returns the current clipboard contents. Not every backend
	// supports read (OSC 52 is write-only), so callers must handle
	// ErrUnsupported.
	Paste() (string, error)
}

// System returns a Clipboard backed by atotto/clipboard, the default on
// every platform sqlgo ships for. Errors from the underlying package are
// mapped to ErrUnsupported so callers have a single sentinel to check.
func System() Clipboard { return systemClipboard{} }

type systemClipboard struct{}

func (systemClipboard) Copy(text string) error {
	if atotto.Unsupported {
		return ErrUnsupported
	}
	if err := atotto.WriteAll(text); err != nil {
		return mapErr(err)
	}
	return nil
}

func (systemClipboard) Paste() (string, error) {
	if atotto.Unsupported {
		return "", ErrUnsupported
	}
	s, err := atotto.ReadAll()
	if err != nil {
		return "", mapErr(err)
	}
	return s, nil
}

// mapErr translates atotto errors to ErrUnsupported.
//   - Linux: missing xclip/xsel/wl-copy
//   - Windows: OpenClipboard failures (no interactive session,
//     bash-in-terminal, CI runs)
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "executable file not found") ||
		strings.Contains(msg, "xsel") ||
		strings.Contains(msg, "xclip") ||
		strings.Contains(msg, "wl-copy") {
		return ErrUnsupported
	}
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "access is denied") ||
		strings.Contains(lower, "openclipboard") {
		return ErrUnsupported
	}
	return err
}
