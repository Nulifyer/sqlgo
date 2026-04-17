package connectutil

import (
	"context"
	"fmt"

	"github.com/Nulifyer/sqlgo/internal/config"
	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/secret"
	"github.com/Nulifyer/sqlgo/internal/sshtunnel"
)

// SSHKeyringAccount is the account name used for a connection's SSH
// tunnel password when it is stored in the OS keyring.
func SSHKeyringAccount(connName string) string {
	return connName + ":ssh"
}

// RuntimeDeps contains the narrow runtime hooks needed to resolve and
// open a saved connection. DefaultRuntimeDeps wires these to the real
// db/secret/sshtunnel packages; tests can swap in fakes.
type RuntimeDeps struct {
	Secrets      secret.Store
	GetDriver    func(name string) (db.Driver, error)
	GetProfile   func(name string) (db.Profile, bool)
	GetTransport func(name string) (db.Transport, bool)
	OpenWith     func(ctx context.Context, p db.Profile, t db.Transport, cfg db.Config) (db.Conn, error)
	OpenTunnel   func(cfg sshtunnel.Config) (*sshtunnel.Tunnel, error)
}

// DefaultRuntimeDeps returns the production dependency set.
func DefaultRuntimeDeps(secrets secret.Store) RuntimeDeps {
	return RuntimeDeps{
		Secrets:      secrets,
		GetDriver:    db.Get,
		GetProfile:   db.GetProfile,
		GetTransport: db.GetTransport,
		OpenWith:     db.OpenWith,
		OpenTunnel:   sshtunnel.Open,
	}
}

// ResolvedConnection is the dial-ready form of a saved connection after
// keyring placeholders and driver/profile routing have been resolved.
type ResolvedConnection struct {
	Config      db.Config
	Driver      db.Driver
	UseOpenWith bool
	Profile     db.Profile
	Transport   db.Transport
	Tunnel      *sshtunnel.Config
}

// ResolveSavedConnection turns a saved connection row into the runtime
// shape needed to open it. Password placeholders are resolved through
// deps.Secrets, and "Other..." connections are translated to
// profile+transport routing.
func ResolveSavedConnection(c config.Connection, deps RuntimeDeps) (ResolvedConnection, error) {
	pass, err := secret.Resolve(deps.Secrets, c.Name, c.Password)
	if err != nil {
		return ResolvedConnection{}, fmt.Errorf("password for %q: %w", c.Name, err)
	}
	resolved := ResolvedConnection{
		Config: db.Config{
			Host:     c.Host,
			Port:     c.Port,
			User:     c.User,
			Password: pass,
			Database: c.Database,
			Options:  c.Options,
		},
	}
	if c.Profile != "" && c.Transport != "" {
		if deps.GetProfile == nil {
			return ResolvedConnection{}, fmt.Errorf("unknown profile: %s", c.Profile)
		}
		profile, ok := deps.GetProfile(c.Profile)
		if !ok {
			return ResolvedConnection{}, fmt.Errorf("unknown profile: %s", c.Profile)
		}
		if deps.GetTransport == nil {
			return ResolvedConnection{}, fmt.Errorf("unknown transport: %s", c.Transport)
		}
		transport, ok := deps.GetTransport(c.Transport)
		if !ok {
			return ResolvedConnection{}, fmt.Errorf("unknown transport: %s", c.Transport)
		}
		resolved.UseOpenWith = true
		resolved.Profile = profile
		resolved.Transport = transport
	} else {
		if deps.GetDriver == nil {
			return ResolvedConnection{}, fmt.Errorf("db: driver %q not registered", c.Driver)
		}
		driver, err := deps.GetDriver(c.Driver)
		if err != nil {
			return ResolvedConnection{}, err
		}
		resolved.Driver = driver
	}

	if c.SSH.Host != "" {
		sshPass := c.SSH.Password
		if sshPass == secret.Placeholder {
			if deps.Secrets == nil {
				return ResolvedConnection{}, fmt.Errorf("ssh password for %q: password in keyring but no store available", c.Name)
			}
			resolvedPass, err := deps.Secrets.Get(SSHKeyringAccount(c.Name))
			if err != nil {
				return ResolvedConnection{}, fmt.Errorf("ssh password for %q: %w", c.Name, err)
			}
			sshPass = resolvedPass
		}
		resolved.Tunnel = &sshtunnel.Config{
			SSHHost:     c.SSH.Host,
			SSHPort:     c.SSH.Port,
			SSHUser:     c.SSH.User,
			SSHPassword: sshPass,
			SSHKeyPath:  c.SSH.KeyPath,
			TargetHost:  c.Host,
			TargetPort:  c.Port,
		}
	}
	return resolved, nil
}

// OpenResolvedConnection opens the underlying db.Conn and, when
// configured, the SSH tunnel that fronts it. Tunnel setup is torn down
// automatically if the DB open fails.
func OpenResolvedConnection(ctx context.Context, resolved ResolvedConnection, deps RuntimeDeps) (db.Conn, *sshtunnel.Tunnel, error) {
	cfg := resolved.Config
	var tunnel *sshtunnel.Tunnel
	if resolved.Tunnel != nil {
		if deps.OpenTunnel == nil {
			return nil, nil, fmt.Errorf("ssh tunnel: open function not configured")
		}
		t, err := deps.OpenTunnel(*resolved.Tunnel)
		if err != nil {
			return nil, nil, err
		}
		tunnel = t
		cfg.Host = t.LocalHost
		cfg.Port = t.LocalPort
	}

	var (
		conn db.Conn
		err  error
	)
	if resolved.UseOpenWith {
		if deps.OpenWith == nil {
			err = fmt.Errorf("openwith: function not configured")
		} else {
			conn, err = deps.OpenWith(ctx, resolved.Profile, resolved.Transport, cfg)
		}
	} else {
		conn, err = resolved.Driver.Open(ctx, cfg)
	}
	if err != nil {
		if tunnel != nil {
			_ = tunnel.Close()
		}
		return nil, nil, err
	}
	return conn, tunnel, nil
}
