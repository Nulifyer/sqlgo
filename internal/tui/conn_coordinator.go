package tui

import "sync"

// connCoordinator serializes every I/O operation against the shared db.Conn.
// A query holds the lease until its Rows cursor is closed, matching db.Conn's
// exclusive-use contract rather than only protecting the initial Query call.
type connCoordinator struct {
	mu sync.Mutex
}

func (c *connCoordinator) acquire() func() {
	c.mu.Lock()
	return c.mu.Unlock
}
