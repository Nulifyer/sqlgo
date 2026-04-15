package tui

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Nulifyer/sqlgo/internal/config"
	"github.com/Nulifyer/sqlgo/internal/db"
)

// probeTimeout bounds each test button so the form never hangs on a
// dead host longer than the user's patience. Five seconds matches the
// connect-side timeouts and is plenty for a TCP handshake or driver
// auth round-trip on any real network.
const probeTimeout = 5 * time.Second

// startTestNetwork runs a driver-aware reachability probe in the
// background and writes the outcome back to the form's status line.
// Scope is "can we reach the server?" -- no auth, no DSN. For local
// engines (sqlite, file) it falls back to existence checks since
// there's no socket to dial.
func (fl *formLayer) startTestNetwork(a *app) {
	f := fl.f
	driver := strings.TrimSpace(f.fixed[coreDriver].in.String())
	host := strings.TrimSpace(f.fixed[coreHost].in.String())
	portStr := strings.TrimSpace(f.fixed[corePort].in.String())
	database := strings.TrimSpace(f.fixed[coreDatabase].in.String())

	f.status = "testing network..."
	go func() {
		msg := probeNetwork(driver, host, portStr, database)
		a.asyncCh <- func(a *app) {
			if a.topLayer() != fl {
				return
			}
			fl.f.status = msg
		}
	}()
}

// probeNetwork does the driver-specific reachability check. Returns a
// human-readable one-liner either way -- callers just assign it to
// status. Never returns an error separately.
func probeNetwork(driver, host, portStr, database string) string {
	switch driver {
	case "sqlite", "file":
		// No network; check the file path(s) instead. file driver
		// packs a ';'-separated list into Database.
		if database == "" {
			return "network: no path set"
		}
		paths := []string{database}
		if driver == "file" {
			paths = strings.Split(database, ";")
		}
		for _, p := range paths {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if _, err := os.Stat(p); err != nil {
				return "network: " + err.Error()
			}
		}
		return "network: path(s) reachable"
	case "libsql":
		// Host is a full URL (libsql://... or https://...). Extract
		// the underlying host:port and dial that.
		h, p, err := parseLibsqlHost(host)
		if err != nil {
			return "network: " + err.Error()
		}
		return dial(h, p)
	case "d1":
		// Host optional (defaults to api.cloudflare.com). No port in
		// the form; Cloudflare only speaks 443.
		h := host
		if h == "" {
			h = "api.cloudflare.com"
		}
		return dial(h, 443)
	}
	if host == "" {
		return "network: host required"
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > maxTCPPort {
		return "network: port must be 1..65535"
	}
	return dial(host, port)
}

func dial(host string, port int) string {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	c, err := net.DialTimeout("tcp", addr, probeTimeout)
	if err != nil {
		return "network: " + err.Error()
	}
	_ = c.Close()
	return "network: reachable (" + addr + ")"
}

// parseLibsqlHost extracts host + port from the Turso URL the libsql
// driver expects. Accepts bare host:port too so partially-typed forms
// still probe sensibly.
func parseLibsqlHost(raw string) (string, int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", 0, fmt.Errorf("host required")
	}
	if !strings.Contains(raw, "://") {
		h, p, err := net.SplitHostPort(raw)
		if err != nil {
			return raw, 443, nil
		}
		pn, err := strconv.Atoi(p)
		if err != nil {
			return "", 0, fmt.Errorf("bad port: %s", p)
		}
		return h, pn, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", 0, err
	}
	h := u.Hostname()
	if h == "" {
		return "", 0, fmt.Errorf("no host in %q", raw)
	}
	p := u.Port()
	if p == "" {
		return h, 443, nil
	}
	pn, err := strconv.Atoi(p)
	if err != nil {
		return "", 0, fmt.Errorf("bad port: %s", p)
	}
	return h, pn, nil
}

// startTestAuth runs the full driver.Open + Ping path so the user can
// confirm credentials before saving. Validates the form first so a
// missing required field surfaces exactly like Ctrl+S would, without
// wasting a round-trip.
func (fl *formLayer) startTestAuth(a *app) {
	f := fl.f
	c, err := f.toConnection()
	if err != nil {
		f.status = "auth: " + err.Error()
		return
	}
	f.status = "testing auth..."
	go func() {
		msg := probeAuth(c)
		a.asyncCh <- func(a *app) {
			if a.topLayer() != fl {
				return
			}
			fl.f.status = msg
		}
	}()
}

func probeAuth(c config.Connection) string {
	d, err := db.Get(c.Driver)
	if err != nil {
		return "auth: " + err.Error()
	}
	cfg := db.Config{
		Host:     c.Host,
		Port:     c.Port,
		User:     c.User,
		Password: c.Password,
		Database: c.Database,
		Options:  c.Options,
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		return "auth: " + err.Error()
	}
	defer conn.Close()
	if err := conn.Ping(ctx); err != nil {
		return "auth: " + err.Error()
	}
	return "auth: ok"
}
