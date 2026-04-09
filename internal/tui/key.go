package tui

import (
	"bufio"
	"io"
	"time"
)

// KeyKind is a coarse classification of a keypress.
type KeyKind int

const (
	KeyRune KeyKind = iota
	KeyEnter
	KeyTab
	KeyBackTab
	KeyBackspace
	KeyEsc
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyHome
	KeyEnd
	KeyPgUp
	KeyPgDn
	KeyDelete
	KeyF1
	KeyF2
	KeyF3
	KeyF4
	KeyF5
	KeyF6
	KeyF7
	KeyF8
	KeyF9
	KeyF10
	KeyF11
	KeyF12
)

// Key is a single decoded keypress. Ctrl is true for Ctrl+<rune> combos
// (Rune holds the lowercase letter; e.g. Ctrl+Q -> Rune='q', Ctrl=true).
// Alt is true for Alt+<rune> combos — terminals encode these as ESC+<rune>,
// which we distinguish from bare Esc at decode time by peeking for a
// follow-up byte.
type Key struct {
	Kind KeyKind
	Rune rune
	Ctrl bool
	Alt  bool
}

// keyReader decodes bytes from a terminal into Key events. It owns its
// own buffered reader so escape sequences can be peeked.
type keyReader struct {
	r *bufio.Reader
}

func newKeyReader(r io.Reader) *keyReader {
	return &keyReader{r: bufio.NewReader(r)}
}

// Read blocks until the next key is available.
func (kr *keyReader) Read() (Key, error) {
	b, err := kr.r.ReadByte()
	if err != nil {
		return Key{}, err
	}

	switch {
	case b == 0x1b:
		return kr.readEscape()
	case b == '\r' || b == '\n':
		return Key{Kind: KeyEnter}, nil
	case b == '\t':
		return Key{Kind: KeyTab}, nil
	case b == 0x7f || b == 0x08:
		return Key{Kind: KeyBackspace}, nil
	case b < 0x20:
		// Ctrl+<letter>: 0x01..0x1a maps to a..z
		return Key{Kind: KeyRune, Rune: rune(b - 1 + 'a'), Ctrl: true}, nil
	case b < 0x80:
		return Key{Kind: KeyRune, Rune: rune(b)}, nil
	}

	// UTF-8 multi-byte. Reconstruct.
	return kr.readUTF8(b)
}

func (kr *keyReader) readUTF8(first byte) (Key, error) {
	var n int
	switch {
	case first&0xE0 == 0xC0:
		n = 1
	case first&0xF0 == 0xE0:
		n = 2
	case first&0xF8 == 0xF0:
		n = 3
	default:
		return Key{Kind: KeyRune, Rune: rune(first)}, nil
	}
	buf := make([]byte, n+1)
	buf[0] = first
	if _, err := io.ReadFull(kr.r, buf[1:]); err != nil {
		return Key{}, err
	}
	r, _ := decodeUTF8(buf)
	return Key{Kind: KeyRune, Rune: r}, nil
}

// readEscape handles ESC, ESC+[<...>, ESC+O<...>, and ESC+<rune> (Alt+rune)
// sequences. A bare ESC (no follow-up within a short window) returns KeyEsc.
func (kr *keyReader) readEscape() (Key, error) {
	// Peek with a small wait so we can distinguish bare Esc from CSI.
	if !kr.peekAvailable(8 * time.Millisecond) {
		return Key{Kind: KeyEsc}, nil
	}
	b, err := kr.r.ReadByte()
	if err != nil {
		return Key{Kind: KeyEsc}, nil
	}
	switch b {
	case '[':
		return kr.readCSI()
	case 'O':
		// SS3: function keys on some terminals.
		c, err := kr.r.ReadByte()
		if err != nil {
			return Key{Kind: KeyEsc}, nil
		}
		switch c {
		case 'P':
			return Key{Kind: KeyF1}, nil
		case 'H':
			return Key{Kind: KeyHome}, nil
		case 'F':
			return Key{Kind: KeyEnd}, nil
		}
		return Key{Kind: KeyEsc}, nil
	}
	// Alt+<rune>: ESC followed by a printable ASCII byte. This covers
	// Alt+1..9, Alt+letters, etc. Multibyte Alt combos are rare enough to
	// ignore — they fall through to bare Esc.
	if b >= 0x20 && b < 0x7f {
		return Key{Kind: KeyRune, Rune: rune(b), Alt: true}, nil
	}
	return Key{Kind: KeyEsc}, nil
}

func (kr *keyReader) readCSI() (Key, error) {
	// CSI: read until a final byte in 0x40..0x7e.
	var params []byte
	for {
		b, err := kr.r.ReadByte()
		if err != nil {
			return Key{}, err
		}
		if b >= 0x40 && b <= 0x7e {
			return decodeCSI(params, b), nil
		}
		params = append(params, b)
	}
}

func decodeCSI(params []byte, final byte) Key {
	switch final {
	case 'A':
		return Key{Kind: KeyUp}
	case 'B':
		return Key{Kind: KeyDown}
	case 'C':
		return Key{Kind: KeyRight}
	case 'D':
		return Key{Kind: KeyLeft}
	case 'H':
		return Key{Kind: KeyHome}
	case 'F':
		return Key{Kind: KeyEnd}
	case 'Z':
		return Key{Kind: KeyBackTab}
	case '~':
		// CSI <n>~ : 1=Home 3=Del 4=End 5=PgUp 6=PgDn
		// 15=F5 17=F6 18=F7 19=F8 20=F9 21=F10 23=F11 24=F12
		switch string(params) {
		case "1":
			return Key{Kind: KeyHome}
		case "3":
			return Key{Kind: KeyDelete}
		case "4":
			return Key{Kind: KeyEnd}
		case "5":
			return Key{Kind: KeyPgUp}
		case "6":
			return Key{Kind: KeyPgDn}
		case "15":
			return Key{Kind: KeyF5}
		case "17":
			return Key{Kind: KeyF6}
		case "18":
			return Key{Kind: KeyF7}
		case "19":
			return Key{Kind: KeyF8}
		case "20":
			return Key{Kind: KeyF9}
		case "21":
			return Key{Kind: KeyF10}
		case "23":
			return Key{Kind: KeyF11}
		case "24":
			return Key{Kind: KeyF12}
		}
	}
	return Key{Kind: KeyEsc}
}

// peekAvailable returns true if at least one byte is buffered or arrives
// within d. We can only check the buffered side without dragging in select(2),
// so on cold buffers we do a tiny blocking read with a goroutine.
func (kr *keyReader) peekAvailable(d time.Duration) bool {
	if kr.r.Buffered() > 0 {
		return true
	}
	ch := make(chan bool, 1)
	go func() {
		_, err := kr.r.Peek(1)
		ch <- err == nil
	}()
	select {
	case ok := <-ch:
		return ok
	case <-time.After(d):
		return false
	}
}

func decodeUTF8(b []byte) (rune, int) {
	// minimal decoder; full UTF-8 handled by callers via bufio reads
	switch len(b) {
	case 1:
		return rune(b[0]), 1
	case 2:
		return rune(b[0]&0x1f)<<6 | rune(b[1]&0x3f), 2
	case 3:
		return rune(b[0]&0x0f)<<12 | rune(b[1]&0x3f)<<6 | rune(b[2]&0x3f), 3
	case 4:
		return rune(b[0]&0x07)<<18 | rune(b[1]&0x3f)<<12 | rune(b[2]&0x3f)<<6 | rune(b[3]&0x3f), 4
	}
	return 0xFFFD, 1
}
