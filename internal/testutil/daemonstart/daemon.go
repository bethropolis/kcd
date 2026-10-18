// Package daemonstart provides helpers for starting kcd in tests.
package daemonstart

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/daemon"
	"github.com/bethropolis/kcd/pkg/client"
)

// StartTestDaemon runs a kcd daemon with a cancellable context, waiting for
// its IPC socket; returns a cancel func and a pre-connected client.
func StartTestDaemon(t *testing.T, cfg *config.Config) (context.CancelFunc, *client.Client) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		if err := daemon.Run(ctx, cfg); err != nil && ctx.Err() == nil {
			t.Logf("daemon exited with error: %v", err)
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	var d net.Dialer
	for time.Now().Before(deadline) {
		checkCtx, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
		if conn, err := d.DialContext(checkCtx, "unix", cfg.SocketPath); err == nil {
			stop()
			_ = conn.Close()
			break
		}
		stop()
		time.Sleep(50 * time.Millisecond)
	}
	cl := &client.Client{SocketPath: cfg.SocketPath, Timeout: 5 * time.Second}
	t.Cleanup(func() {
		cancel()
		_ = os.Remove(cfg.SocketPath)
	})
	return cancel, cl
}
