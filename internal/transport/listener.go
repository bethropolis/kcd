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
	// keepAliveIdle overrides the first-probe delay for accepted
	// connections; zero keeps DefaultKeepAliveIdle.
	keepAliveIdle time.Duration
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

// SetKeepAliveIdle overrides the keepalive first-probe delay for
// subsequently accepted connections. Must be called before serving.
func (l *Listener) SetKeepAliveIdle(d time.Duration) {
	l.keepAliveIdle = d
}

// Accept waits for and returns the next connection.
func (l *Listener) Accept() (net.Conn, error) {
	conn, err := l.l.Accept()
	if err != nil {
		return nil, err
	}

	SetTCPKeepAlive(conn, l.keepAliveIdle)

	return conn, nil
}

// Close closes the underlying listener.
func (l *Listener) Close() error {
	return l.l.Close()
}
