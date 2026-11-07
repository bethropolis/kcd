package sftp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/sysexec"
)

func (p *SftpPlugin) Unmount(deviceID string) error {
	// Resolved, not read from the map: a pre-restart mount is absent from
	// cache but live, and "not mounted" would strand its sshfs process.
	mountPoint := p.MountedPath(deviceID)
	if mountPoint == "" {
		// Nothing mounted, but a released mount may leave its directory: clear
		// it and report the asked-for state, not an error.
		if leftover := p.mountPointFor(deviceID); p.removeLeftoverDir(deviceID, leftover) {
			p.logger.Info("removed leftover SFTP mount directory",
				log.String("device_id", deviceID),
				log.String("mount_point", leftover),
			)
		}
		return fmt.Errorf("not mounted: no SFTP mount for device %s", deviceID)
	}

	// The kernel is the source of truth: releasing an already-gone mount
	// fails forever, so erroring there would wedge the device as "mounted".
	if !mountExists(mountPoint) {
		p.logger.Info("SFTP mount already released, clearing stale state",
			log.String("device_id", deviceID),
			log.String("mount_point", mountPoint),
		)
		p.finishUnmount(deviceID, mountPoint)
		return nil
	}

	p.mu.RLock()
	pid := p.mountPIDs[deviceID]
	p.mu.RUnlock()

	p.logger.Info("unmounting SFTP share", log.String("mount_point", mountPoint))

	// Graceful shutdown first (SIGTERM, wait, SIGKILL); kernel-adopted mounts
	// carry no tracked PID, so look one up instead of skipping the step.
	hasPID := pid != 0
	if !hasPID {
		if found, err := findSSHFSPID(mountPoint); err == nil {
			pid, hasPID = found, true
		}
	}
	if hasPID {
		p.logger.Debug("sending SIGTERM to sshfs", log.Int("pid", pid))
		proc, err := os.FindProcess(pid)
		if err == nil {
			if err := proc.Signal(syscall.SIGTERM); err == nil {
				done := make(chan struct{})
				go func() {
					proc.Wait()
					close(done)
				}()
				select {
				case <-done:
					p.logger.Debug("sshfs exited cleanly after SIGTERM")
				case <-time.After(3 * time.Second):
					p.logger.Debug("sshfs did not exit after SIGTERM, sending SIGKILL")
					proc.Kill()
				}
			}
		}
	}

	// Ensure the mount point is released (bounded: a wedged FUSE mount
	// must not hang Unmount forever).
	tool := "fusermount3"
	if _, err := exec.LookPath(tool); err != nil {
		tool = "fusermount"
	}
	unmountCtx, unmountCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer unmountCancel()
	out, err := sysexec.Run(unmountCtx, tool, "-u", mountPoint)
	if err == nil {
		p.finishUnmount(deviceID, mountPoint)
		return nil
	}

	detail := strings.TrimSpace(string(out))

	// It may have gone while we were killing the sshfs process.
	if !mountExists(mountPoint) {
		p.logger.Info("SFTP mount released during unmount",
			log.String("mount_point", mountPoint),
			log.String("output", detail),
		)
		p.finishUnmount(deviceID, mountPoint)
		return nil
	}

	// Still present but dead: a normal unmount refuses it ("Transport endpoint
	// is not connected"), so detach lazily — insiders see ENOTCONN, not a hang.
	if strings.Contains(detail, "Transport endpoint is not connected") {
		lazyOut, lazyErr := sysexec.Run(unmountCtx, tool, "-uz", mountPoint)
		if lazyErr == nil {
			p.logger.Info("SFTP mount force-detached with a lazy unmount",
				log.String("mount_point", mountPoint),
			)
			p.finishUnmount(deviceID, mountPoint)
			return nil
		}
		p.logger.Warn("lazy unmount failed",
			log.String("mount_point", mountPoint),
			log.Error(lazyErr),
			log.String("output", strings.TrimSpace(string(lazyOut))),
		)
		detail = detail + " (lazy unmount also failed: " + strings.TrimSpace(string(lazyOut)) + ")"
	}

	// Keep the tracked state: the mount is genuinely still there, and the
	// caller needs a retry to clear it.
	p.logger.Warn("fusermount failed",
		log.String("mount_point", mountPoint),
		log.Error(err),
		log.String("output", detail),
	)
	if detail != "" {
		detail = ": " + detail
	}
	return fmt.Errorf("stale SFTP mount at %s could not be released, %s failed: %v%s",
		mountPoint, tool, err, detail)
}

// finishUnmount clears tracked state and removes the mount directory. Called
// once the mount is confirmed released.
func (p *SftpPlugin) finishUnmount(deviceID, mountPoint string) {
	p.mu.Lock()
	delete(p.mountPoints, deviceID)
	delete(p.mountPIDs, deviceID)
	delete(p.mountReadOnly, deviceID)
	p.mu.Unlock()

	_ = os.Remove(mountPoint)
	p.logger.Info("SFTP unmounted", log.String("mount_point", mountPoint))
	p.publishUnmounted(deviceID, mountPoint)
}

// removeLeftoverDir removes a mount directory left behind by a mount that is
// no longer in the kernel, reporting whether there was one.
func (p *SftpPlugin) removeLeftoverDir(deviceID, mountPoint string) bool {
	if _, err := os.Stat(mountPoint); err != nil {
		return false
	}
	p.mu.Lock()
	delete(p.mountPoints, deviceID)
	delete(p.mountPIDs, deviceID)
	delete(p.mountReadOnly, deviceID)
	p.mu.Unlock()
	// Best effort: a non-empty or busy directory just stays, and the error
	// above still tells the caller nothing is mounted.
	return os.Remove(mountPoint) == nil
}

// findSSHFSPID scans /proc to find the sshfs daemon PID for a given mount point.
// Uses /proc directly to avoid external dependencies (pgrep, etc.).
func findSSHFSPID(mountPoint string) (int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, fmt.Errorf("read /proc: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		// cmdline uses null bytes as separators; convert to string for matching.
		if strings.Contains(string(cmdline), mountPoint) && strings.Contains(string(cmdline), "sshfs") {
			return pid, nil
		}
	}
	return 0, fmt.Errorf("no sshfs process found for mount point %s", mountPoint)
}

// UnmountAll releases every tracked mount, returning on completion or ctx
// expiry — which is not "unmounted"; sound only because shutdown is the caller.
func (p *SftpPlugin) UnmountAll(ctx context.Context) {
	// Shutdown unmounts explicitly (OnDisconnect skips graceful stops) and
	// concurrently: rm -rf would otherwise descend into a live mount.
	p.mu.RLock()
	ids := make([]string, 0, len(p.mountPoints))
	for id := range p.mountPoints {
		ids = append(ids, id)
	}
	p.mu.RUnlock()

	if len(ids) == 0 {
		return
	}
	p.logger.Info("releasing SFTP mounts on shutdown", log.Int("count", len(ids)))

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(deviceID string) {
			defer wg.Done()
			if err := p.Unmount(deviceID); err != nil {
				p.logger.Warn("could not release SFTP mount on shutdown",
					log.String("device_id", deviceID),
					log.Error(err),
				)
			}
		}(id)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		p.logger.Warn("timed out releasing SFTP mounts; some may survive this shutdown",
			log.Error(ctx.Err()),
		)
	}
}
