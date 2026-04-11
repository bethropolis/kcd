package transport

import (
	"fmt"
	"net"
	"time"
)

// Listener wraps a net.Listener.
type Listener struct {
	l net.Listener
}

// Listen starts a TCP listener on the given TCP address.
func Listen(addr string) (*Listener, error) {
	l, err := net.Listen("tcp", addr)
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
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(30 * time.Second)
	}

	return conn, nil
}

// Close closes the underlying listener.
func (l *Listener) Close() error {
	return l.l.Close()
}
