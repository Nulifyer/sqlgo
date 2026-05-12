//go:build windows

package tui

import (
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// stdinReader returns a byte source that reads console input via
// ReadConsoleW. Two layers on Windows translate ^Z -> EOF and close
// the app on Ctrl+Z:
//  1. Go's internal/poll console wrapper (golang/go#3530) -- bypassed
//     by not using os.Stdin.Read.
//  2. The Win32 ReadFile path itself, at the ConDrv driver layer
//     (microsoft/terminal#4958) -- raw mode does not disable this.
//
// ReadConsoleW is the only documented API that never processes ^Z,
// so we use it and decode the UTF-16 result to UTF-8 bytes.
//
// The handle is os.Stdin's existing handle, which term.MakeRaw
// already put into raw mode (console modes are per-handle, so a
// fresh CONIN$ open would come back cooked).
func stdinReader() io.Reader {
	return &conInReader{h: windows.Handle(os.Stdin.Fd())}
}

type conInReader struct {
	h    windows.Handle
	wbuf [256]uint16
	buf  []byte
}

// pasteEventThreshold is the minimum number of key-down events that
// must be queued simultaneously before we treat the burst as a paste.
// Normal typing rarely queues more than 1-2 events; a paste dumps all
// characters at once. 8 avoids false positives from fast typing or
// held-key repeat while still catching short multi-line pastes.
const pasteEventThreshold = 8

func (c *conInReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(c.buf) == 0 {
		// Heuristic paste detection: when many key events are
		// already queued, batch-read them via ReadConsoleInputW
		// and wrap the result in bracketed paste sequences.
		// ReadConsoleW often does not relay the terminal's ESC[200~
		// brackets, so this ensures the key decoder sees a single
		// PasteMsg regardless of terminal support.
		if c.pendingKeyCount() >= pasteEventThreshold {
			if synth := c.readPasteBatch(); len(synth) > 0 {
				c.buf = synth
			}
		}
	}
	if len(c.buf) == 0 {
		var read uint32
		if err := windows.ReadConsole(c.h, &c.wbuf[0], uint32(len(c.wbuf)), &read, nil); err != nil {
			return 0, err
		}
		if read == 0 {
			return 0, io.EOF
		}
		runes := utf16.Decode(c.wbuf[:read])
		c.buf = []byte(string(runes))
	}
	n := copy(p, c.buf)
	c.buf = c.buf[n:]
	return n, nil
}

// pendingKeyCount returns the number of key-down events with
// printable/control characters currently queued. Native bracketed
// paste starters are treated as paste batches even before the normal
// heuristic threshold is reached, so a queued ESC[200~ opener does not
// fall back to the timing-sensitive ReadConsoleW byte path.
func (c *conInReader) pendingKeyCount() int {
	var total uint32
	if err := windows.GetNumberOfConsoleInputEvents(c.h, &total); err != nil || total == 0 {
		return 0
	}
	peekBuf := make([]inputRecord, min(int(total), 256))
	var read uint32
	r1, _, _ := procPeekConsoleInputW.Call(
		uintptr(c.h),
		uintptr(unsafe.Pointer(&peekBuf[0])),
		uintptr(len(peekBuf)),
		uintptr(unsafe.Pointer(&read)),
	)
	if r1 == 0 {
		return 0
	}
	count := 0
	var prefix strings.Builder
	prefix.Grow(len(bracketedPasteStart))
	firstPrintable := true
	startsWithEsc := false
	for i := uint32(0); i < read; i++ {
		if peekBuf[i].EventType != keyEvent {
			continue
		}
		kr := (*keyEventRecord)(unsafe.Pointer(&peekBuf[i].Event[0]))
		if kr.KeyDown == 0 || kr.UnicodeChar == 0 {
			continue
		}
		if firstPrintable {
			startsWithEsc = kr.UnicodeChar == 0x1B
			firstPrintable = false
		}
		if prefix.Len() < len(bracketedPasteStart) {
			prefix.WriteRune(rune(kr.UnicodeChar))
			if prefix.String() == bracketedPasteStart {
				return pasteEventThreshold
			}
		}
		count++
	}
	if total < uint32(pasteEventThreshold) {
		return 0
	}
	if startsWithEsc {
		return 0
	}
	return count
}

// readPasteBatch drains all pending input records via
// ReadConsoleInputW, extracts the character content from key-down
// events, and returns it wrapped in bracketed paste sequences
// (ESC[200~ ... ESC[201~). If the content already starts with the
// exact ESC[200~ opener it is returned as-is to avoid double-wrapping.
func (c *conInReader) readPasteBatch() []byte {
	recBuf := make([]inputRecord, 256)
	var runes []rune
	for {
		var n uint32
		if err := windows.GetNumberOfConsoleInputEvents(c.h, &n); err != nil || n == 0 {
			break
		}
		toRead := min(int(n), len(recBuf))
		var read uint32
		r1, _, _ := procReadConsoleInputW.Call(
			uintptr(c.h),
			uintptr(unsafe.Pointer(&recBuf[0])),
			uintptr(toRead),
			uintptr(unsafe.Pointer(&read)),
		)
		if r1 == 0 || read == 0 {
			break
		}
		for i := uint32(0); i < read; i++ {
			if recBuf[i].EventType != keyEvent {
				continue
			}
			kr := (*keyEventRecord)(unsafe.Pointer(&recBuf[i].Event[0]))
			if kr.KeyDown == 0 || kr.UnicodeChar == 0 {
				continue
			}
			ch := rune(kr.UnicodeChar)
			rep := kr.RepeatCount
			if rep == 0 {
				rep = 1
			}
			for j := uint16(0); j < rep; j++ {
				runes = append(runes, ch)
			}
		}
	}
	if len(runes) == 0 {
		return nil
	}
	return wrapPasteBatch(string(runes))
}

const (
	bracketedPasteStart = "\x1b[200~"
	bracketedPasteEnd   = "\x1b[201~"
)

func wrapPasteBatch(content string) []byte {
	if strings.HasPrefix(content, bracketedPasteStart) {
		return []byte(content)
	}
	return []byte(bracketedPasteStart + content + bracketedPasteEnd)
}

// stdinPeekReadable reports whether a key event is pending on the
// console input queue within d. It uses PeekConsoleInputW so we never
// consume bytes and never start a second concurrent ReadConsoleW --
// which would race the main reader on the shared bufio.Reader state
// and leak partial CSI tails ("5~", "[A", etc.) as literal runes when
// the main reader's peek window fired before the goroutine Peek got
// scheduled.
//
// Mouse / focus / window / menu events are filtered out: ReadConsoleW
// discards them, so a WaitForSingleObject-based signal would lie about
// readability and cause the next ReadByte to block.
func stdinPeekReadable(d time.Duration) bool {
	h := windows.Handle(os.Stdin.Fd())
	deadline := time.Now().Add(d)
	// Poll with 2ms granularity -- well under the 50ms window readEscape
	// grants us, fine-grained enough that we return promptly when input
	// arrives, coarse enough to not burn CPU.
	const step = 2 * time.Millisecond
	for {
		if hasPendingKeyEvent(h) {
			return true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		sleep := step
		if remaining < sleep {
			sleep = remaining
		}
		time.Sleep(sleep)
	}
}

func hasPendingKeyEvent(h windows.Handle) bool {
	var n uint32
	if err := windows.GetNumberOfConsoleInputEvents(h, &n); err != nil || n == 0 {
		return false
	}
	var buf [16]inputRecord
	count := n
	if count > uint32(len(buf)) {
		count = uint32(len(buf))
	}
	var read uint32
	r1, _, _ := procPeekConsoleInputW.Call(
		uintptr(h),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(count),
		uintptr(unsafe.Pointer(&read)),
	)
	if r1 == 0 {
		return false
	}
	for i := uint32(0); i < read; i++ {
		if buf[i].EventType == keyEvent {
			kr := (*keyEventRecord)(unsafe.Pointer(&buf[i].Event[0]))
			if kr.KeyDown != 0 && kr.UnicodeChar != 0 {
				return true
			}
			// Key-down events with UnicodeChar == 0 are pure modifier
			// presses (Shift/Ctrl/Alt) or function keys that Windows
			// resolves via key-translation. Arrow keys and friends set
			// UnicodeChar to 0 but VirtualKeyCode is non-zero; in VT
			// input mode ReadConsoleW translates them to escape
			// sequences, so treat those as pending too.
			if kr.KeyDown != 0 && kr.VirtualKeyCode != 0 && isTranslatableVK(kr.VirtualKeyCode) {
				return true
			}
		}
	}
	return false
}

// isTranslatableVK returns true for virtual-key codes that VT input
// mode translates into escape sequences (arrows, Home/End, PgUp/PgDn,
// F-keys, etc). We filter out pure modifier keys so peekAvailable
// doesn't return true on a Shift release and then block in ReadConsole.
func isTranslatableVK(vk uint16) bool {
	switch vk {
	case 0x10, 0x11, 0x12, // SHIFT, CONTROL, MENU(Alt)
		0xA0, 0xA1, 0xA2, 0xA3, 0xA4, 0xA5, // L/R SHIFT/CTRL/MENU
		0x14, 0x90, 0x91: // CAPITAL, NUMLOCK, SCROLL
		return false
	}
	return true
}

const keyEvent = 0x0001

type inputRecord struct {
	EventType uint16
	_         uint16
	// Union of event records; KEY_EVENT_RECORD is the largest we care
	// about. 16 bytes is enough to cover it on both 32- and 64-bit.
	Event [16]byte
}

type keyEventRecord struct {
	KeyDown         int32
	RepeatCount     uint16
	VirtualKeyCode  uint16
	VirtualScanCode uint16
	UnicodeChar     uint16
	ControlKeyState uint32
}

var (
	modKernel32           = windows.NewLazySystemDLL("kernel32.dll")
	procPeekConsoleInputW = modKernel32.NewProc("PeekConsoleInputW")
	procReadConsoleInputW = modKernel32.NewProc("ReadConsoleInputW")
)
