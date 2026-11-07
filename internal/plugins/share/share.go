package share

import (
	"crypto/tls"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/transport"
)

var _ plugin.Plugin = (*SharePlugin)(nil)

func NewSharePlugin(downloadDir string, cfg config.ShareConfig, tlsConfig *tls.Config, bus *events.Bus, logger log.Logger, options ...transport.SidechannelOptions) *SharePlugin {
	var sidechannel transport.SidechannelOptions
	if len(options) > 0 {
		sidechannel = options[0]
	}
	return &SharePlugin{
		sidechannel: sidechannel,
		DownloadDir: downloadDir,
		cfg:         cfg,
		TLSConfig:   tlsConfig,
		Logger:      logger.With(log.String("plugin", "share")),
		bus:         bus,
	}
}

func (p *SharePlugin) Name() string { return "Share" }

func (p *SharePlugin) Timeout() time.Duration { return 0 }

func (p *SharePlugin) IncomingTypes() []string {
	return []string{"kdeconnect.share.request", "kdeconnect.share.request.update"}
}

func (p *SharePlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.share.request"}
}

func (p *SharePlugin) OnConnect(dev device.Peer)    {}
func (p *SharePlugin) OnDisconnect(dev device.Peer) {}
