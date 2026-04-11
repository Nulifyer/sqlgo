//go:build integration

package sshtunnel

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	_ "github.com/Nulifyer/sqlgo/internal/db/postgres"
)

// TestIntegrationTunnelToPostgres dials the compose.yaml sshd
// service as a jump host and forwards to the postgres service,
// then opens a db.Conn through the forwarded socket. This is
// the end-to-end shape the picker's "SSH tunnel" checkbox
// produces in production.
//
// Known-hosts verification: on a fresh compose up the sshd
// container's key isn't in ~/.ssh/known_hosts yet. We point
// the sshtunnel package at a temp known_hosts, then do the
// trust-on-first-use dance manually: expect UnknownHostError
// on the first Open, call AppendKnownHost, retry.
func TestIntegrationTunnelToPostgres(t *testing.T) {
	kh := t.TempDir() + "/known_hosts"
	restore := TestOnlySetKnownHostsPath(kh)
	defer restore()

	cfg := Config{
		SSHHost:     envOr("SQLGO_IT_SSH_HOST", "127.0.0.1"),
		SSHPort:     12222,
		SSHUser:     envOr("SQLGO_IT_SSH_USER", "sqlgo"),
		SSHPassword: envOr("SQLGO_IT_SSH_PASSWORD", "sqlgo_dev"),
		TargetHost:  "postgres", // compose network alias
		TargetPort:  5432,
	}

	// First attempt: expect UnknownHostError and the key.
	tunnel, err := Open(cfg)
	var unknown *UnknownHostError
	if err == nil {
		// Already trusted (maybe a prior test run left an
		// entry). Roll with it.
		defer tunnel.Close()
	} else if errors.As(err, &unknown) {
		if err := AppendKnownHost(unknown.Host, unknown.Port, unknown.Key); err != nil {
			t.Fatalf("AppendKnownHost: %v", err)
		}
		tunnel, err = Open(cfg)
		if err != nil {
			t.Fatalf("Open after trust: %v", err)
		}
		defer tunnel.Close()
	} else {
		t.Fatalf("Open (is compose up?): %v", err)
	}

	// Now dial postgres through the tunnel's loopback port.
	pg, err := db.Get("postgres")
	if err != nil {
		t.Fatalf("db.Get postgres: %v", err)
	}
	dbCfg := db.Config{
		Host:     tunnel.LocalHost,
		Port:     tunnel.LocalPort,
		User:     envOr("SQLGO_IT_PG_USER", "sqlgo"),
		Password: envOr("SQLGO_IT_PG_PASSWORD", "sqlgo_dev"),
		Database: envOr("SQLGO_IT_PG_DB", "sqlgo_test"),
		Options:  map[string]string{"sslmode": "disable"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pg.Open(ctx, dbCfg)
	if err != nil {
		t.Fatalf("open postgres via tunnel: %v", err)
	}
	defer conn.Close()

	if err := conn.Ping(ctx); err != nil {
		t.Errorf("ping through tunnel: %v", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
