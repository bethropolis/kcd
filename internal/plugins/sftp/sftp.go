package sftp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

type SftpPlugin struct {
	bus      *events.Bus
	logger   *zap.Logger
	mu       sync.RWMutex
	lastBody map[string]SftpBody
}

func NewSftpPlugin(bus *events.Bus, logger *zap.Logger) *SftpPlugin {
	return &SftpPlugin{
		bus:      bus,
		logger:   logger.With(zap.String("plugin", "sftp")),
		lastBody: make(map[string]SftpBody),
	}
}

type SftpBody struct {
	IP       string      `json:"ip"`
	Port     json.Number `json:"port"`
	User     string      `json:"user"`
	Password string      `json:"password"`
	Path     string      `json:"path"`
}

func (p *SftpPlugin) Name() string            { return "SFTP" }
func (p *SftpPlugin) Timeout() time.Duration  { return 5 * time.Second }
func (p *SftpPlugin) IncomingTypes() []string { return []string{"kdeconnect.sftp"} }
func (p *SftpPlugin) OutgoingTypes() []string { return []string{"kdeconnect.sftp.request"} }

func (p *SftpPlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	var body SftpBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	p.mu.Lock()
	p.lastBody[dev.ID()] = body
	p.mu.Unlock()

	sftpURI := fmt.Sprintf("sftp://%s:%s@%s:%s%s", body.User, body.Password, body.IP, body.Port.String(), body.Path)
	safeURI := fmt.Sprintf("sftp://%s@%s:%s%s", body.User, body.IP, body.Port.String(), body.Path)
	p.logger.Info("SFTP server available", zap.String("uri", safeURI))

	if p.bus != nil {
		p.bus.Publish(events.TypeSftpMount, dev.ID(), map[string]string{
			"uri":      sftpURI,
			"ip":       body.IP,
			"port":     body.Port.String(),
			"user":     body.User,
			"password": body.Password,
			"path":     body.Path,
		})
	}

	return nil
}

// RequestMount sends a request to the device to start its SFTP server.
func (p *SftpPlugin) RequestMount(dev device.Sender) error {
	pkt, err := protocol.NewPacket("kdeconnect.sftp.request", map[string]interface{}{
		"startBrowsing": true,
	})
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}

// MountLocally attempts to mount the remote filesystem using sshfs.
func (p *SftpPlugin) MountLocally(ctx context.Context, deviceID string) error {
	p.mu.RLock()
	body, ok := p.lastBody[deviceID]
	p.mu.RUnlock()

	if !ok {
		return fmt.Errorf("no sftp credentials available for device %s; run 'sftp mount-request' first", deviceID)
	}

	mountPoint := filepath.Join(os.TempDir(), "kcd-sftp-"+deviceID)
	if err := os.MkdirAll(mountPoint, 0700); err != nil {
		return fmt.Errorf("failed to create mount point %s: %w", mountPoint, err)
	}

	p.logger.Info("mounting sftp share", zap.String("device_id", deviceID), zap.String("mount_point", mountPoint))

	// Using sshfs with security precautions
	args := []string{
		fmt.Sprintf("%s@%s:%s", body.User, body.IP, body.Path),
		mountPoint,
		"-p", body.Port.String(),
		"-o", "password_stdin",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
	}

	cmd := exec.CommandContext(ctx, "sshfs", args...)
	// KDE Connect Android app doesn't always handle password_stdin perfectly if trailing newline is missing
	cmd.Stdin = strings.NewReader(body.Password + "\n")

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sshfs failed: %w (output: %s)", err, string(out))
	}

	p.logger.Info("sftp mount successful", zap.String("mount_point", mountPoint))
	return nil
}

func (p *SftpPlugin) OnConnect(dev device.Sender)    {}
func (p *SftpPlugin) OnDisconnect(dev device.Sender) {}
