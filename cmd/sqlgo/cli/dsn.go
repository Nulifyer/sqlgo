package cli

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/Nulifyer/sqlgo/internal/db"
)

// parseDSN turns a URL-shaped DSN into (driver-name, db.Config). The
// scheme selects the driver and the rest maps onto engine-agnostic
// Config fields:
//
//	scheme://[user[:password]@]host[:port]/database?opt=val&opt=val
//
// Query parameters land in Config.Options so driver-specific knobs
// (encrypt, sslmode, …) ride through unchanged. The password can be
// overridden later via --password-stdin or $SQLGO_PASSWORD; if a DSN
// already contains a password it is used verbatim.
func parseDSN(raw string) (string, db.Config, error) {
	if raw == "" {
		return "", db.Config{}, fmt.Errorf("empty dsn")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", db.Config{}, fmt.Errorf("parse dsn: %w", err)
	}
	if u.Scheme == "" {
		return "", db.Config{}, fmt.Errorf("dsn missing driver scheme (e.g. postgres://…)")
	}
	cfg := db.Config{
		Host:     u.Hostname(),
		Database: strings.TrimPrefix(u.Path, "/"),
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return "", db.Config{}, fmt.Errorf("dsn port %q: %w", p, err)
		}
		cfg.Port = n
	}
	if u.User != nil {
		cfg.User = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			cfg.Password = pw
		}
	}
	if q := u.Query(); len(q) > 0 {
		cfg.Options = map[string]string{}
		for k := range q {
			cfg.Options[k] = q.Get(k)
		}
	}
	return u.Scheme, cfg, nil
}
