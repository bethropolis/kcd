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
}

func NewSftpPlugin(cfg config.SFTPConfig, bus *events.Bus, logger *zap.Logger) *SftpPlugin {
	return &SftpPlugin{
		cfg:         cfg,
		bus:         bus,
		logger:      logger.With(zap.String("plugin", "sftp")),
		lastBody:    make(map[string]SftpBody),
		mountPoints: make(map[string]string),
	}
}

// SftpBody matches the body of a kdeconnect.sftp packet sent by the Android app.
type SftpBody struct {
	IP   string      `json:"ip"`
	Port json.Number `json:"port"`
	User string      `json:"user"`
	// Password is intentionally not logged.
	Password string `json:"password"`
	// Path is the directory on the Android device the user should be taken to.
	// It is NOT the remote SFTP path — the Android SFTP server is typically
	// chrooted to its storage root, so this path is used as a navigation target
	// WITHIN the mounted filesystem, not as the sshfs remote path.
	Path string `json:"path"`
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

	p.mu.Lock()
	p.lastBody[dev.ID()] = body
	p.mu.Unlock()

	safeURI := fmt.Sprintf("sftp://%s@%s:%s%s", body.User, body.IP, body.Port.String(), body.Path)
	p.logger.Info("SFTP server available", zap.String("uri", safeURI))

	if p.bus != nil {
		p.bus.Publish(events.TypeSftpMount, dev.ID(), map[string]string{
			"uri":      fmt.Sprintf("sftp://%s:%s@%s:%s%s", body.User, body.Password, body.IP, body.Port.String(), body.Path),
			"ip":       body.IP,
			"port":     body.Port.String(),
			"user":     body.User,
			"password": body.Password,
			"path":     body.Path,
		})
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

	// Mount the SFTP server ROOT — empty path after ':'.
	//
	// Why not use body.Path as the remote path?
	// The KDE Connect Android app chroots its built-in SFTP server to the
	// device's storage root (e.g. /storage/emulated/0).  Passing body.Path
	// (e.g. "/storage/emulated/0") as the remote sshfs path would navigate to
	// that path INSIDE the chroot — effectively /storage/emulated/0/storage/…
	// — which doesn't exist, producing the "you do not have permission to
	// view /" error in GNOME Files / Nautilus.
	//
	// Mounting at root gives us the chroot's top level.  We then navigate the
	// user to body.Path within the local mount.
	remoteRoot := fmt.Sprintf("%s@%s:", body.User, body.IP)

	args := []string{
		remoteRoot,
		mountPoint,
		"-p", body.Port.String(),
		"-o", "password_stdin",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "reconnect",
		"-o", "ServerAliveInterval=" + strconv.Itoa(p.cfg.KeepaliveIntervalSecs),
		"-o", "ServerAliveCountMax=" + strconv.Itoa(p.cfg.KeepaliveCount),
		"-o", "auto_cache",
		"-o", "kernel_cache",
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
		return "", fmt.Errorf("sshfs failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}

	// Navigate the user to body.Path within the mount.
	browsePath := mountPoint
	if body.Path != "" && body.Path != "/" {
		browsePath = filepath.Join(mountPoint, body.Path)
	}

	// Track the mount point so Unmount() can call fusermount.
	p.mu.Lock()
	p.mountPoints[deviceID] = mountPoint
	p.mu.Unlock()

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
			if err := exec.Command(cmd, browsePath).Start(); err != nil {
				p.logger.Debug("auto-open failed", zap.String("command", cmd), zap.Error(err))
			}
		}()
	}

	return browsePath, nil
}

func (p *SftpPlugin) OnConnect(_ device.Sender)    {}
func (p *SftpPlugin) OnDisconnect(_ device.Sender) {}

// Unmount cleanly unmounts a previously mounted SFTP filesystem using fusermount.
// It removes the mount point directory after a successful unmount.
// Returns an error if the device was never mounted or if fusermount fails.
func (p *SftpPlugin) Unmount(deviceID string) error {
	p.mu.Lock()
	mountPoint, ok := p.mountPoints[deviceID]
	if ok {
		delete(p.mountPoints, deviceID)
	}
	p.mu.Unlock()

	if !ok {
		return fmt.Errorf("no active SFTP mount for device %s", deviceID)
	}

	p.logger.Info("unmounting SFTP share", zap.String("mount_point", mountPoint))

	// fusermount3 is the modern variant (Debian/Ubuntu); fall back to fusermount.
	tool := "fusermount3"
	if _, err := exec.LookPath(tool); err != nil {
		tool = "fusermount"
	}

	if out, err := exec.Command(tool, "-u", mountPoint).CombinedOutput(); err != nil {
		// Put the mount point back so the caller can retry.
		p.mu.Lock()
		p.mountPoints[deviceID] = mountPoint
		p.mu.Unlock()
		return fmt.Errorf("fusermount: %w\n%s", err, strings.TrimSpace(string(out)))
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
