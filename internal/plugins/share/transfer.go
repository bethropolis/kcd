package share

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"runtime/debug"
	"time"

	"go.uber.org/zap"
)

type progressWriter struct {
	total    int64
	current  int64
	callback func(int64, int64)
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n := len(p)
	pw.current += int64(n)
	if pw.callback != nil {
		pw.callback(pw.current, pw.total)
	}
	return n, nil
}

// ReceiveSideChannel connects to the peer's TLS port and streams the payload to a local file.
// It ensures no memory is buffered by using io.Copy with an io.LimitReader.
// KDE Connect requires TLS for file transfers using the same certificates as the main connection.
func ReceiveSideChannel(ctx context.Context, ip net.IP, port int, size int64, dest string, tlsConfig *tls.Config, onProgress func(int64, int64), logger *zap.Logger) error {
	if size < 0 {
		return fmt.Errorf("share: indefinite payload sizes (-1) are not supported")
	}

	addr := fmt.Sprintf("%s:%d", ip.String(), port)

	// Use TLS dialer - KDE Connect uses encrypted side-channels
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		},
		Config: tlsConfig,
	}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("share: dial side-channel %s: %w", addr, err)
	}
	defer conn.Close()

	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("share: create file %s: %w", dest, err)
	}
	defer f.Close()

	// io.LimitReader prevents reading past the announced size per absolute rule.
	limitReader := io.LimitReader(conn, size)

	var r io.Reader = limitReader
	if onProgress != nil {
		r = io.TeeReader(limitReader, &progressWriter{total: size, callback: onProgress})
	}

	n, err := io.Copy(f, r)
	if err != nil {
		return fmt.Errorf("share: stream transfer to %s: %w", dest, err)
	}

	if n < size {
		return fmt.Errorf("share: transfer truncated (%d/%d bytes)", n, size)
	}

	logger.Info("share: transfer complete", zap.String("path", dest), zap.Int64("bytes", n))
	return nil
}

// SendSideChannel starts a TLS listener on a random port and streams a file to the first connecting peer.
// KDE Connect requires TLS for file transfers using the same certificates as the main connection.
func SendSideChannel(ctx context.Context, filePath string, tlsConfig *tls.Config, expectedDeviceID string, onProgress func(int64, int64), logger *zap.Logger) (int, error) {
	var ln net.Listener
	const (
		minPort = 1739
		maxPort = 1764
	)
	for port := minPort; port <= maxPort; port++ {
		var e error
		ln, e = tls.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port), tlsConfig)
		if e == nil {
			break
		}
	}
	if ln == nil {
		return 0, fmt.Errorf("share: no available ports in range %d-%d", minPort, maxPort)
	}

	port := ln.Addr().(*net.TCPAddr).Port

	// Process the transfer in a goroutine so we can return the port immediately
	// for the share request packet.
	go func() {
		defer debug.FreeOSMemory() // Free memory after large file transfer per Phase 3 rules
		defer ln.Close()

		// Wait for the single incoming connection with context support
		type acceptResult struct {
			conn net.Conn
			err  error
		}
		resChan := make(chan acceptResult, 1)

		go func() {
			c, e := ln.Accept()
			resChan <- acceptResult{c, e}
		}()

		var conn net.Conn
		select {
		case <-ctx.Done():
			return
		case res := <-resChan:
			if res.err != nil {
				logger.Error("share: side-channel accept failed", zap.Error(res.err))
				return
			}
			conn = res.conn
		}
		defer conn.Close()

		// Security: verify peer certificate common name matches expected device ID
		tlsConn, ok := conn.(*tls.Conn)
		if ok {
			if err := tlsConn.HandshakeContext(ctx); err != nil {
				logger.Warn("share: side-channel tls handshake failed", zap.Error(err))
				return
			}
			state := tlsConn.ConnectionState()
			if len(state.PeerCertificates) == 0 || state.PeerCertificates[0].Subject.CommonName != expectedDeviceID {
				logger.Warn("share: side-channel rejected unauthorized client",
					zap.String("expected_device_id", expectedDeviceID))
				return
			}
		} else {
			logger.Warn("share: side-channel connection is not TLS")
			return
		}

		f, err := os.Open(filePath)
		if err != nil {
			logger.Error("share: failed to open source file", zap.String("path", filePath), zap.Error(err))
			return
		}
		defer f.Close()

		stat, _ := f.Stat()
		size := stat.Size()

		var r io.Reader = f
		if onProgress != nil {
			r = io.TeeReader(f, &progressWriter{total: size, callback: onProgress})
		}

		// Stream directly to connection per absolute rule.
		n, err := io.Copy(conn, r)
		if err != nil {
			logger.Error("share: transfer error", zap.Error(err))
			return
		}

		logger.Info("share: send complete", zap.String("path", filePath), zap.Int64("bytes", n), zap.Int64("total", size))
	}()

	return port, nil
}
