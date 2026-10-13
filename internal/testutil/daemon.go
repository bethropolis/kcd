// Package testutil provides helpers for starting the kcd daemon in tests.
package testutil

import (
	"net"
	"os"
	"testing"
	"time"

	"context"
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

	// Poll until the IPC socket is ready (up to 3 seconds).
	deadline := time.Now().Add(3 * time.Second)
	var d net.Dialer
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		if conn, err := d.DialContext(ctx, "unix", cfg.SocketPath); err == nil {
			cancel()
			conn.Close()
			break
		}
		cancel()
		time.Sleep(50 * time.Millisecond)
	}

	cl := &client.Client{
		SocketPath: cfg.SocketPath,
		Timeout:    5 * time.Second,
	}

	t.Cleanup(func() {
		cancel()
		os.Remove(cfg.SocketPath)
	})

	return cancel, cl
}
