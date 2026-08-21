package sshtunnel

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestOpenContextCanceledBeforeDial(t *testing.T) {
	_ = isolateKnownHosts(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := OpenContext(ctx, Config{
		SSHHost:     "127.0.0.1",
		SSHPort:     1,
		SSHUser:     "user",
		SSHPassword: "password",
		TargetHost:  "database",
		TargetPort:  5432,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenContext error = %v, want context.Canceled", err)
	}
}

func TestOpenContextExpiredDeadlineBeforeDial(t *testing.T) {
	_ = isolateKnownHosts(t)
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer cancel()

	_, err := OpenContext(ctx, Config{
		SSHHost:     "127.0.0.1",
		SSHPort:     1,
		SSHUser:     "user",
		SSHPassword: "password",
		TargetHost:  "database",
		TargetPort:  5432,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("OpenContext error = %v, want context.DeadlineExceeded", err)
	}
}

func TestOpenContextCancelsStalledHandshakeAndClosesSocket(t *testing.T) {
	_ = isolateKnownHosts(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	accepted := make(chan struct{})
	closed := make(chan struct{})
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			close(accepted)
			close(closed)
			return
		}
		close(accepted)
		_, _ = io.Copy(io.Discard, conn)
		_ = conn.Close()
		close(closed)
	}()

	host, port := splitTCPAddr(t, listener.Addr())
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, openErr := OpenContext(ctx, Config{
			SSHHost:     host,
			SSHPort:     port,
			SSHUser:     "user",
			SSHPassword: "password",
			TargetHost:  "database",
			TargetPort:  5432,
		})
		result <- openErr
	}()

	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("TCP connection was not accepted")
	}
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("OpenContext error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OpenContext did not return after cancellation")
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not observe the canceled client socket closing")
	}
}

func TestOpenContextPreservesHostKeyErrorClassification(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, host string, port int)
		check   func(error) bool
	}{
		{
			name:    "unknown host",
			prepare: func(*testing.T, string, int) {},
			check: func(err error) bool {
				var target *UnknownHostError
				return errors.As(err, &target)
			},
		},
		{
			name: "host key mismatch",
			prepare: func(t *testing.T, host string, port int) {
				if err := AppendKnownHost(host, port, newTestKey(t)); err != nil {
					t.Fatalf("AppendKnownHost: %v", err)
				}
			},
			check: func(err error) bool {
				var target *HostKeyMismatchError
				return errors.As(err, &target)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = isolateKnownHosts(t)
			host, port, stop := startSSHHandshakeServer(t)
			defer stop()
			tt.prepare(t, host, port)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, err := OpenContext(ctx, Config{
				SSHHost:     host,
				SSHPort:     port,
				SSHUser:     "user",
				SSHPassword: "password",
				TargetHost:  "database",
				TargetPort:  5432,
			})
			if !tt.check(err) {
				t.Fatalf("OpenContext error = %T (%v), classification not preserved", err, err)
			}
		})
	}
}

func splitTCPAddr(t *testing.T, addr net.Addr) (string, int) {
	t.Helper()
	host, portText, err := net.SplitHostPort(addr.String())
	if err != nil {
		t.Fatalf("split address %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse port %q: %v", portText, err)
	}
	return host, port
}

func startSSHHandshakeServer(t *testing.T) (host string, port int, stop func()) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatalf("create server signer: %v", err)
	}
	serverCfg := &ssh.ServerConfig{NoClientAuth: true}
	serverCfg.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		serverConn, _, _, _ := ssh.NewServerConn(conn, serverCfg)
		if serverConn != nil {
			_ = serverConn.Close()
		}
	}()

	host, port = splitTCPAddr(t, listener.Addr())
	stop = func() {
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("SSH handshake server did not stop")
		}
	}
	return host, port, stop
}
