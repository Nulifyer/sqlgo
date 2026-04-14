# SSH Tunneling

sqlgo can route any engine connection through an SSH jump host. The
driver sees a plain `127.0.0.1:PORT` socket, so tunneling is transparent
to the adapters and works for every supported engine.

Code: [internal/sshtunnel/](../internal/sshtunnel/).

## How it works

1. `sshtunnel.Open` dials the SSH server and opens a listener on
   `127.0.0.1:0` (loopback only -- the forwarded port is never
   exposed on the LAN).
2. Each accepted local socket triggers a remote `ssh.Dial` to
   `TargetHost:TargetPort` (reachable *from the SSH host*, not from
   the local machine).
3. Bytes are copied in both directions. When either direction ends,
   both sockets are closed so the peer copy unblocks promptly.
4. On `Close`, the listener and SSH client are torn down and the
   accept loop + in-flight connections all exit.

## Configuring a tunnel

The connection form exposes the same fields as
[sshtunnel.Config](../internal/sshtunnel/tunnel.go):

| Field | Meaning |
|---|---|
| `SSHHost` / `SSHPort` | the jump host. Default port is 22. |
| `SSHUser` | username on the jump host. |
| `SSHKeyPath` | path to a private key file. Takes precedence over the password. |
| `SSHPassword` | password for the jump user when no key file is set. |
| `TargetHost` / `TargetPort` | the database host/port as seen from the SSH host. |

Key-file auth is preferred; password auth is supported as a fallback.
**`ssh-agent` is intentionally not used** -- supply a key file or a
password. Omitting both is an error, not a silent agent probe.

## Host-key verification

Host keys are verified against `~/.ssh/known_hosts` (or the path set
via `TestOnlySetKnownHostsPath`). The file and its parent directory
are created on first use with mode `0600` / `0700`.

Three outcomes:

- **Known matching key.** Passes silently.
- **Unknown host.** `Open` returns `*UnknownHostError` carrying the
  presented `ssh.PublicKey`. The TUI surfaces the key fingerprint in
  a trust prompt; accepting the prompt calls
  `sshtunnel.AppendKnownHost` and retries `Open`. This is standard
  TOFU ("trust on first use").
- **Stored key differs from presented key.** `Open` returns
  `*HostKeyMismatchError`. **Unrecoverable** -- the TUI does not
  offer a bypass. The operator must edit `known_hosts` by hand after
  confirming the key change out of band. Silent override would
  defeat the only MITM signal the client has.

### Error types

Both sentinel errors are typed so callers can match them without
string parsing. `ssh.Dial` failures that are neither sentinel are
returned as a wrapped `fmt.Errorf("ssh dial %s: %w", addr, err)`.

```go
t, err := sshtunnel.Open(cfg)
var unknown *sshtunnel.UnknownHostError
var mismatch *sshtunnel.HostKeyMismatchError
switch {
case errors.As(err, &unknown):
    // prompt user, on accept: sshtunnel.AppendKnownHost(host, port, key); retry
case errors.As(err, &mismatch):
    // fatal; surface the error, do not retry, do not auto-append
case err != nil:
    // generic failure (auth, network, config)
}
```

### known_hosts format

Entries are written in OpenSSH format via `knownhosts.Line`. Port 22
writes the bare hostname; other ports write `[host]:port`. sqlgo never
rewrites or reorders existing entries -- it only appends.

## Lifecycle

`Tunnel.Close` is idempotent and tears down, in order, the listener,
the SSH client, and waits for the accept loop and all in-flight
`handleConn` goroutines to drain. The TUI closes the `db.Conn` first
and then the tunnel, so any lingering reads on the forwarded socket
see a clean "driver closed" error first.

## Threading

- The accept loop and every per-connection copy pair run as goroutines
  tracked by a single `sync.WaitGroup`.
- When one direction's `io.Copy` returns, it closes both sockets so
  the other direction's copy returns immediately. Without that, one
  goroutine could outlive `Close` until the remote peer happened to
  time out.

## Troubleshooting

- **`host is not in known_hosts`**: first-time connection. Accept the
  TOFU prompt in the TUI.
- **`host key for ... does NOT match known_hosts`**: stored key
  differs. Verify the server's real host key (typically via
  `ssh-keygen -F` on a trusted host, or out-of-band with the server
  operator) before editing `known_hosts`.
- **`no ssh auth method configured`**: neither `SSHKeyPath` nor
  `SSHPassword` is set on the connection.
- **`read key ...`** / **`parse key ...`**: the key file is missing,
  unreadable, or encrypted. Encrypted keys are not currently
  supported -- decrypt or use a separate unlocked key.
