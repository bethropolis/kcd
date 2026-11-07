package sms

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
)

var _ plugin.Plugin = (*SMSPlugin)(nil)

// NewSMSPlugin creates a new SMS and conversation handling plugin.
func NewSMSPlugin(cfg config.SMSConfig, bus *events.Bus, tlsConfig *tls.Config, logger log.Logger, options ...Options) *SMSPlugin {
	var opts Options
	if len(options) > 0 {
		opts = options[0]
	}
	cacheDir := opts.CacheDir
	if cacheDir == "" {
		cacheDir = filepath.Join(os.TempDir(), "kcd", "sms-attachments")
	}
	_ = os.MkdirAll(cacheDir, 0700)

	p := &SMSPlugin{
		sidechannel:   opts.Sidechannel,
		notifications: opts.Notifications,
		cfg:           cfg,
		bus:           bus,
		tlsConfig:     tlsConfig,
		logger:        logger.With(log.String("plugin", "sms")),
		cacheDir:      cacheDir,
		devices:       make(map[string]device.Peer),
		armedAt:       make(map[string]time.Time),
	}

	// Arming is owner-gated: the phone is only asked to push while a client
	// is watching sms.incoming, or when always_arm is set.
	if bus != nil {
		bus.OnSubscriberChange(p.syncArming)
	}

	return p
}

func (p *SMSPlugin) Name() string           { return "SMS" }
func (p *SMSPlugin) Timeout() time.Duration { return 5 * time.Second }

func (p *SMSPlugin) IncomingTypes() []string {
	return []string{PacketTypeSMSMessages, PacketTypeSMSAttachmentFile}
}

func (p *SMSPlugin) OutgoingTypes() []string {
	return []string{
		PacketTypeSMSRequest,
		PacketTypeSMSRequestConvs,
		PacketTypeSMSRequestConv,
		PacketTypeSMSRequestAtt,
	}
}

// OnConnect tracks the device and re-arms it: the phone's arm flag survives
// reconnects but not app restarts, so per-connection arming is the only recovery.
func (p *SMSPlugin) OnConnect(dev device.Peer) {
	p.mu.Lock()
	p.devices[dev.ID()] = dev
	delete(p.armedAt, dev.ID())
	p.mu.Unlock()

	p.syncArming()
}

// OnDisconnect forgets the device so the next connect re-arms it.
func (p *SMSPlugin) OnDisconnect(dev device.Peer) {
	p.mu.Lock()
	delete(p.devices, dev.ID())
	delete(p.armedAt, dev.ID())
	p.mu.Unlock()
}
