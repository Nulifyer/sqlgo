package tui

import (
	"fmt"

	"github.com/Nulifyer/sqlgo/internal/config"
	"github.com/Nulifyer/sqlgo/internal/sshtunnel"
	"golang.org/x/crypto/ssh"
)

// trustLayer is the modal overlay shown when an SSH tunnel open
// returns an *UnknownHostError. It displays the host address and
// the SHA256 fingerprint of the presented key, and asks the user
// to accept (writes the key to ~/.ssh/known_hosts and retries the
// connection) or reject (pops the overlay and leaves the original
// connection attempt failed).
//
// Trust-on-first-use is deliberately a two-keystroke action: Enter
// + 'y' to accept, Esc to reject. A single Enter could be hit by
// accident, and host-key trust isn't something we want to fall
// into on a typo.
type trustLayer struct {
	target config.Connection
	err    *sshtunnel.UnknownHostError

	// armed flips to true on the first Enter; a second Enter then
	// commits the trust. 'y' / 'n' are the explicit shortcuts and
	// work whether or not armed is set.
	armed  bool
	status string
}

func newTrustLayer(target config.Connection, err *sshtunnel.UnknownHostError) *trustLayer {
	return &trustLayer{target: target, err: err}
}

func (tl *trustLayer) Draw(a *app, c *cellbuf) {
	boxW := 72
	if boxW > a.term.width-4 {
		boxW = a.term.width - 4
	}
	if boxW < 48 {
		boxW = 48
	}
	boxH := 12
	row := (a.term.height - boxH) / 2
	col := (a.term.width - boxW) / 2
	if row < 1 {
		row = 1
	}
	if col < 1 {
		col = 1
	}
	r := rect{row: row, col: col, w: boxW, h: boxH}
	c.fillRect(r)
	drawFrame(c, r, "SSH host not trusted", true)

	innerCol := col + 2
	c.writeAt(row+1, innerCol, truncate(
		fmt.Sprintf("Host %s:%d is not in your known_hosts file.", tl.err.Host, tl.err.Port),
		boxW-4,
	))
	c.writeAt(row+2, innerCol, truncate("The server presented this key:", boxW-4))
	c.writeAt(row+3, innerCol+2, truncate(
		fmt.Sprintf("type:        %s", tl.err.Key.Type()),
		boxW-6,
	))
	c.writeAt(row+4, innerCol+2, truncate(
		fmt.Sprintf("fingerprint: %s", ssh.FingerprintSHA256(tl.err.Key)),
		boxW-6,
	))
	c.writeAt(row+6, innerCol, truncate(
		"Verify this fingerprint out-of-band before accepting.",
		boxW-4,
	))
	c.writeAt(row+7, innerCol, truncate(
		"If it does not match, press Esc or N and contact the host operator.",
		boxW-4,
	))

	if tl.status != "" {
		c.writeAt(row+boxH-3, innerCol, truncate("! "+tl.status, boxW-4))
	}

	prompt := "Trust this key? [y]=yes  [n/Esc]=no"
	if tl.armed {
		prompt = "Press Enter again or 'y' to CONFIRM, Esc to cancel"
	}
	c.writeAt(row+boxH-2, innerCol, truncate(prompt, boxW-4))
}

func (tl *trustLayer) HandleKey(a *app, k Key) {
	// Esc / n / N always reject, regardless of armed state.
	if k.Kind == KeyEsc {
		tl.reject(a)
		return
	}
	if k.Kind == KeyRune && !k.Ctrl && !k.Alt {
		switch k.Rune {
		case 'n', 'N':
			tl.reject(a)
			return
		case 'y', 'Y':
			tl.accept(a)
			return
		}
	}
	if k.Kind == KeyEnter {
		if tl.armed {
			tl.accept(a)
			return
		}
		tl.armed = true
		tl.status = "press Enter again or 'y' to confirm"
		return
	}
	// Any other key disarms so an accidental key doesn't get
	// followed by an intended Enter.
	tl.armed = false
}

// reject pops the overlay and surfaces a friendly message on the
// underlying layer (picker status line if the picker is still up,
// main-view status otherwise).
func (tl *trustLayer) reject(a *app) {
	a.popLayer()
	msg := fmt.Sprintf("ssh trust rejected: %s:%d", tl.err.Host, tl.err.Port)
	if pl, ok := a.topLayer().(*pickerLayer); ok {
		pl.setStatus(msg)
		return
	}
	a.mainLayerPtr().status = msg
}

// accept writes the host key to ~/.ssh/known_hosts and immediately
// retries the connection. On write failure we stay on the overlay
// and surface the error so the user can decide what to do next.
func (tl *trustLayer) accept(a *app) {
	if err := sshtunnel.AppendKnownHost(tl.err.Host, tl.err.Port, tl.err.Key); err != nil {
		tl.status = "write known_hosts failed: " + err.Error()
		tl.armed = false
		return
	}
	a.popLayer()
	// Surface a transient success message on the layer we're
	// returning to, then retry the dial that triggered this
	// overlay.
	if pl, ok := a.topLayer().(*pickerLayer); ok {
		pl.setStatus("ssh host trusted; reconnecting...")
		// Flush so the retry's blocking dial doesn't leave the UI
		// visually stuck on the old status.
		a.draw()
		_ = a.scr.flush()
	}
	a.connectTo(tl.target)
}

// Hints is the status-bar hint line for the trust layer.
func (tl *trustLayer) Hints(a *app) string {
	_ = a
	return joinHints("y=trust", "n/Esc=reject")
}
