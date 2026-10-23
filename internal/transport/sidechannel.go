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

// SidechannelOptions bounds establishment with a single overall timeout;
// IdleTimeout bounds streaming silence, letting slow transfers run unbounded.
type SidechannelOptions struct {
	Timeout     time.Duration
	IdleTimeout time.Duration
	// KeepAliveIdle overrides the TCP keepalive first-probe delay for
	// the dial; zero keeps DefaultKeepAliveIdle.
	KeepAliveIdle  time.Duration
	MaxPayloadSize int64
	PortMin        int
	PortMax        int
}

// defaultSetupTimeout bounds side-channel establishment (TCP dial + TLS
// handshake) when the caller passes no explicit timeout.
const defaultSetupTimeout = 15 * time.Second

// DialSidechannel connects as a TLS client, verifying the paired cert. The
// caller owns closing; streaming has no absolute deadline, only idle bound.
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
		opts.Timeout = defaultSetupTimeout
	}
	if opts.KeepAliveIdle <= 0 {
		opts.KeepAliveIdle = DefaultKeepAliveIdle
	}
	addr := net.JoinHostPort(ip.String(), strconv.Itoa(port))
	// One wall-clock budget covers everything before payload streaming:
	// the TCP connect and the TLS handshake together.
	setupCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	dialer := net.Dialer{Timeout: opts.Timeout, KeepAlive: opts.KeepAliveIdle}
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
	// The caller's context governs establishment only: payload I/O streams
	// unbounded after the handshake, and the caller owns closing the conn.
	return WithIdleTimeout(conn, opts.IdleTimeout), nil
}
