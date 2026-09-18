package transport

import (
	"net"
	"time"
)

// IdleTimeoutConn bounds silence, not size: every Read or Write pushes its
// deadline forward by the idle interval, so slow-but-alive transfers run as
// long as they need while a stalled socket (walked-out-of-range phone,
// half-open zombie) fails promptly instead of hanging on TCP keepalive.
// Wrap with WithIdleTimeout; a non-positive interval returns conn unchanged.
type IdleTimeoutConn struct {
	net.Conn
	idle time.Duration
}

// WithIdleTimeout wraps conn so any read or write gap longer than idle
// fails with a timeout error.
func WithIdleTimeout(conn net.Conn, idle time.Duration) net.Conn {
	if conn == nil || idle <= 0 {
		return conn
	}
	return &IdleTimeoutConn{Conn: conn, idle: idle}
}

// Read sets a fresh read deadline before every read.
func (c *IdleTimeoutConn) Read(b []byte) (int, error) {
	if err := c.SetReadDeadline(time.Now().Add(c.idle)); err != nil {
		return 0, err
	}
	return c.Conn.Read(b)
}

// Write sets a fresh write deadline before every write.
func (c *IdleTimeoutConn) Write(b []byte) (int, error) {
	if err := c.SetWriteDeadline(time.Now().Add(c.idle)); err != nil {
		return 0, err
	}
	return c.Conn.Write(b)
}
