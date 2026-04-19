package plugin

import (
	"context"
	"os/exec"
	"time"

	"go.uber.org/zap"
)

// RunCommandAsync executes a system command in a goroutine so it doesn't block the plugin handler.
// It logs a warning if the command fails, aiding in debugging missing dependencies (like notify-send, xclip).
func RunCommandAsync(logger *zap.Logger, name string, args ...string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, name, args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			logger.Warn("subprocess failed",
				zap.String("cmd", name),
				zap.Error(err),
				zap.String("output", string(out)),
			)
		}
	}()
}
