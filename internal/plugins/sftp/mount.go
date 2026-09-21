package sftp

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
)

// sshUserPattern allows the generated Android SFTP usernames (alphanumerics,
// underscore, dot, hyphen) while rejecting anything starting with '-' —
// sshfs would parse that as an option flag (e.g. -oProxyCommand=...),
// yielding local command execution.
var sshUserPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

// sshHostPattern allows IPs (validated separately) and plain hostnames
// (.local, LAN names). Anything else — flags, spaces, shell metachars,
// userinfo (@) — is rejected.
var sshHostPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)

// buildSSHFSArgs validates the phone-provided (or IPC-provided) remote
// parameters and constructs the sshfs argv. Validation (not a "--"
// separator — unsupported by older sshfs 2.x) is what prevents option
// injection: no validated value can begin with '-', so sshfs/fuse option
// parsing can never reinterpret remoteRoot as a flag like -oProxyCommand.
func buildSSHFSArgs(body SftpBody, remotePath, mountPoint string, uid, gid int, keepaliveInterval, keepaliveCount int, extraOpts []string) ([]string, error) {
	if !sshUserPattern.MatchString(body.User) || len(body.User) > 64 {
		return nil, fmt.Errorf("sftp: refusing suspicious ssh user %q", body.User)
	}
	if body.IP == "" || strings.HasPrefix(body.IP, "-") {
		return nil, fmt.Errorf("sftp: refusing suspicious ssh host %q", body.IP)
	}
	if net.ParseIP(body.IP) == nil && (!sshHostPattern.MatchString(body.IP) || len(body.IP) > 253) {
		return nil, fmt.Errorf("sftp: refusing invalid ssh host %q", body.IP)
	}
	port, err := strconv.Atoi(strings.TrimSpace(body.Port.String()))
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("sftp: refusing invalid ssh port %q", body.Port.String())
	}
	if remotePath == "" || strings.HasPrefix(remotePath, "-") {
		return nil, fmt.Errorf("sftp: refusing suspicious remote path %q", remotePath)
	}
	remotePath = filepath.Clean(remotePath)
	remoteRoot := fmt.Sprintf("%s@%s:%s", body.User, body.IP, remotePath)

	args := []string{
		remoteRoot,
		mountPoint,
		"-p", strconv.Itoa(port),
		"-s",
		"-F", "/dev/null",
		"-o", "password_stdin",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "reconnect",
		"-o", "ServerAliveInterval=" + strconv.Itoa(keepaliveInterval),
		"-o", "ServerAliveCountMax=" + strconv.Itoa(keepaliveCount),
		"-o", "auto_cache",
		"-o", "kernel_cache",
		"-o", "uid=" + strconv.Itoa(uid),
		"-o", "gid=" + strconv.Itoa(gid),
	}

	// ExtraSshfsOpts comes from the local operator config, not the phone —
	// passed through as-is.
	for _, opt := range extraOpts {
		args = append(args, "-o", opt)
	}
	return args, nil
}

// mountWithBody performs the sshfs mount and returns the local browse path.
// volumePath specifies which storage volume to mount. If empty, the first
// available volume is selected automatically.
func (p *SftpPlugin) mountWithBody(ctx context.Context, deviceID string, body SftpBody, volumePath string) (string, error) {
	baseDir := p.cfg.MountDir
	if baseDir == "" {
		baseDir = os.TempDir()
	}
	mountPoint := filepath.Join(baseDir, "kcd-sftp-"+deviceID)
	if err := os.MkdirAll(mountPoint, 0700); err != nil {
		return "", fmt.Errorf("create mount point %s: %w", mountPoint, err)
	}

	// Determine the remote path on the Android device.
	// The Android SFTP server exposes the real filesystem at "/".
	// Listing "/" via sshfs fails because it contains permission-denied
	// entries (/proc, /sys). Instead, mount directly to a storage
	// volume (e.g. /storage/emulated/0) which is guaranteed browsable.
	// If a specific volumePath is provided, use it; otherwise auto-select
	// the first available volume.
	remotePath := volumePath
	if remotePath == "" {
		if len(body.MultiPaths) > 0 {
			remotePath = body.MultiPaths[0]
		} else if body.Path != "" && body.Path != "/" {
			remotePath = body.Path
		}
	}
	args, err := buildSSHFSArgs(body, remotePath, mountPoint, os.Getuid(), os.Getgid(), p.cfg.KeepaliveIntervalSecs, p.cfg.KeepaliveCount, p.cfg.ExtraSshfsOpts)
	if err != nil {
		_ = os.Remove(mountPoint)
		return "", err
	}

	cmd := exec.CommandContext(ctx, "sshfs", args...)
	cmd.Stdin = strings.NewReader(body.Password + "\n")

	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(mountPoint)
		msg := strings.TrimSpace(string(out))
		errMsg := fmt.Sprintf("sshfs failed: %v\n%s", err, msg)
		if strings.Contains(msg, "Operation not permitted") || strings.Contains(msg, "fusermount") {
			errMsg += "\n\nHint: FUSE requires user_allow_other in /etc/fuse.conf.\nRun: sudo sed -i 's/^#user_allow_other/user_allow_other/' /etc/fuse.conf"
		} else if strings.Contains(msg, "sshfs: not found") || strings.Contains(msg, "executable file not found") {
			errMsg += "\n\nHint: sshfs is not installed.\nInstall: sudo apt install sshfs  (or the equivalent for your distro)"
		}
		return "", fmt.Errorf("%s", errMsg)
	}

	// The mount point now IS the storage volume root, so the user browses
	// directly to the mount point — no extra navigation needed.
	browsePath := mountPoint

	// Track the mount point so Unmount() can call fusermount.
	p.mu.Lock()
	p.mountPoints[deviceID] = mountPoint
	p.mu.Unlock()

	// Find and track the sshfs daemon PID for graceful shutdown.
	if pid, err := findSSHFSPID(mountPoint); err == nil {
		p.mu.Lock()
		p.mountPIDs[deviceID] = pid
		p.mu.Unlock()
		p.logger.Debug("tracking sshfs PID", log.Int("pid", pid))
	} else {
		p.logger.Debug("could not find sshfs PID", log.Error(err))
	}

	p.logger.Info("SFTP mounted",
		log.String("mount_point", mountPoint),
		log.String("browse_path", browsePath),
	)

	// Open in the default file manager (best effort, non-blocking).
	if p.cfg.AutoOpen {
		go func() {
			cmd := p.cfg.OpenCommand
			if cmd == "" {
				cmd = "xdg-open"
			}
			if err := exec.CommandContext(context.Background(), cmd, browsePath).Start(); err != nil {
				p.logger.Debug("auto-open failed", log.String("command", cmd), log.Error(err))
			}
		}()
	}

	return browsePath, nil
}

func (p *SftpPlugin) OnConnect(_ device.Sender) {}

func (p *SftpPlugin) OnDisconnect(dev device.Sender) {
	p.mu.Lock()
	deviceID := dev.ID()
	_, mounted := p.mountPoints[deviceID]
	p.mu.Unlock()

	if mounted {
		p.logger.Info("device disconnected, cleaning up SFTP mount",
			log.String("device_id", deviceID),
		)
		if err := p.Unmount(deviceID); err != nil {
			p.logger.Warn("failed to unmount on disconnect",
				log.String("device_id", deviceID),
				log.Error(err),
			)
		}
	}

	// Evict cached credentials to prevent slow memory leak.
	p.mu.Lock()
	delete(p.lastBody, deviceID)
	p.mu.Unlock()
}

// Unmount cleanly unmounts a previously mounted SFTP filesystem.
// It first attempts a graceful shutdown of the sshfs process (SIGTERM → wait → SIGKILL),
// then uses fusermount to ensure the mount point is released.
// Returns an error if the device was never mounted.
func (p *SftpPlugin) Unmount(deviceID string) error {
	p.mu.Lock()
	mountPoint, ok := p.mountPoints[deviceID]
	if ok {
		delete(p.mountPoints, deviceID)
	}
	pid, hasPID := p.mountPIDs[deviceID]
	if hasPID {
		delete(p.mountPIDs, deviceID)
	}
	p.mu.Unlock()

	if !ok {
		return fmt.Errorf("no active SFTP mount for device %s", deviceID)
	}

	p.logger.Info("unmounting SFTP share", log.String("mount_point", mountPoint))

	// Graceful shutdown: SIGTERM → wait → SIGKILL.
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
	if out, err := plugin.RunCommandSync(unmountCtx, tool, "-u", mountPoint); err != nil {
		p.logger.Warn("fusermount cleanup failed",
			log.String("mount_point", mountPoint),
			log.Error(err),
			log.String("output", strings.TrimSpace(string(out))),
		)
	}

	_ = os.Remove(mountPoint)
	p.logger.Info("SFTP unmounted", log.String("mount_point", mountPoint))
	return nil
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
