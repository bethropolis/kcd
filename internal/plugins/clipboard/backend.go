package clipboard

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bethropolis/kcd/internal/log"
)

// probeBackend picks the clipboard backend, preferring the Wayland socket:
// DISPLAY alone would silently copy to the X clipboard apps never see.
func probeBackend() (clipboardBackend, string) {
	rtDir := os.Getenv("XDG_RUNTIME_DIR")

	// Wayland: trust WAYLAND_DISPLAY only if its socket actually exists,
	// otherwise scan the runtime dir for any live wayland-* socket.
	if disp := os.Getenv("WAYLAND_DISPLAY"); disp != "" && rtDir != "" {
		if _, err := os.Stat(filepath.Join(rtDir, disp)); err == nil {
			if _, err := exec.LookPath("wl-paste"); err == nil {
				return backendWayland, disp
			}
		}
	}
	if rtDir != "" {
		if entries, err := os.ReadDir(rtDir); err == nil {
			for _, e := range entries {
				name := e.Name()
				if !e.IsDir() && strings.HasPrefix(name, "wayland-") {
					if _, err := exec.LookPath("wl-paste"); err == nil {
						return backendWayland, name
					}
				}
			}
		}
	}

	// X11 fallback.
	if os.Getenv("DISPLAY") != "" {
		if _, err := exec.LookPath("xclip"); err == nil {
			return backendX11, ""
		}
	}
	return backendUnknown, ""
}

// getBackend returns the cached backend, re-probing while unknown: an early
// failed probe must not stick for the process lifetime. Only known caches.
func (p *ClipboardPlugin) getBackend() (clipboardBackend, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.backend != backendUnknown {
		return p.backend, p.wlDisplay
	}
	backend, disp := p.probe()
	if backend != backendUnknown {
		p.backend = backend
		p.wlDisplay = disp
		p.logger.Debug("clipboard: backend detected",
			log.Int("backend", int(backend)), log.String("wl_display", disp))
	}
	return backend, disp
}

// clipboardCmd builds the tool command with WAYLAND_DISPLAY injected; it
// carries no deadline itself — runClipboard applies the timeout around it.
func (p *ClipboardPlugin) clipboardCmd(name string, args ...string) *exec.Cmd {
	// A background context: the deadline lives in runClipboard, which binds
	// a bounded context around execution so a hung tool is killed there.
	cmd := exec.CommandContext(context.Background(), name, args...)
	cmd.WaitDelay = time.Second
	if _, disp := p.getBackend(); disp != "" {
		cmd.Env = append(os.Environ(), "WAYLAND_DISPLAY="+disp)
	}
	return cmd
}

// runClipboard runs a clipboard subprocess bounded by timeout, wrapping the
// tool's stderr into failures so the real reason surfaces, not "exit status N".
func (p *ClipboardPlugin) runClipboard(ctx context.Context, cmd *exec.Cmd) ([]byte, error) {
	tctx, cancel := context.WithTimeout(ctx, clipboardTimeout)
	defer cancel()

	timed := exec.CommandContext(tctx, cmd.Path, cmd.Args[1:]...)
	timed.Env = cmd.Env
	timed.WaitDelay = capKillDelay(cmd.WaitDelay)
	timed.Stdin = nil

	var stderr bytes.Buffer
	timed.Stderr = &stderr

	out, err := timed.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			err = fmt.Errorf("%w: %s", err, msg)
		}
	}
	return out, err
}

// runCopy writes via wl-copy/xclip -i with fds on the null device: wl-copy
// forks a persistent manager, so pipes would never EOF and Wait would hang.
func (p *ClipboardPlugin) runCopy(ctx context.Context, cmd *exec.Cmd, stdin io.Reader) error {
	tctx, cancel := context.WithTimeout(ctx, clipboardTimeout)
	defer cancel()

	timed := exec.CommandContext(tctx, cmd.Path, cmd.Args[1:]...)
	timed.Env = cmd.Env
	timed.WaitDelay = capKillDelay(cmd.WaitDelay)
	timed.Stdin = stdin
	timed.Stdout = nil
	timed.Stderr = nil
	return timed.Run()
}

// capKillDelay bounds how long Wait may block after killing the child: a
// bare exec defaults to no bound, and a forked grandchild would pin it open.
func capKillDelay(d time.Duration) time.Duration {
	if d <= 0 {
		return time.Second
	}
	return d
}

// isNoSelection reports whether a tool failure means empty clipboard rather
// than a broken tool: it matches the tools' "nothing here" messages.
func isNoSelection(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "no selection") ||
		strings.Contains(s, "nothing is copied") ||
		strings.Contains(s, "no data")
}
