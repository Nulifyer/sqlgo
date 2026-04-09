// Package sshtunnel opens an SSH jump connection and runs a loopback
// TCP listener that forwards every accepted connection to a target
// address reachable from the SSH host. This lets sqlgo talk to a
// database behind a bastion without every engine adapter growing its
// own SSH wiring: the tunnel rewrites the target address to
// 127.0.0.1:<local-port>, and the driver's DSN sees a plain local
// socket.
//
// Auth: key file takes precedence over password when both are set,
// matching the form's contract. Host key verification is currently
// disabled (ssh.InsecureIgnoreHostKey) -- matching the "same as sqlit"
// baseline -- with a TODO to add known_hosts support later.
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

// Config describes what to tunnel. TargetHost/TargetPort are the
// database host and port as reachable *from the SSH host*, not from
// the local machine. SSH auth is password or key file (key wins).
type Config struct {
	SSHHost     string
	SSHPort     int
	SSHUser     string
	SSHPassword string
	SSHKeyPath  string

	TargetHost string
	TargetPort int
}

// Tunnel is an active jump connection. LocalHost/LocalPort give the
// loopback address the caller should dial instead of the real target;
// Close tears down the listener, the accept loop, and the underlying
// SSH client.
type Tunnel struct {
	LocalHost string
	LocalPort int

	client   *ssh.Client
	listener net.Listener
	wg       sync.WaitGroup
	mu       sync.Mutex
	closed   bool
}

// Open establishes the SSH client, starts the local listener, and
// returns once the tunnel is ready to accept connections. Any error
// tears down whatever was already set up so the caller never sees a
// half-open tunnel.
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

	clientCfg := &ssh.ClientConfig{
		User:            cfg.SSHUser,
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // TODO: known_hosts
	}
	sshAddr := net.JoinHostPort(cfg.SSHHost, strconv.Itoa(cfg.SSHPort))
	client, err := ssh.Dial("tcp", sshAddr, clientCfg)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", sshAddr, err)
	}

	// Listen on an ephemeral loopback port. 127.0.0.1 specifically so
	// the forwarded port isn't exposed on the LAN.
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

// Close shuts down the listener and SSH client. Safe to call multiple
// times. Returns the first underlying error, if any.
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

// acceptLoop accepts local connections and hands each one to a pair
// of io.Copy goroutines that shuffle bytes between the local socket
// and the SSH-forwarded remote socket.
func (t *Tunnel) acceptLoop(targetHost string, targetPort int) {
	defer t.wg.Done()
	targetAddr := net.JoinHostPort(targetHost, strconv.Itoa(targetPort))
	for {
		local, err := t.listener.Accept()
		if err != nil {
			// Listener closed (via Close) or the OS killed it. Either
			// way, exit the loop cleanly -- there's nothing to log.
			return
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

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(remote, local)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(local, remote)
		done <- struct{}{}
	}()
	<-done
}

// buildAuth constructs the ssh.AuthMethod slice from the config. Key
// file takes precedence over password when both are set. An empty
// auth set is an error -- we won't fall back to ssh-agent silently
// because that would be surprising when neither the form nor the
// config mention it.
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
