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

	"go.uber.org/zap"
)

// probeBackend determines the usable clipboard backend (wl-paste/xclip) by
// inspecting the environment and $XDG_RUNTIME_DIR. It is side-effect free and
// unit-testable. The returned string is the WAYLAND_DISPLAY value to inject
// into spawned subprocesses (empty for X11/unknown).
//
// A Wayland socket is preferred over DISPLAY: under a systemd user service
// WAYLAND_DISPLAY is often unset at startup, and treating DISPLAY as the
// backend on a Wayland session silently copies to the X clipboard where
// Wayland-native apps never see it.
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

// getBackend returns the cached backend, re-probing while it is unknown so an
// early failed probe (compositor not up yet, env not imported) does not stick
// for the lifetime of the process. Only non-unknown results are cached.
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
			zap.Int("backend", int(backend)), zap.String("wl_display", disp))
	}
	return backend, disp
}

// clipboardCmd builds an exec.Cmd for a clipboard tool with WAYLAND_DISPLAY
// injected into the subprocess environment from the probed socket, so the
// tool works even when the daemon's own environment lacks the variable.
// The command itself carries no deadline; runClipboard applies the timeout
// around execution so a hung wl-paste can never stall the daemon.
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

// runClipboard runs a clipboard subprocess bounded by the clipboard timeout
// and captures any stderr the tool prints. On failure the stderr is wrapped
// into the returned error so the real reason (e.g. "No selection", a compositor
// error) is visible instead of a bare "exit status N".
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

// runCopy writes clipboard data via wl-copy/xclip -i. Unlike runClipboard it
// must NOT capture stdout/stderr through os.Pipe: wl-copy forks a persistent
// background manager that inherits the pipe fds, so the pipe never EOFs and
// cmd.Output()/cmd.Wait() would block until the manager dies. Pointing the
// child's fds at the null device instead makes Run() return as soon as the
// forking wl-copy process exits. wl-paste never forks, which is why the read
// path above can still use pipes.
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

// capKillDelay returns nonzero bound on how long Wait may block on the
// stdout/stderr pipes after the timeout kills the child. Callers built via
// clipboardCmd start with WaitDelay=1s, but a bare exec.CommandContext
// (tests, or any future caller) defaults to 0, which means Wait blocks until
// the pipes EOF — and a killed child that forked a grandchild holding those
// fds would pin runClipboard open until the grandchild exits (e.g. a whole
// 30s sleep). Ensure it always returns promptly after the deadline.
func capKillDelay(d time.Duration) time.Duration {
	if d <= 0 {
		return time.Second
	}
	return d
}

// isNoSelection reports whether a clipboard tool failure actually means the
// clipboard is empty (nothing to push) rather than a real problem with the
// tool or compositor. Matches wl-paste's and xclip's "nothing here" messages.
func isNoSelection(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "no selection") ||
		strings.Contains(s, "nothing is copied") ||
		strings.Contains(s, "no data")
}
