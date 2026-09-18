package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/log"
)

// SidechannelOptions bounds the connection establishment phase — TCP dial
// plus TLS handshake plus pin verification — with a single overall timeout
// (default 15s). IdleTimeout optionally bounds streaming silence: every
// read or write pushes the deadline forward, so stalled transfers fail
// while slow-but-alive ones run unbounded in total. Zero disables it.
type SidechannelOptions struct {
	Timeout     time.Duration
	IdleTimeout time.Duration
}

// DialSidechannel connects as a TLS client and verifies the paired certificate
// before returning any payload bytes. The caller owns and must close the result.
// Once established, payload streaming carries no absolute deadline — only the
// optional idle bound from SidechannelOptions applies.
func DialSidechannel(ctx context.Context, ip net.IP, port int, tlsConfig *tls.Config, expectedFP string, logger log.Logger, options ...SidechannelOptions) (net.Conn, error) {
	if ip == nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("side-channel: invalid peer address")
	}
	if tlsConfig == nil {
		return nil, fmt.Errorf("side-channel: TLS configuration is required")
	}
	opts := SidechannelOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 15 * time.Second
	}
	addr := net.JoinHostPort(ip.String(), strconv.Itoa(port))
	// One wall-clock budget covers everything before payload streaming:
	// the TCP connect and the TLS handshake together.
	setupCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	dialer := net.Dialer{Timeout: opts.Timeout, KeepAlive: 30 * time.Second}
	raw, err := dialer.DialContext(setupCtx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("side-channel: dial %s: %w", addr, err)
	}
	conn := tls.Client(raw, tlsConfig)
	if err := conn.HandshakeContext(setupCtx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("side-channel: handshake %s: %w", addr, err)
	}
	if expectedFP == "" {
		logger.Warn("side-channel: no pinned peer fingerprint, skipping verification", log.String("remote_addr", addr))
	} else if err := cert.VerifySideChannelPeer(conn.ConnectionState(), expectedFP); err != nil {
		conn.Close()
		return nil, fmt.Errorf("side-channel: peer verification failed: %w", err)
	}
	// The caller's context governs connection establishment only. Once the
	// handshake is done, cancellation has no effect on payload I/O — the
	// pre-helper plugins dialed with the request context but streamed
	// unbounded, and download goroutines may legitimately outlive the
	// packet-handler context. Callers own the returned conn and close it
	// when streaming finishes.
	return WithIdleTimeout(conn, opts.IdleTimeout), nil
}
