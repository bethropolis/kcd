package plugin

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/log"
)

// RunCommandOutput must return stdout only: stderr must not leak into
// parsed output.
func TestRunCommandOutputDiscardsStderr(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, err := RunCommandOutput(ctx, "sh", "-c", "printf hello; printf noise >&2")
	if err != nil {
		t.Fatalf("RunCommandOutput failed: %v", err)
	}
	if string(out) != "hello" {
		t.Fatalf("output = %q, want %q (stderr leaked)", out, "hello")
	}
}

// RunCommandSync returns combined output with the failure.
func TestRunCommandSyncCombinedOnError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, err := RunCommandSync(ctx, "sh", "-c", "echo oops; exit 3")
	if err == nil {
		t.Fatal("expected non-nil error for exit 3")
	}
	if !strings.Contains(string(out), "oops") {
		t.Fatalf("output = %q, want it to contain %q", out, "oops")
	}
}

// RunCommandAsync must not block the caller.
func TestRunCommandAsyncReturnsImmediately(t *testing.T) {
	start := time.Now()
	RunCommandAsync(log.Nop(), "sleep", "30")
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("RunCommandAsync blocked for %v", elapsed)
	}
}
