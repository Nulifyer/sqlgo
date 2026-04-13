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
// Alt is true for Alt+<rune> combos -- terminals encode these as
// ESC+<rune>, which we distinguish from bare Esc at decode time by
// peeking for a follow-up byte. Shift is set on arrow/home/end/pg keys
// when the terminal reports an xterm modifier of 2 or higher, so the
// editor can drive shift-select without needing its own shift latch.
type Key struct {
	Kind  KeyKind
	Rune  rune
	Ctrl  bool
	Alt   bool
	Shift bool
}

// keyReader decodes bytes from a terminal into Key events. It owns its
// own buffered reader so escape sequences can be peeked.
type keyReader struct {
	r *bufio.Reader
}

func newKeyReader(r io.Reader) *keyReader {
	return &keyReader{r: bufio.NewReader(r)}
}

// Read blocks until the next input event is available. Returns one of
// Key, PasteMsg, MouseMsg, FocusMsg, BlurMsg depending on the CSI
// sequence the terminal emitted.
func (kr *keyReader) Read() (InputMsg, error) {
	b, err := kr.r.ReadByte()
	if err != nil {
		return nil, err
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
	case b == 0x00:
		// 0x00 is historically both Ctrl+@ and Ctrl+Space. Decode
		// as Ctrl+Space -- far more useful as an editor shortcut.
		return Key{Kind: KeyRune, Rune: ' ', Ctrl: true}, nil
	case b < 0x20:
		// Ctrl+<letter>: 0x01..0x1a maps to a..z
		return Key{Kind: KeyRune, Rune: rune(b - 1 + 'a'), Ctrl: true}, nil
	case b < 0x80:
		return Key{Kind: KeyRune, Rune: rune(b)}, nil
	}

	// UTF-8 multi-byte. Reconstruct.
	return kr.readUTF8(b)
}

func (kr *keyReader) readUTF8(first byte) (InputMsg, error) {
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
func (kr *keyReader) readEscape() (InputMsg, error) {
	// Peek with a wait so we can distinguish bare Esc from CSI. The
	// window needs to cover cold-buffer latency on ConPTY / SSH;
	// 8ms was too tight and caused arrow-key splits where the "["
	// arrived late, leaving "5~" or "~" remnants from PgUp/PgDn
	// sequences to leak in as literal runes. 50ms is well under
	// perceptible Esc lag and well over typical inter-byte delays.
	if !kr.peekAvailable(50 * time.Millisecond) {
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

func (kr *keyReader) readCSI() (InputMsg, error) {
	// CSI: read until a final byte in 0x40..0x7e.
	//
	// SGR mouse (CSI <...M|m) has a '<' intermediate byte we need to
	// detect early so decodeCSI doesn't mis-parse the params. Legacy
	// X10 mouse (CSI M<b><x><y>) is a fixed 3-byte payload right after
	// the M with no semicolons -- handled via the sgr=false path.
	var params []byte
	sgrMouse := false
	for {
		b, err := kr.r.ReadByte()
		if err != nil {
			return nil, err
		}
		// SGR mouse introducer: remember it and drop from params so
		// the numeric parser doesn't trip on the '<'.
		if len(params) == 0 && b == '<' {
			sgrMouse = true
			continue
		}
		// Legacy X10 mouse: CSI M <btn> <x> <y>. The M arrives as a
		// terminator (0x40-0x7e), so we branch here before the final-
		// byte check swallows it.
		if len(params) == 0 && !sgrMouse && b == 'M' {
			return kr.readLegacyMouse()
		}
		if b >= 0x40 && b <= 0x7e {
			if sgrMouse && (b == 'M' || b == 'm') {
				return decodeSGRMouse(params, b == 'M')
			}
			// Bracketed paste start: CSI 200~ -> collect until 201~.
			if b == '~' && string(params) == "200" {
				return kr.readPaste()
			}
			// Focus in/out: CSI I / CSI O (no params).
			if len(params) == 0 && b == 'I' {
				return FocusMsg{}, nil
			}
			if len(params) == 0 && b == 'O' {
				return BlurMsg{}, nil
			}
			return decodeCSI(params, b), nil
		}
		params = append(params, b)
	}
}

// readPaste accumulates bytes until CSI 201~ (paste end) and returns
// them as a single PasteMsg. Any CSI/escape bytes inside the paste are
// passed through verbatim -- terminals that escape them do so before
// bracketing, so the payload is the literal text the user copied.
func (kr *keyReader) readPaste() (InputMsg, error) {
	var buf []byte
	const endSeq = "\x1b[201~"
	for {
		b, err := kr.r.ReadByte()
		if err != nil {
			return nil, err
		}
		buf = append(buf, b)
		// Fast path: check the tail of buf for the terminator.
		if len(buf) >= len(endSeq) && string(buf[len(buf)-len(endSeq):]) == endSeq {
			buf = buf[:len(buf)-len(endSeq)]
			return PasteMsg{Text: string(buf)}, nil
		}
		// Safety cap: 16 MB. A paste longer than that is almost
		// certainly a wedged terminal; drop the partial and return an
		// empty PasteMsg so the loop doesn't block forever.
		if len(buf) > 16*1024*1024 {
			return PasteMsg{}, nil
		}
	}
}

// readLegacyMouse decodes the X10 encoding: three bytes after "CSI M",
// biased by 32. It's obsolete but still emitted by some terminals when
// SGR isn't negotiated. Coordinates are 1-based in both encodings.
func (kr *keyReader) readLegacyMouse() (InputMsg, error) {
	buf := make([]byte, 3)
	if _, err := io.ReadFull(kr.r, buf); err != nil {
		return nil, err
	}
	code := int(buf[0]) - 32
	x := int(buf[1]) - 32
	y := int(buf[2]) - 32
	return mouseFromCode(code, x, y, true), nil
}

// decodeSGRMouse parses "CSI <code;x;y M|m" into a MouseMsg. The final
// byte tells us press vs release: capital M = press, lowercase m =
// release. Motion events show up with the motion bit (32) set in code.
func decodeSGRMouse(params []byte, press bool) (InputMsg, error) {
	// Three ';'-separated integers.
	code, x, y, ok := parseThreeInts(params)
	if !ok {
		return Key{Kind: KeyEsc}, nil
	}
	return mouseFromCode(code, x, y, press), nil
}

// mouseFromCode extracts button / action / modifiers from an xterm
// mouse code. Bit layout: 0-1 = button low bits, 2 = shift, 3 = alt,
// 4 = ctrl, 5 = motion flag, 6+7 = button high bits (wheel & extended).
func mouseFromCode(code, x, y int, press bool) MouseMsg {
	shift := code&4 != 0
	alt := code&8 != 0
	ctrl := code&16 != 0
	motion := code&32 != 0
	btnLow := code & 3
	btnHigh := code >> 6 // 0, 1 (wheel), 2 (extended)

	var btn MouseButton
	var act MouseAction
	switch btnHigh {
	case 1:
		// Wheel: low bit 0 = up, 1 = down.
		if btnLow == 0 {
			btn = MouseButtonWheelUp
		} else {
			btn = MouseButtonWheelDown
		}
		act = MouseActionPress
	default:
		switch btnLow {
		case 0:
			btn = MouseButtonLeft
		case 1:
			btn = MouseButtonMiddle
		case 2:
			btn = MouseButtonRight
		case 3:
			// 3 in SGR = release of whichever button. Legacy X10
			// reports all releases as 3 (no which-button info).
			btn = MouseButtonNone
		}
		if motion {
			act = MouseActionMotion
		} else if !press || btnLow == 3 {
			act = MouseActionRelease
		} else {
			act = MouseActionPress
		}
	}
	return MouseMsg{X: x, Y: y, Button: btn, Action: act, Shift: shift, Alt: alt, Ctrl: ctrl}
}

// parseThreeInts splits params on ';' and parses three decimals. Used
// only by SGR mouse decoding; returns ok=false on malformed input.
func parseThreeInts(params []byte) (a, b, c int, ok bool) {
	vals := [3]int{}
	idx := 0
	cur := 0
	started := false
	for _, ch := range params {
		if ch == ';' {
			if !started || idx >= 2 {
				if idx >= 3 {
					return 0, 0, 0, false
				}
			}
			vals[idx] = cur
			idx++
			cur = 0
			started = false
			if idx >= 3 {
				return 0, 0, 0, false
			}
			continue
		}
		if ch < '0' || ch > '9' {
			return 0, 0, 0, false
		}
		cur = cur*10 + int(ch-'0')
		started = true
	}
	if !started || idx != 2 {
		return 0, 0, 0, false
	}
	vals[2] = cur
	return vals[0], vals[1], vals[2], true
}

func decodeCSI(params []byte, final byte) Key {
	// xterm modifier encoding: two params "<code>;<mod>" for arrow/
	// home/end keys, and "<n>;<mod>" for tilde codes. mod = 1 + bits
	// where bit 0 = Shift, bit 1 = Alt, bit 2 = Ctrl. No mod param
	// means unmodified.
	p1, p2 := splitCSIParams(params)
	mod := parseModifier(p2)
	apply := func(k Key) Key {
		if mod.Shift {
			k.Shift = true
		}
		if mod.Alt {
			k.Alt = true
		}
		if mod.Ctrl {
			k.Ctrl = true
		}
		return k
	}
	switch final {
	case 'A':
		return apply(Key{Kind: KeyUp})
	case 'B':
		return apply(Key{Kind: KeyDown})
	case 'C':
		return apply(Key{Kind: KeyRight})
	case 'D':
		return apply(Key{Kind: KeyLeft})
	case 'H':
		return apply(Key{Kind: KeyHome})
	case 'F':
		return apply(Key{Kind: KeyEnd})
	case 'Z':
		return Key{Kind: KeyBackTab}
	case '~':
		switch p1 {
		case "1":
			return apply(Key{Kind: KeyHome})
		case "3":
			return apply(Key{Kind: KeyDelete})
		case "4":
			return apply(Key{Kind: KeyEnd})
		case "5":
			return apply(Key{Kind: KeyPgUp})
		case "6":
			return apply(Key{Kind: KeyPgDn})
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

// splitCSIParams divides a CSI param byte string at the first ';' and
// returns the two halves as strings. Extra params beyond the second
// are ignored -- none of the sequences we care about use them.
func splitCSIParams(params []byte) (string, string) {
	for i, b := range params {
		if b == ';' {
			return string(params[:i]), string(params[i+1:])
		}
	}
	return string(params), ""
}

// csiModifier carries the decoded modifier bits from an xterm-style
// keypress param. Unknown / missing params decode to zero value.
type csiModifier struct {
	Shift bool
	Alt   bool
	Ctrl  bool
}

// parseModifier turns a CSI modifier param ("2", "5", etc) into a
// csiModifier. Per xterm, the value is 1 + bitmask of Shift(1) /
// Alt(2) / Ctrl(4). An empty or unparseable string yields no modifiers.
func parseModifier(s string) csiModifier {
	if s == "" {
		return csiModifier{}
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return csiModifier{}
		}
		n = n*10 + int(c-'0')
	}
	if n < 1 {
		return csiModifier{}
	}
	bits := n - 1
	return csiModifier{
		Shift: bits&1 != 0,
		Alt:   bits&2 != 0,
		Ctrl:  bits&4 != 0,
	}
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
