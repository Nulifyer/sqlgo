// Package sshtunnel opens an SSH jump connection and forwards a
// loopback listener to a target reachable from the SSH host. The
// driver's DSN sees a plain local socket; every engine adapter
// gets SSH support for free.
//
// Auth: key file > password. Host keys verified against
// ~/.ssh/known_hosts (see knownhosts.go). Unknown hosts return
// *UnknownHostError (caller prompts + retries). Mismatches return
// *HostKeyMismatchError (fatal, no override).
package sshtunnel

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"

	"golang.org/x/crypto/ssh"
)

// Config describes a tunnel. TargetHost/TargetPort are reachable
// from the SSH host, not from the local machine.
type Config struct {
	SSHHost     string
	SSHPort     int
	SSHUser     string
	SSHPassword string
	SSHKeyPath  string

	TargetHost string
	TargetPort int
}

// Tunnel is an active jump connection. Dial LocalHost:LocalPort
// instead of the real target. Close tears down everything.
type Tunnel struct {
	LocalHost string
	LocalPort int

	client   *ssh.Client
	listener net.Listener
	wg       sync.WaitGroup
	mu       sync.Mutex
	closed   bool
}

// Open dials the SSH server, starts the local listener, and
// returns a Tunnel ready to forward. Errors tear down any
// partial state.
func Open(cfg Config) (*Tunnel, error) {
	if cfg.SSHHost == "" {
		return nil, errors.New("ssh tunnel: empty ssh host")
	}
	if cfg.SSHPort == 0 {
		cfg.SSHPort = 22
	}
	if cfg.TargetHost == "" {
		return nil, errors.New("ssh tunnel: empty target host")
	}
	if cfg.TargetPort == 0 {
		return nil, errors.New("ssh tunnel: empty target port")
	}

	auth, err := buildAuth(cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh tunnel auth: %w", err)
	}

	hostKeyCb, err := buildHostKeyCallback(cfg.SSHHost, cfg.SSHPort)
	if err != nil {
		return nil, fmt.Errorf("ssh tunnel host key: %w", err)
	}
	clientCfg := &ssh.ClientConfig{
		User:            cfg.SSHUser,
		Auth:            auth,
		HostKeyCallback: hostKeyCb,
	}
	sshAddr := net.JoinHostPort(cfg.SSHHost, strconv.Itoa(cfg.SSHPort))
	client, err := ssh.Dial("tcp", sshAddr, clientCfg)
	if err != nil {
		// Surface the known-host sentinels unwrapped so callers
		// can type-check without peeling an fmt.Errorf wrapper.
		var unknown *UnknownHostError
		if errors.As(err, &unknown) {
			return nil, unknown
		}
		var mismatch *HostKeyMismatchError
		if errors.As(err, &mismatch) {
			return nil, mismatch
		}
		return nil, fmt.Errorf("ssh dial %s: %w", sshAddr, err)
	}

	// Loopback-only so the forwarded port isn't exposed on the LAN.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ssh tunnel listen: %w", err)
	}
	tcpAddr := listener.Addr().(*net.TCPAddr)

	t := &Tunnel{
		LocalHost: "127.0.0.1",
		LocalPort: tcpAddr.Port,
		client:    client,
		listener:  listener,
	}
	t.wg.Add(1)
	go t.acceptLoop(cfg.TargetHost, cfg.TargetPort)
	return t, nil
}

// Close tears down the listener and SSH client. Idempotent.
func (t *Tunnel) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.mu.Unlock()

	var firstErr error
	if err := t.listener.Close(); err != nil {
		firstErr = err
	}
	if err := t.client.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	t.wg.Wait()
	return firstErr
}

// acceptLoop shuffles bytes between each accepted local socket
// and an SSH-forwarded remote socket.
func (t *Tunnel) acceptLoop(targetHost string, targetPort int) {
	defer t.wg.Done()
	targetAddr := net.JoinHostPort(targetHost, strconv.Itoa(targetPort))
	for {
		local, err := t.listener.Accept()
		if err != nil {
			return // listener closed
		}
		t.wg.Add(1)
		go t.handleConn(local, targetAddr)
	}
}

func (t *Tunnel) handleConn(local net.Conn, targetAddr string) {
	defer t.wg.Done()
	defer local.Close()
	remote, err := t.client.Dial("tcp", targetAddr)
	if err != nil {
		return
	}
	defer remote.Close()

	// Wait for both directions. When one side closes, shut the other
	// connection so its io.Copy unblocks instead of leaking until the
	// peer notices. Without this, Close() races the inner goroutines.
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(remote, local)
		_ = remote.Close()
		_ = local.Close()
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(local, remote)
		_ = local.Close()
		_ = remote.Close()
		done <- struct{}{}
	}()
	<-done
	<-done
}

// buildAuth returns ssh.AuthMethod from cfg. Key > password.
// Empty auth is an error (no silent ssh-agent fallback).
func buildAuth(cfg Config) ([]ssh.AuthMethod, error) {
	var auth []ssh.AuthMethod
	if cfg.SSHKeyPath != "" {
		data, err := os.ReadFile(cfg.SSHKeyPath)
		if err != nil {
			return nil, fmt.Errorf("read key %s: %w", cfg.SSHKeyPath, err)
		}
		signer, err := ssh.ParsePrivateKey(data)
		if err != nil {
			return nil, fmt.Errorf("parse key %s: %w", cfg.SSHKeyPath, err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	} else if cfg.SSHPassword != "" {
		auth = append(auth, ssh.Password(cfg.SSHPassword))
	}
	if len(auth) == 0 {
		return nil, errors.New("no ssh auth method configured (set key or password)")
	}
	return auth, nil
}
