package plugin

import (
	"context"
	"os/exec"
	"time"

	"github.com/bethropolis/kcd/internal/log"
)

// RunCommandAsync executes a system command in a goroutine so it doesn't block the plugin handler.
// It logs a warning if the command fails, aiding in debugging missing dependencies (like notify-send, xclip).
func RunCommandAsync(logger log.Logger, name string, args ...string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, name, args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			logger.Warn("subprocess failed",
				log.String("cmd", name),
				log.Error(err),
				log.String("output", string(out)),
			)
		}
	}()
}

// RunCommandSync executes a system command synchronously and returns its combined output and error.
// The caller is responsible for applying timeouts via the context.
func RunCommandSync(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}

// RunCommandOutput executes a system command synchronously and returns its
// standard output only (stderr is discarded). Use it where stdout is parsed:
// combined output would let stderr corrupt the parse. Timeouts via ctx.
func RunCommandOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.Output()
}
