package clipboard

import (
	"crypto/tls"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/transport"
)

var _ plugin.Plugin = (*ClipboardPlugin)(nil)

// NewClipboardPlugin creates a clipboard plugin.
func NewClipboardPlugin(tlsConfig *tls.Config, logger log.Logger, pushOnConnect bool, options ...transport.SidechannelOptions) *ClipboardPlugin {
	var sidechannel transport.SidechannelOptions
	if len(options) > 0 {
		sidechannel = options[0]
	}
	return &ClipboardPlugin{
		sidechannel:   sidechannel,
		tlsConfig:     tlsConfig,
		pushOnConnect: pushOnConnect,
		receive:       true,
		send:          true,
		logger:        logger.With(log.String("plugin", "clipboard")),
		probe:         probeBackend,
	}
}

// Name returns the plugin name.
func (p *ClipboardPlugin) Name() string { return "Clipboard" }

// Timeout returns the timeout.
func (p *ClipboardPlugin) Timeout() time.Duration { return 5 * time.Second }

// IncomingTypes returns the packet types this plugin handles.
func (p *ClipboardPlugin) IncomingTypes() []string {
	return []string{"kdeconnect.clipboard", "kdeconnect.clipboard.connect", "kdeconnect.clipboard.file"}
}

// OutgoingTypes returns the packet types this plugin may send.
func (p *ClipboardPlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.clipboard", "kdeconnect.clipboard.connect"}
}

func (p *ClipboardPlugin) OnConnect(dev device.Peer) {
	p.mu.Lock()
	send := p.send
	p.mu.Unlock()
	if !p.pushOnConnect || !send {
		return
	}
	content := p.readClipboard()
	if content == "" {
		return
	}

	p.mu.Lock()
	p.lastPushedContent = content
	ts := p.localChangedAt
	p.mu.Unlock()

	body := ClipboardBody{
		Content:   content,
		Timestamp: ts,
	}
	pkt, err := protocol.NewPacket("kdeconnect.clipboard.connect", body)
	if err != nil {
		p.logger.Debug("clipboard: OnConnect: failed to build packet", log.Error(err))
		return
	}
	// Best-effort — device may still be completing the TLS handshake.
	_ = dev.Send(pkt)
}

func (p *ClipboardPlugin) OnDisconnect(dev device.Peer) {
}
