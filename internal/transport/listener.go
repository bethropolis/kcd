package transport

import (
	"context"
	"fmt"
	"net"
	"time"
)

// Listener wraps a net.Listener.
type Listener struct {
	l net.Listener
}

// Listen starts a TCP listener on the given TCP address.
func Listen(ctx context.Context, addr string) (*Listener, error) {
	lc := net.ListenConfig{}
	l, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("transport: listen on %s: %w", addr, err)
	}

	return &Listener{l: l}, nil
}

// Accept waits for and returns the next connection.
func (l *Listener) Accept() (net.Conn, error) {
	conn, err := l.l.Accept()
	if err != nil {
		return nil, err
	}

	if tcpConn, ok := conn.(*net.TCPConn); ok {
		if err := tcpConn.SetKeepAliveConfig(net.KeepAliveConfig{
			Enable:   true,
			Idle:     30 * time.Second,
			Interval: 10 * time.Second,
			Count:    3,
		}); err != nil {
			_ = tcpConn.SetKeepAlive(true)
			_ = tcpConn.SetKeepAlivePeriod(30 * time.Second)
		}
	}

	return conn, nil
}

// Close closes the underlying listener.
func (l *Listener) Close() error {
	return l.l.Close()
}
