package connectutil

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/config"
	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sshtunnel"
)

func TestResolveSavedConnectionRejectsIncompatibleProfileTransport(t *testing.T) {
	c := config.Connection{
		Name:      "invalid-composition",
		Profile:   "postgres",
		Transport: "mysql",
	}
	deps := RuntimeDeps{
		GetProfile: func(name string) (db.Profile, bool) {
			return db.Profile{Name: name, SupportedTransports: []string{"pgx"}}, true
		},
		GetTransport: func(name string) (db.Transport, bool) {
			return db.Transport{Name: name}, true
		},
	}

	_, err := ResolveSavedConnection(c, deps)
	if err == nil || !strings.Contains(err.Error(), `profile "postgres" does not support transport "mysql"`) {
		t.Fatalf("ResolveSavedConnection error = %v", err)
	}
}

func TestOpenResolvedConnectionRejectsIncompatiblePairBeforeTunnel(t *testing.T) {
	tunnelCalled := false
	resolved := ResolvedConnection{
		UseOpenWith: true,
		Profile: db.Profile{
			Name:                "postgres",
			SupportedTransports: []string{"pgx"},
		},
		Transport: db.Transport{Name: "mysql"},
		Tunnel:    &sshtunnel.Config{SSHHost: "jump"},
	}
	deps := RuntimeDeps{
		OpenTunnel: func(context.Context, sshtunnel.Config) (*sshtunnel.Tunnel, error) {
			tunnelCalled = true
			return nil, nil
		},
	}

	_, _, err := OpenResolvedConnection(context.Background(), resolved, deps)
	if err == nil || !strings.Contains(err.Error(), `profile "postgres" does not support transport "mysql"`) {
		t.Fatalf("OpenResolvedConnection error = %v", err)
	}
	if tunnelCalled {
		t.Fatal("OpenTunnel called for an incompatible profile/transport pair")
	}
}

func TestOpenResolvedConnectionPassesContextToTunnel(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "request")
	wantErr := errors.New("tunnel failed")
	called := false

	_, _, err := OpenResolvedConnection(ctx, ResolvedConnection{
		Tunnel: &sshtunnel.Config{SSHHost: "jump"},
	}, RuntimeDeps{
		OpenTunnel: func(got context.Context, _ sshtunnel.Config) (*sshtunnel.Tunnel, error) {
			called = true
			if got != ctx {
				t.Errorf("OpenTunnel received a different context")
			}
			if got.Value(contextKey{}) != "request" {
				t.Errorf("OpenTunnel context value = %v, want request", got.Value(contextKey{}))
			}
			return nil, wantErr
		},
	})
	if !called {
		t.Fatal("OpenTunnel was not called")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("OpenResolvedConnection error = %v, want %v", err, wantErr)
	}
}
