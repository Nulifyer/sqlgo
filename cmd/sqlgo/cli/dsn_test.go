package cli

import (
	"net/url"
	"reflect"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
)

func TestParseDSNIPv6(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		raw      string
		wantHost string
		wantPort int
	}{
		{
			name:     "bracketed_ipv6",
			raw:      "postgres://user:pass@[2001:db8::1]:5432/app",
			wantHost: "2001:db8::1",
			wantPort: 5432,
		},
		{
			name:     "ipv6_loopback_without_port",
			raw:      "mysql://root@[::1]/app",
			wantHost: "::1",
		},
		{
			name:     "ipv6_zone",
			raw:      "trino://sqlgo@[fe80::1%25eth0]:8080/hive",
			wantHost: "fe80::1%eth0",
			wantPort: 8080,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, got, err := parseDSN(tc.raw)
			if err != nil {
				t.Fatalf("parseDSN(%q): %v", tc.raw, err)
			}
			if got.Host != tc.wantHost || got.Port != tc.wantPort {
				t.Fatalf("parseDSN(%q) host/port = %q/%d, want %q/%d", tc.raw, got.Host, got.Port, tc.wantHost, tc.wantPort)
			}
		})
	}
}

func TestParseDSNIPv6RoundTrip(t *testing.T) {
	t.Parallel()
	tests := []string{
		"postgres://user:pass@[2001:db8::1]:5432/app?sslmode=require",
		"trino://sqlgo@[fe80::1%25eth0]:8080/hive?schema=default",
	}
	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			driverName, cfg, err := parseDSN(raw)
			if err != nil {
				t.Fatalf("first parseDSN(%q): %v", raw, err)
			}
			query := url.Values{}
			for key, value := range cfg.Options {
				query.Set(key, value)
			}
			u := url.URL{
				Scheme:   driverName,
				User:     url.UserPassword(cfg.User, cfg.Password),
				Host:     db.JoinHostPort(cfg.Host, cfg.Port),
				Path:     "/" + cfg.Database,
				RawQuery: query.Encode(),
			}
			gotDriver, gotCfg, err := parseDSN(u.String())
			if err != nil {
				t.Fatalf("second parseDSN(%q): %v", u.String(), err)
			}
			if gotDriver != driverName || !reflect.DeepEqual(gotCfg, cfg) {
				t.Fatalf("round trip = %q/%#v, want %q/%#v", gotDriver, gotCfg, driverName, cfg)
			}
		})
	}
}
