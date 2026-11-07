package clipboard

import (
	"crypto/tls"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/transport"
)

var _ plugin.Plugin = (*ClipboardPlugin)(nil)

type clipboardBackend int

const (
	backendUnknown clipboardBackend = iota
	backendWayland
	backendX11
)

// ClipboardPlugin handles clipboard sync both directions.
type ClipboardPlugin struct {
	sidechannel       transport.SidechannelOptions
	pushOnConnect     bool
	receive           bool
	send              bool
	lastTimestamp     int64
	localChangedAt    int64
	tlsConfig         *tls.Config
	logger            log.Logger
	backend           clipboardBackend
	wlDisplay         string // WAYLAND_DISPLAY value for spawned subprocesses
	probe             func() (clipboardBackend, string)
	mu                sync.Mutex
	lastContent       string // last content received from phone (inbound)
	lastPushedContent string // last content sent to phone (outbound)
}

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

// SetPolicy updates the global clipboard direction policy.
func (p *ClipboardPlugin) SetPolicy(receive, send bool) {
	p.mu.Lock()
	p.receive = receive
	p.send = send
	p.mu.Unlock()
}

// clipboardTimeout bounds every wl-copy/wl-paste/xclip subprocess so a hung
// clipboard tool cannot block clipboard sync indefinitely.
const clipboardTimeout = 2 * time.Second

// ClipboardBody represents the content of a clipboard packet.
type ClipboardBody struct {
	Content   string `json:"content"`
	Timestamp int64  `json:"timestamp,omitempty"`
}

// Name returns the plugin name.
func (p *ClipboardPlugin) Name() string { return "Clipboard" }

// Timeout returns the timeout.
func (p *ClipboardPlugin) Timeout() time.Duration { return 5 * time.Second }

// IncomingTypes returns the packet types this plugin handles.
func (p *ClipboardPlugin) IncomingTypes() []string {
	return []string{"kdeconnect.clipboard", "kdeconnect.clipboard.connect", "kdeconnect.clipboard.file"}
}

// LocalChangedAt returns the timestamp of the last local clipboard change in epoch ms.
func (p *ClipboardPlugin) LocalChangedAt() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.localChangedAt
}

// SetLocalChangedAt sets the timestamp of the last local clipboard change in epoch ms.
func (p *ClipboardPlugin) SetLocalChangedAt(ts int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.localChangedAt = ts
}

// OutgoingTypes returns the packet types this plugin may send.
func (p *ClipboardPlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.clipboard", "kdeconnect.clipboard.connect"}
}
