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

	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
)

// sshUserPattern allows generated Android SFTP usernames while rejecting
// anything starting with '-': sshfs would parse that as an option flag,
// yielding local command execution.
var sshUserPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

// sshHostPattern allows IPs (validated separately) and plain hostnames;
// flags, spaces, metachars, and userinfo are rejected.
var sshHostPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)

// buildSSHFSArgs validates remote parameters into sshfs argv. No validated
// value begins with '-', which is what keeps option parsing from reading
// remote input as flags on sshfs versions without "--" support.
func buildSSHFSArgs(body SftpBody, remotePath, mountPoint string, uid, gid int, keepaliveInterval, keepaliveCount int, extraOpts []string, readOnly bool) ([]string, error) {
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

	// Read-only is set before ExtraSshfsOpts so an explicit operator override
	// in the config can still force a writable mount.
	if readOnly {
		args = append(args, "-o", "ro")
	}

	// ExtraSshfsOpts comes from the local operator config, not the phone —
	// passed through as-is.
	for _, opt := range extraOpts {
		args = append(args, "-o", opt)
	}
	return args, nil
}

// sshfsHint maps a failure message to an actionable hint, matching specific
// causes: a duplicate mount names fusermount3 without it being a config issue.
func sshfsHint(msg string) string {
	switch {
	case strings.Contains(msg, "Operation not permitted"),
		strings.Contains(msg, "user_allow_other"),
		strings.Contains(msg, "Permission denied"):
		return "Hint: FUSE requires user_allow_other in /etc/fuse.conf.\nRun: sudo sed -i 's/^#user_allow_other/user_allow_other/' /etc/fuse.conf"
	case strings.Contains(msg, "sshfs: not found"),
		strings.Contains(msg, "executable file not found"):
		return "Hint: sshfs is not installed.\nInstall: sudo apt install sshfs  (or the equivalent for your distro)"
	default:
		return ""
	}
}

// mountWithBody performs the sshfs mount, defaulting to the first volume;
// returns the local browse path.
func (p *SftpPlugin) mountWithBody(ctx context.Context, deviceID string, body SftpBody, volumePath string, readOnly bool) (string, error) {
	mountPoint := p.mountPointFor(deviceID)
	if mountPoint == "" {
		return "", fmt.Errorf("sftp: device ID produces an unsafe mount path")
	}
	// Warn before the reuse check too: an existing mount at a hazardous spot
	// still needs telling, once per location.
	p.warnIfInBulkDeletableDir(mountPoint)

	// Idempotent: re-running sshfs onto a live mountpoint fails misleadingly,
	// so return the existing mount point (also a cheap file-manager reopen).
	if existing := p.MountedPath(deviceID); existing != "" {
		p.logger.Info("SFTP already mounted, reusing mount point",
			log.String("device_id", deviceID),
			log.String("mount_point", existing),
		)
		// Mounting is idempotent, so a mode flag can't apply to an existing
		// mount: report what it actually is instead of pretending.
		if existingRO := p.mountIsReadOnly(deviceID); existingRO != readOnly {
			p.logger.Info("read-only mode differs from the existing mount and cannot be changed in place; unmount first",
				log.String("device_id", deviceID),
				log.Bool("existing_read_only", existingRO),
				log.Bool("requested_read_only", readOnly),
			)
		}
		p.autoOpen(existing)
		return existing, nil
	}

	if err := os.MkdirAll(mountPoint, 0700); err != nil {
		return "", fmt.Errorf("create mount point %s: %w", mountPoint, err)
	}

	// Never mount "/" itself: the real filesystem's permission-denied entries
	// fail listing; mount a storage volume, guaranteed browsable, instead.
	remotePath := volumePath
	if remotePath == "" {
		if len(body.MultiPaths) > 0 {
			remotePath = body.MultiPaths[0]
		} else if body.Path != "" && body.Path != "/" {
			remotePath = body.Path
		}
	}
	args, err := buildSSHFSArgs(body, remotePath, mountPoint, os.Getuid(), os.Getgid(), p.cfg.KeepaliveIntervalSecs, p.cfg.KeepaliveCount, p.cfg.ExtraSshfsOpts, readOnly)
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
		if hint := sshfsHint(msg); hint != "" {
			errMsg += "\n\n" + hint
		}
		return "", fmt.Errorf("%s", errMsg)
	}

	// The mount point now IS the storage volume root, so the user browses
	// directly to the mount point — no extra navigation needed.
	browsePath := mountPoint

	// Track the mount point so Unmount() can call fusermount, and its mode so a
	// later --ro request against this mount can report the truth.
	p.mu.Lock()
	p.mountPoints[deviceID] = mountPoint
	p.mountReadOnly[deviceID] = readOnly
	p.mu.Unlock()

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
	p.publishMounted(deviceID, mountPoint, volumePath)

	p.autoOpen(browsePath)

	return browsePath, nil
}

// publishMounted announces a completed mount so clients can render a truthful
// mount toggle without inspecting /proc themselves.
func (p *SftpPlugin) publishMounted(deviceID, mountPoint, volumePath string) {
	if p.bus == nil {
		return
	}
	payload := map[string]any{"mountPoint": mountPoint}
	if volumePath != "" {
		payload["volume"] = volumePath
	}
	p.bus.Publish(events.TypeSftpMounted, deviceID, payload)
}

func (p *SftpPlugin) publishUnmounted(deviceID, mountPoint string) {
	if p.bus == nil {
		return
	}
	p.bus.Publish(events.TypeSftpUnmounted, deviceID, map[string]any{"mountPoint": mountPoint})
}

// autoOpen opens a browse path in the file manager, best effort, in a
// goroutine: a hung manager must never block the mount path.
func (p *SftpPlugin) autoOpen(browsePath string) {
	if !p.cfg.AutoOpen {
		return
	}
	cmd := p.cfg.OpenCommand
	if cmd == "" {
		cmd = "xdg-open"
	}
	go func() {
		if err := exec.CommandContext(context.Background(), cmd, browsePath).Start(); err != nil {
			p.logger.Debug("auto-open failed", log.String("command", cmd), log.Error(err))
		}
	}()
}

func (p *SftpPlugin) mountIsReadOnly(deviceID string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.mountReadOnly[deviceID]
}

// Unmount releases a mounted filesystem. Tracked state drops only on actual
// release (failures stay retryable); errors separate never-mounted from stuck.
