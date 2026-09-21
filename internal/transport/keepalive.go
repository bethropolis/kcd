package transport

import (
	"net"
	"time"
)

// TCP keepalive probe schedule, shared by the inbound listener and the
// outbound dial path. One definition keeps both ends symmetric —
// asymmetric timeouts are a classic source of one-sided zombie
// connections where one end holds a dead socket the other has dropped.
const (
	keepAliveIdle     = 30 * time.Second
	keepAliveInterval = 10 * time.Second
	keepAliveCount    = 3
)

// SetTCPKeepAlive enables keepalive probing on conn. Non-TCP connections
// are left untouched. Falls back to the legacy period-only API where the
// full config call is unavailable.
func SetTCPKeepAlive(conn net.Conn) {
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return
	}
	if err := tcpConn.SetKeepAliveConfig(net.KeepAliveConfig{
		Enable:   true,
		Idle:     keepAliveIdle,
		Interval: keepAliveInterval,
		Count:    keepAliveCount,
	}); err != nil {
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(keepAliveIdle)
	}
}
