package share

import (
	"crypto/tls"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/transport"
	"go.uber.org/zap"
)

type progressThrottle struct {
	bus      *events.Bus
	deviceID string
	filename string
	total    int64
	interval time.Duration
	mu       sync.Mutex
	last     time.Time
	pending  int64
}

func newProgressThrottle(bus *events.Bus, deviceID, filename string, total int64) *progressThrottle {
	return &progressThrottle{
		bus:      bus,
		deviceID: deviceID,
		filename: filename,
		total:    total,
		interval: 500 * time.Millisecond,
	}
}

func (t *progressThrottle) Update(current, _ int64) {
	if t.bus == nil {
		return
	}
	t.mu.Lock()
	t.pending = current
	now := time.Now()
	if now.Sub(t.last) < t.interval {
		t.mu.Unlock()
		return
	}
	t.last = now
	cur := t.pending
	t.mu.Unlock()

	t.bus.Publish(events.TypeShareProgress, t.deviceID, map[string]any{
		"file":    t.filename,
		"current": cur,
		"total":   t.total,
	})
}

type SharePlugin struct {
	sidechannel transport.SidechannelOptions
	DownloadDir string
	cfg         config.ShareConfig
	TLSConfig   *tls.Config
	Logger      *zap.Logger
	bus         *events.Bus
}

func NewSharePlugin(downloadDir string, cfg config.ShareConfig, tlsConfig *tls.Config, bus *events.Bus, logger *zap.Logger, options ...transport.SidechannelOptions) *SharePlugin {
	var sidechannel transport.SidechannelOptions
	if len(options) > 0 {
		sidechannel = options[0]
	}
	return &SharePlugin{
		sidechannel: sidechannel,
		DownloadDir: downloadDir,
		cfg:         cfg,
		TLSConfig:   tlsConfig,
		Logger:      logger.With(zap.String("plugin", "share")),
		bus:         bus,
	}
}

// ShareBody includes the missing Android metadata (LastModified/CreationTime)
type ShareBody struct {
	Filename         string `json:"filename"`
	NumberOfFiles    int    `json:"numberOfFiles,omitempty"`
	TotalPayloadSize int64  `json:"totalPayloadSize,omitempty"`
	LastModified     int64  `json:"lastModified,omitempty"`
	CreationTime     int64  `json:"creationTime,omitempty"`
	Text             string `json:"text,omitempty"`
	Url              string `json:"url,omitempty"`
}

func (p *SharePlugin) Name() string { return "Share" }

func (p *SharePlugin) Timeout() time.Duration { return 0 }

func (p *SharePlugin) IncomingTypes() []string {
	return []string{"kdeconnect.share.request"}
}

func (p *SharePlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.share.request"}
}
