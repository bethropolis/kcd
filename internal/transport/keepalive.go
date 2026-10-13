package transport

import (
	"net"
	"time"
)

// DefaultKeepAliveIdle is the first-probe delay absent an explicit idle,
// shared by both directions: asymmetric timeouts breed one-sided zombies.
const (
	DefaultKeepAliveIdle = 30 * time.Second
	keepAliveInterval    = 10 * time.Second
	keepAliveCount       = 3
)

// SetTCPKeepAlive enables keepalive probing with the given idle delay (or
// the default when non-positive); non-TCP connections are left untouched.
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
