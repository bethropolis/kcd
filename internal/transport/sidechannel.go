package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"go.uber.org/zap"
)

// SidechannelOptions bounds the entire connection establishment phase — TCP
// dial plus TLS handshake plus pin verification — with a single overall
// timeout (default 15s). Payload streaming is never time-bounded.
type SidechannelOptions struct {
	Timeout time.Duration
}

// DialSidechannel connects as a TLS client and verifies the paired certificate
// before returning any payload bytes. The caller owns and must close the result.
// Once established, payload streaming carries no deadline, so large transfers
// are never truncated mid-stream by the helper.
func DialSidechannel(ctx context.Context, ip net.IP, port int, tlsConfig *tls.Config, expectedFP string, logger *zap.Logger, options ...SidechannelOptions) (net.Conn, error) {
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
		if logger != nil {
			logger.Warn("side-channel: no pinned peer fingerprint, skipping verification", zap.String("remote_addr", addr))
		}
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
	return conn, nil
}
