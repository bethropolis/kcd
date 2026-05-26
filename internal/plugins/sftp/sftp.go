package sftp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

// SftpPlugin handles KDE Connect SFTP negotiation and optional sshfs mounting.
type SftpPlugin struct {
	cfg         config.SFTPConfig
	bus         *events.Bus
	logger      *zap.Logger
	mu          sync.RWMutex
	lastBody    map[string]SftpBody
	mountPoints map[string]string // deviceID -> local mountPoint path
	mountPIDs   map[string]int    // deviceID -> sshfs PID for graceful shutdown
}

func NewSftpPlugin(cfg config.SFTPConfig, bus *events.Bus, logger *zap.Logger) *SftpPlugin {
	return &SftpPlugin{
		cfg:         cfg,
		bus:         bus,
		logger:      logger.With(zap.String("plugin", "sftp")),
		lastBody:    make(map[string]SftpBody),
		mountPoints: make(map[string]string),
		mountPIDs:   make(map[string]int),
	}
}

// SftpBody matches the body of a kdeconnect.sftp packet sent by the Android app.
type SftpBody struct {
	IP   string      `json:"ip"`
	Port json.Number `json:"port"`
	User string      `json:"user"`
	// Password is intentionally not logged.
	Password string `json:"password"`
	// Path is the primary storage root path from the Android device.
	// When exactly one volume exists this is the volume path (e.g. /storage/emulated/0);
	// when multiple volumes exist this falls back to "/" (legacy compat).
	// Prefer MultiPaths for the authoritative list of browsable roots.
	Path string `json:"path"`
	// MultiPaths lists all available storage root paths on the device
	// (e.g. internal storage, SD card). Populated by Android API 30+.
	MultiPaths []string `json:"multiPaths,omitempty"`
	// PathNames provides human-readable labels for each path in MultiPaths.
	PathNames []string `json:"pathNames,omitempty"`
	// ErrorMessage is set when the device cannot start the SFTP server
	// (e.g. missing storage permissions).
	ErrorMessage string `json:"errorMessage,omitempty"`
}

// StorageVolume describes a single browsable storage root on the device.
type StorageVolume struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// SftpInfo holds the complete cached SFTP connection details for a device.
type SftpInfo struct {
	IP       string          `json:"ip"`
	Port     json.Number     `json:"port"`
	User     string          `json:"user"`
	Password string          `json:"password"`
	Path     string          `json:"path"`
	Volumes  []StorageVolume `json:"volumes,omitempty"`
}

func (p *SftpPlugin) Name() string            { return "SFTP" }
func (p *SftpPlugin) Timeout() time.Duration  { return 5 * time.Second }
func (p *SftpPlugin) IncomingTypes() []string { return []string{"kdeconnect.sftp"} }
func (p *SftpPlugin) OutgoingTypes() []string { return []string{"kdeconnect.sftp.request"} }

func (p *SftpPlugin) Handle(_ context.Context, dev device.Sender, pkt *protocol.Packet) error {
	var body SftpBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	if body.ErrorMessage != "" {
		p.logger.Warn("SFTP server error from device",
			zap.String("device_id", dev.ID()),
			zap.String("error", body.ErrorMessage),
		)
		if p.bus != nil {
			p.bus.Publish(events.TypeSftpMount, dev.ID(), map[string]interface{}{
				"error": body.ErrorMessage,
			})
		}
		return nil
	}

	p.mu.Lock()
	p.lastBody[dev.ID()] = body
	p.mu.Unlock()

	safeURI := fmt.Sprintf("sftp://%s@%s:%s%s", body.User, body.IP, body.Port.String(), body.Path)
	p.logger.Info("SFTP server available", zap.String("uri", safeURI))

	evtPayload := map[string]interface{}{
		"uri":      fmt.Sprintf("sftp://%s:%s@%s:%s%s", body.User, body.Password, body.IP, body.Port.String(), body.Path),
		"ip":       body.IP,
		"port":     body.Port.String(),
		"user":     body.User,
		"password": body.Password,
		"path":     body.Path,
	}
	if len(body.MultiPaths) > 0 {
		volumes := make([]map[string]string, 0, len(body.MultiPaths))
		for i, mp := range body.MultiPaths {
			name := mp
			if i < len(body.PathNames) {
				name = body.PathNames[i]
			}
			volumes = append(volumes, map[string]string{"name": name, "path": mp})
		}
		evtPayload["volumes"] = volumes
	}

	if p.bus != nil {
		p.bus.Publish(events.TypeSftpMount, dev.ID(), evtPayload)
	}

	return nil
}

// RequestMount sends a kdeconnect.sftp.request packet asking the device to
// start its SFTP server and return credentials.
func (p *SftpPlugin) RequestMount(dev device.Sender) error {
	pkt, err := protocol.NewPacket("kdeconnect.sftp.request", map[string]any{
		"startBrowsing": true,
	})
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}

// RequestAndMount sends the SFTP request, waits for the Android device to
// respond with credentials (up to 20 s), mounts the filesystem via sshfs,
// and returns the local path the user should open.
func (p *SftpPlugin) RequestAndMount(ctx context.Context, dev device.Sender) (string, error) {
	if p.bus == nil {
		return "", fmt.Errorf("event bus not available")
	}

	// Subscribe BEFORE sending the request to guarantee we don't miss the response.
	sub := p.bus.Subscribe(0, events.TypeSftpMount)
	defer sub.Close()

	if err := p.RequestMount(dev); err != nil {
		return "", fmt.Errorf("send SFTP request: %w", err)
	}

	p.logger.Info("SFTP request sent, waiting for phone response", zap.String("device", dev.ID()))

	timeout := time.Duration(p.cfg.CredentialsTimeoutSecs) * time.Second
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		select {
		case evt, ok := <-sub.C:
			if !ok {
				return "", fmt.Errorf("event bus closed")
			}
			if evt.DeviceID != dev.ID() {
				continue
			}
			p.mu.RLock()
			body, exists := p.lastBody[dev.ID()]
			p.mu.RUnlock()
			if !exists {
				return "", fmt.Errorf("credentials missing after event (internal error)")
			}
			return p.mountWithBody(ctx, dev.ID(), body)

		case <-deadline.Done():
			return "", fmt.Errorf("timed out after %s waiting for SFTP response — is the KDE Connect app open on the phone?", timeout)
		}
	}
}

// MountLocally mounts using previously cached credentials.
// Prefer RequestAndMount for a one-step experience.
func (p *SftpPlugin) MountLocally(ctx context.Context, deviceID string) (string, error) {
	p.mu.RLock()
	body, ok := p.lastBody[deviceID]
	p.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("no SFTP credentials cached for device %s — use 'kcd sftp mount' which requests them automatically", deviceID)
	}
	return p.mountWithBody(ctx, deviceID, body)
}

// mountWithBody performs the sshfs mount and returns the local browse path.
func (p *SftpPlugin) mountWithBody(ctx context.Context, deviceID string, body SftpBody) (string, error) {
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
	// entries (/proc, /sys). Instead, mount directly to the first storage
	// volume (e.g. /storage/emulated/0) which is guaranteed browsable.
	remotePath := ""
	if len(body.MultiPaths) > 0 {
		remotePath = body.MultiPaths[0]
	} else if body.Path != "" && body.Path != "/" {
		remotePath = body.Path
	}
	remoteRoot := fmt.Sprintf("%s@%s:%s", body.User, body.IP, remotePath)

	args := []string{
		remoteRoot,
		mountPoint,
		"-p", body.Port.String(),
		"-s",
		"-F", "/dev/null",
		"-o", "password_stdin",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "reconnect",
		"-o", "ServerAliveInterval=" + strconv.Itoa(p.cfg.KeepaliveIntervalSecs),
		"-o", "ServerAliveCountMax=" + strconv.Itoa(p.cfg.KeepaliveCount),
		"-o", "auto_cache",
		"-o", "kernel_cache",
		"-o", "uid=" + strconv.Itoa(os.Getuid()),
		"-o", "gid=" + strconv.Itoa(os.Getgid()),
	}

	if len(p.cfg.ExtraSshfsOpts) > 0 {
		for _, opt := range p.cfg.ExtraSshfsOpts {
			args = append(args, "-o", opt)
		}
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
		p.logger.Debug("tracking sshfs PID", zap.Int("pid", pid))
	} else {
		p.logger.Debug("could not find sshfs PID", zap.Error(err))
	}

	p.logger.Info("SFTP mounted",
		zap.String("mount_point", mountPoint),
		zap.String("browse_path", browsePath),
	)

	// Open in the default file manager (best effort, non-blocking).
	if p.cfg.AutoOpen {
		go func() {
			cmd := p.cfg.OpenCommand
			if cmd == "" {
				cmd = "xdg-open"
			}
			if err := exec.CommandContext(context.Background(), cmd, browsePath).Start(); err != nil {
				p.logger.Debug("auto-open failed", zap.String("command", cmd), zap.Error(err))
			}
		}()
	}

	return browsePath, nil
}

// Info returns the cached SFTP connection details for a device.
// Returns nil if no credentials have been received yet.
func (p *SftpPlugin) Info(deviceID string) *SftpInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()
	body, ok := p.lastBody[deviceID]
	if !ok {
		return nil
	}
	info := &SftpInfo{
		IP:       body.IP,
		Port:     body.Port,
		User:     body.User,
		Password: body.Password,
		Path:     body.Path,
	}
	for i, mp := range body.MultiPaths {
		name := mp
		if i < len(body.PathNames) {
			name = body.PathNames[i]
		}
		info.Volumes = append(info.Volumes, StorageVolume{Name: name, Path: mp})
	}
	return info
}

// Volumes returns the list of available storage volumes from cached credentials.
// Returns nil if no credentials or no multiPaths data.
func (p *SftpPlugin) Volumes(deviceID string) []StorageVolume {
	p.mu.RLock()
	defer p.mu.RUnlock()
	body, ok := p.lastBody[deviceID]
	if !ok || len(body.MultiPaths) == 0 {
		return nil
	}
	volumes := make([]StorageVolume, 0, len(body.MultiPaths))
	for i, mp := range body.MultiPaths {
		name := mp
		if i < len(body.PathNames) {
			name = body.PathNames[i]
		}
		volumes = append(volumes, StorageVolume{Name: name, Path: mp})
	}
	return volumes
}

func (p *SftpPlugin) OnConnect(_ device.Sender) {}

func (p *SftpPlugin) OnDisconnect(dev device.Sender) {
	p.mu.Lock()
	deviceID := dev.ID()
	_, mounted := p.mountPoints[deviceID]
	p.mu.Unlock()

	if mounted {
		p.logger.Info("device disconnected, cleaning up SFTP mount",
			zap.String("device_id", deviceID),
		)
		if err := p.Unmount(deviceID); err != nil {
			p.logger.Warn("failed to unmount on disconnect",
				zap.String("device_id", deviceID),
				zap.Error(err),
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

	p.logger.Info("unmounting SFTP share", zap.String("mount_point", mountPoint))

	// Graceful shutdown: SIGTERM → wait → SIGKILL.
	if hasPID {
		p.logger.Debug("sending SIGTERM to sshfs", zap.Int("pid", pid))
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

	// Ensure the mount point is released.
	tool := "fusermount3"
	if _, err := exec.LookPath(tool); err != nil {
		tool = "fusermount"
	}

	if out, err := exec.CommandContext(context.Background(), tool, "-u", mountPoint).CombinedOutput(); err != nil {
		p.logger.Warn("fusermount cleanup failed",
			zap.String("mount_point", mountPoint),
			zap.Error(err),
			zap.String("output", strings.TrimSpace(string(out))),
		)
	}

	_ = os.Remove(mountPoint)
	p.logger.Info("SFTP unmounted", zap.String("mount_point", mountPoint))
	return nil
}

// MountedPath returns the local mount point for a device, or "" if not mounted.
func (p *SftpPlugin) MountedPath(deviceID string) string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.mountPoints[deviceID]
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
