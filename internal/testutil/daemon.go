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

// StartTestDaemon starts a kcd daemon in a goroutine with a cancellable
// context and waits up to 3 seconds for its IPC socket to be ready.
// It returns a cancel function (to stop the daemon) and a pre-connected client.
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
