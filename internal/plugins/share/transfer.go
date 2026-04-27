package share

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
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

func ReceiveSideChannel(ctx context.Context, ip net.IP, port int, size int64, dest string, tlsConfig *tls.Config, onProgress func(int64, int64), logger *zap.Logger) error {
	if size < 0 {
		return fmt.Errorf("share: indefinite payload sizes (-1) are not supported")
	}

	addr := fmt.Sprintf("%s:%d", ip.String(), port)

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

// ListenSideChannel binds to an available TCP port in the KDE Connect side-channel range.
func ListenSideChannel(ctx context.Context, tlsConfig *tls.Config) (net.Listener, int, error) {
	lc := net.ListenConfig{KeepAlive: 30 * time.Second}
	var ln net.Listener
	var err error

	for port := 1739; port <= 1764; port++ {
		ln, err = lc.Listen(ctx, "tcp", fmt.Sprintf("0.0.0.0:%d", port))
		if err == nil {
			return ln, port, nil
		}
	}
	return nil, 0, fmt.Errorf("no available ports in range 1739-1764")
}

// AcceptAndSend waits for the phone to connect, performs the TLS handshake, and streams the file.
func AcceptAndSend(ln net.Listener, filePath string, tlsConfig *tls.Config, expectedDeviceID string, onProgress func(int64, int64), logger *zap.Logger) error {
	defer ln.Close()

	addr := ln.Addr().String()

	// Give Android up to 2 minutes to connect (the user might need to tap "Accept" on their phone)
	acceptCtx, cancelAccept := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelAccept()

	go func() {
		<-acceptCtx.Done()
		ln.Close() // Unblocks Accept() if timeout is reached
	}()

	logger.Info("share: waiting for device to connect",
		zap.String("listen_addr", addr),
		zap.String("device_id", expectedDeviceID),
		zap.String("file", filePath),
	)

	conn, err := ln.Accept()
	if err != nil {
		if acceptCtx.Err() != nil {
			logger.Warn("share: timed out waiting for device to connect — is TCP 1739-1764 open in your firewall?",
				zap.String("listen_addr", addr),
				zap.String("device_id", expectedDeviceID),
			)
			return fmt.Errorf("timed out waiting for device to connect on %s (check firewall: ufw allow 1739:1764/tcp)", addr)
		}
		return fmt.Errorf("accept failed: %w", err)
	}
	defer conn.Close()

	logger.Info("share: device connected to side-channel, starting TLS handshake",
		zap.String("remote_addr", conn.RemoteAddr().String()),
	)

	tlsConn := tls.Server(conn, tlsConfig)
	if err := tlsConn.Handshake(); err != nil {
		logger.Error("share: TLS handshake failed on side-channel",
			zap.String("remote_addr", conn.RemoteAddr().String()),
			zap.Error(err),
		)
		return fmt.Errorf("tls handshake failed: %w", err)
	}

	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		logger.Error("share: device presented no client certificate",
			zap.String("remote_addr", conn.RemoteAddr().String()),
			zap.String("expected_device", expectedDeviceID),
		)
		return fmt.Errorf("share: client presented no certificate (expected device %s)", expectedDeviceID)
	}
	// Android KDE Connect stores device IDs with hyphens in the cert CN
	// (e.g. "9a5c23ea-7195-4da1-b766-282b7256a02d") but sends them with
	// underscores in identity packets. Normalise before comparing.
	certCN := state.PeerCertificates[0].Subject.CommonName
	normCN := strings.ReplaceAll(certCN, "-", "_")
	if normCN != expectedDeviceID {
		logger.Error("share: cert CN mismatch — unexpected device connected to side-channel",
			zap.String("expected_device", expectedDeviceID),
			zap.String("cert_cn", certCN),
		)
		return fmt.Errorf("share: cert CN mismatch: expected device %s, got %s", expectedDeviceID, certCN)
	}

	logger.Info("share: TLS OK, streaming file",
		zap.String("file", filePath),
		zap.String("remote_addr", conn.RemoteAddr().String()),
	)

	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	stat, _ := f.Stat()
	size := stat.Size()

	var r io.Reader = f
	if onProgress != nil {
		r = io.TeeReader(f, &progressWriter{total: size, callback: onProgress})
	}

	n, err := io.Copy(tlsConn, r)
	if err != nil {
		return fmt.Errorf("stream error: %w", err)
	}

	logger.Info("share: send complete", zap.String("path", filePath), zap.Int64("bytes", n))
	return nil
}
