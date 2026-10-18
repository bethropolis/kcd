package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"

	"github.com/bethropolis/kcd/internal/log"
)

// ReceiveOptions bounds a side-channel payload and controls its destination.
type ReceiveOptions struct {
	MaxSize     int64
	Perm        os.FileMode
	OnProgress  func(current, total int64)
	Sidechannel SidechannelOptions
}

// ReceiveToFile downloads exactly size bytes, or reads to EOF when size is -1.
// It atomically replaces dest only after a complete, bounded transfer.
func ReceiveToFile(ctx context.Context, ip net.IP, port int, size int64, dest string, tlsConfig *tls.Config, expectedFP string, logger log.Logger, opts ReceiveOptions) (int64, error) {
	if opts.MaxSize <= 0 || opts.MaxSize > 1<<50 || size < -1 || size > opts.MaxSize {
		return 0, fmt.Errorf("transport: invalid payload size %d (limit %d)", size, opts.MaxSize)
	}
	if size >= 0 {
		if err := CheckFreeSpace(filepath.Dir(dest), size); err != nil {
			return 0, err
		}
	}
	conn, err := DialSidechannel(ctx, ip, port, tlsConfig, expectedFP, logger, opts.Sidechannel)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	perm := opts.Perm
	if perm == 0 {
		perm = 0600
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".kcd-transfer-*")
	if err != nil {
		return 0, fmt.Errorf("transport: create temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return 0, fmt.Errorf("transport: set temporary file permissions: %w", err)
	}

	limit := opts.MaxSize
	if size >= 0 {
		limit = size
	}
	reader := io.LimitReader(conn, limit+1)
	var progress io.Writer
	if opts.OnProgress != nil {
		progress = &progressWriter{total: size, callback: opts.OnProgress}
	}
	src := io.Reader(reader)
	if progress != nil {
		src = io.TeeReader(reader, progress)
	}
	n, copyErr := io.Copy(tmp, src)
	if copyErr == nil && n > limit {
		copyErr = fmt.Errorf("payload exceeds limit %d", limit)
	}
	if copyErr == nil && size >= 0 && n != size {
		copyErr = fmt.Errorf("payload truncated (%d/%d bytes)", n, size)
	}
	if copyErr != nil {
		tmp.Close()
		return n, fmt.Errorf("transport: receive payload: %w", copyErr)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return n, fmt.Errorf("transport: sync payload: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return n, fmt.Errorf("transport: close payload: %w", err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return n, fmt.Errorf("transport: replace destination: %w", err)
	}
	return n, nil
}

type progressWriter struct {
	total, current int64
	callback       func(int64, int64)
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.current += int64(len(p))
	w.callback(w.current, w.total)
	return len(p), nil
}
