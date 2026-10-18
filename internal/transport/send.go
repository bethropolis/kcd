package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/log"
)

// ListenSidechannel binds the first available port in the configured range.
func ListenSidechannel(ctx context.Context, portMin, portMax int) (net.Listener, int, error) {
	lc := net.ListenConfig{KeepAlive: 30 * time.Second}
	for port := portMin; port <= portMax; port++ {
		ln, err := lc.Listen(ctx, "tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			return ln, port, nil
		}
	}
	return nil, 0, fmt.Errorf("transport: no available ports in range %d-%d", portMin, portMax)
}

// AcceptAndSend authenticates a peer and streams a file over a side-channel.
func AcceptAndSend(ln net.Listener, path string, tlsConfig *tls.Config, expectedID, expectedFP string, timeout time.Duration, onProgress func(int64, int64), logger log.Logger, portMin, portMax int, options ...SidechannelOptions) error {
	defer ln.Close()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	go func() { <-ctx.Done(); _ = ln.Close() }()
	conn, err := ln.Accept()
	if err != nil {
		return fmt.Errorf("transport: accept side-channel: %w", err)
	}
	defer conn.Close()
	tlsConn := tls.Server(conn, tlsConfig)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return fmt.Errorf("transport: side-channel handshake: %w", err)
	}
	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 || !cert.MatchesDeviceID(state.PeerCertificates[0], expectedID) {
		return fmt.Errorf("transport: unexpected side-channel peer identity")
	}
	if err := cert.VerifySideChannelPeer(state, expectedFP); err != nil {
		return fmt.Errorf("transport: side-channel peer verification: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("transport: open payload: %w", err)
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return fmt.Errorf("transport: stat payload: %w", err)
	}
	var dst net.Conn = tlsConn
	if len(options) > 0 {
		dst = WithIdleTimeout(tlsConn, options[0].IdleTimeout)
	}
	var src io.Reader = f
	if onProgress != nil {
		src = io.TeeReader(f, &transferProgress{total: stat.Size(), fn: onProgress})
	}
	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("transport: send payload: %w", err)
	}
	return nil
}

type transferProgress struct {
	total   int64
	current int64
	fn      func(int64, int64)
}

func (p *transferProgress) Write(b []byte) (int, error) {
	p.current += int64(len(b))
	p.fn(p.current, p.total)
	return len(b), nil
}
