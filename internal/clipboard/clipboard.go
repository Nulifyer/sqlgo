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

// mapErr translates atotto's raw errors into ErrUnsupported so
// callers don't need to string-match. atotto doesn't expose its
// own sentinel error type -- only a package-level Unsupported
// bool we check before calling -- so this is the fallback path
// for runtime failures.
//
// Mapped cases:
//   - Linux: missing xclip/xsel/wl-copy -> ErrUnsupported.
//   - Windows: OpenClipboard() failures are surfaced as raw
//     Win32 errors. "Access is denied" in particular fires when
//     the current process has no interactive desktop session
//     (bash-in-terminal on a locked workstation, non-interactive
//     CI runs, etc). We treat those as "no clipboard for you"
//     rather than failing the Copy outright.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	// Linux: underlying exec() failures.
	if strings.Contains(msg, "executable file not found") ||
		strings.Contains(msg, "xsel") ||
		strings.Contains(msg, "xclip") ||
		strings.Contains(msg, "wl-copy") {
		return ErrUnsupported
	}
	// Windows: Win32 OpenClipboard failure modes. Matched
	// case-insensitively because syscall error formatting
	// varies across Windows versions and atotto wraps the raw
	// syscall error without normalizing capitalization.
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "access is denied") ||
		strings.Contains(lower, "openclipboard") {
		return ErrUnsupported
	}
	return err
}
