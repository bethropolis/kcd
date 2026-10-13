package transport

import (
	"net"
	"time"
)

// IdleTimeoutConn bounds silence, not size: reads/writes push the deadline
// forward, so stalled sockets fail instead of hanging on TCP keepalive.
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
