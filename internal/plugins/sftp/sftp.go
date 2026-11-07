package sftp

import (
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
)

var _ plugin.Plugin = (*SftpPlugin)(nil)

func NewSftpPlugin(cfg config.SFTPConfig, bus *events.Bus, logger log.Logger) *SftpPlugin {
	return &SftpPlugin{
		cfg:           cfg,
		bus:           bus,
		logger:        logger.With(log.String("plugin", "sftp")),
		lastBody:      make(map[string]SftpBody),
		pending:       make(map[string]chan sftpResult),
		mountPoints:   make(map[string]string),
		mountReadOnly: make(map[string]bool),
		mountPIDs:     make(map[string]int),
	}
}

func (p *SftpPlugin) Name() string            { return "SFTP" }
func (p *SftpPlugin) Timeout() time.Duration  { return 5 * time.Second }
func (p *SftpPlugin) IncomingTypes() []string { return []string{"kdeconnect.sftp"} }
func (p *SftpPlugin) OutgoingTypes() []string { return []string{"kdeconnect.sftp.request"} }

func (p *SftpPlugin) OnConnect(_ device.Peer) {}

func (p *SftpPlugin) OnDisconnect(dev device.Peer) {
	deviceID := dev.ID()
	// Via MountedPath, so a mount adopted from the kernel is cleaned up too
	// rather than surviving every disconnect untouched.
	if p.MountedPath(deviceID) != "" {
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
