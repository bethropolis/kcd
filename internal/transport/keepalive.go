package transport

import (
	"net"
	"time"
)

// DefaultKeepAliveIdle is the first-probe delay when callers pass no
// explicit idle. Shared by the inbound listener and the outbound dial
// path — asymmetric timeouts are a classic source of one-sided zombie
// connections where one end holds a dead socket the other has dropped.
const (
	DefaultKeepAliveIdle = 30 * time.Second
	keepAliveInterval    = 10 * time.Second
	keepAliveCount       = 3
)

// SetTCPKeepAlive enables keepalive probing on conn with the given idle
// delay. Non-positive idle falls back to DefaultKeepAliveIdle. Non-TCP
// connections are left untouched. Falls back to the legacy period-only
// API where the full config call is unavailable.
func SetTCPKeepAlive(conn net.Conn, idle time.Duration) {
	if idle <= 0 {
		idle = DefaultKeepAliveIdle
	}
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return
	}
	if err := tcpConn.SetKeepAliveConfig(net.KeepAliveConfig{
		Enable:   true,
		Idle:     idle,
		Interval: keepAliveInterval,
		Count:    keepAliveCount,
	}); err != nil {
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(idle)
	}
}
