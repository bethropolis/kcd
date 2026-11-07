// Package clipboard provides shared desktop clipboard operations.
package clipboard

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/bethropolis/kcd/internal/sysexec"
)

// SetText copies text to the active clipboard using wl-copy or xclip.
func SetText(ctx context.Context, text string, exec *sysexec.Exec) error {
	if exec == nil {
		exec = sysexec.New(nil)
	}
	name := "xclip"
	args := []string{"-selection", "clipboard", "-i"}
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		if _, err := exec.LookPath("wl-copy"); err == nil {
			name, args = "wl-copy", nil
		}
	}
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("clipboard: find %s: %w", name, err)
	}
	tctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := exec.RunInput(tctx, text, name, args...); err != nil {
		return fmt.Errorf("clipboard: set text: %w", err)
	}
	return nil
}
