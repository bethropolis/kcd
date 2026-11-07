package transport

import (
	"net"
	"sync"
	"time"
)

// IdleTimeoutConn bounds silence, not size: reads/writes push the deadline
// forward, so stalled sockets fail instead of hanging on TCP keepalive.
type IdleTimeoutConn struct {
	net.Conn
	idle             time.Duration
	mu               sync.Mutex
	lastReadRefresh  time.Time
	lastWriteRefresh time.Time
}

// WithIdleTimeout wraps conn so any read or write gap longer than idle
// fails with a timeout error.
func WithIdleTimeout(conn net.Conn, idle time.Duration) net.Conn {
	if conn == nil || idle <= 0 {
		return conn
	}
	now := time.Now()
	_ = conn.SetDeadline(now.Add(idle))
	return &IdleTimeoutConn{
		Conn:             conn,
		idle:             idle,
		lastReadRefresh:  now,
		lastWriteRefresh: now,
	}
}

// Read refreshes the read deadline if more than idle/4 has elapsed, then reads.
func (c *IdleTimeoutConn) Read(b []byte) (int, error) {
	now := time.Now()
	c.mu.Lock()
	if now.Sub(c.lastReadRefresh) >= c.idle/4 {
		c.lastReadRefresh = now
		c.mu.Unlock()
		if err := c.SetReadDeadline(now.Add(c.idle)); err != nil {
			return 0, err
		}
	} else {
		c.mu.Unlock()
	}
	return c.Conn.Read(b)
}

// Write refreshes the write deadline if more than idle/4 has elapsed, then writes.
func (c *IdleTimeoutConn) Write(b []byte) (int, error) {
	now := time.Now()
	c.mu.Lock()
	if now.Sub(c.lastWriteRefresh) >= c.idle/4 {
		c.lastWriteRefresh = now
		c.mu.Unlock()
		if err := c.SetWriteDeadline(now.Add(c.idle)); err != nil {
			return 0, err
		}
	} else {
		c.mu.Unlock()
	}
	return c.Conn.Write(b)
}
