package clipboard

import (
	"crypto/tls"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/transport"
)

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
