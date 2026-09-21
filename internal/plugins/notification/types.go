package notification

import (
	"context"
	"crypto/tls"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/transport"
)

// NotificationPlugin handles incoming notifications and displays them on the desktop.
type NotificationPlugin struct {
	sidechannel    transport.SidechannelOptions
	bus            *events.Bus
	tlsConfig      *tls.Config
	logger         log.Logger
	notifIDs       sync.Map // maps deviceID|body.ID -> desktop notify-send ID (string)
	pendingCloses  sync.Map // maps deviceID|body.ID -> *time.Timer (deferred close for cancel-grace)
	iconDir        string   // temp dir for cached notification icons
	cfg            config.NotificationPluginConfig
	canCloseNotifs bool // whether notify-send supports --print-id
	mu             sync.RWMutex
	filters        config.NotificationConfig
	newExec        func(ctx context.Context, name string, args ...string) *exec.Cmd
}

// NewNotificationPlugin creates a NotificationPlugin.
// tlsConfig is used to fetch notification icon payloads over the KDE Connect
// side-channel; pass nil to disable icon fetching.
func NewNotificationPlugin(cfg config.NotificationPluginConfig, bus *events.Bus, tlsConfig *tls.Config, logger log.Logger, options ...transport.SidechannelOptions) *NotificationPlugin {
	var sidechannel transport.SidechannelOptions
	if len(options) > 0 {
		sidechannel = options[0]
	}
	p := &NotificationPlugin{
		sidechannel: sidechannel,
		cfg:         cfg,
		bus:         bus,
		tlsConfig:   tlsConfig,
		logger:      logger.With(log.String("plugin", "notification")),
		newExec:     exec.CommandContext,
	}

	// Probe --print-id support by checking --help output.
	// This is side-effect-free and immune to version string format changes.
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer probeCancel()
	if out, err := plugin.RunCommandSync(probeCtx, "notify-send", "--help"); err == nil {
		p.canCloseNotifs = strings.Contains(string(out), "--print-id")
	}

	// Create a persistent temp directory for icon files so they survive
	// long enough for the notification daemon to read them.
	baseDir := cfg.IconCacheDir
	if dir, err := os.MkdirTemp(baseDir, "kcd-notif-icons-*"); err == nil {
		p.iconDir = dir
	}

	return p
}

// Close removes the icon temp directory. Call when the plugin is no longer needed.
func (p *NotificationPlugin) Close() {
	if p.iconDir != "" {
		_ = os.RemoveAll(p.iconDir)
	}
	p.pendingCloses.Range(func(k, v any) bool {
		if t, ok := v.(*time.Timer); ok {
			t.Stop()
		}
		return true
	})
}

// SetFilters atomically replaces the per-app notification filter map.
func (p *NotificationPlugin) SetFilters(f config.NotificationConfig) {
	p.mu.Lock()
	p.filters = f
	p.mu.Unlock()
}

// resolveAction returns the configured action for an app ("show" or "silent").
func (p *NotificationPlugin) resolveAction(appName string) string {
	p.mu.RLock()
	f := p.filters
	p.mu.RUnlock()
	if f == nil {
		return "show"
	}
	if action, ok := f[appName]; ok {
		return action
	}
	if def, ok := f["*"]; ok {
		return def
	}
	return "show"
}

// NotificationBody represents the fields of a notification packet.
type NotificationBody struct {
	ID             string `json:"id"`
	AppName        string `json:"appName"`
	Title          string `json:"title"`
	Text           string `json:"text"`
	IsCancel       bool   `json:"isCancel,omitempty"`
	IsClearable    bool   `json:"isClearable,omitempty"`
	Silent         bool   `json:"silent,omitempty"`
	RequestReplyId string `json:"requestReplyId,omitempty"`
}

func (p *NotificationPlugin) Name() string           { return "Notification" }
func (p *NotificationPlugin) Timeout() time.Duration { return 5 * time.Second }
func (p *NotificationPlugin) IncomingTypes() []string {
	return []string{"kdeconnect.notification"}
}
func (p *NotificationPlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.notification.reply", "kdeconnect.notification.request"}
}

// nonAlphaNumeric sanitises app names to be safe for exec / notify-send args.
var nonAlphaNumeric = regexp.MustCompile(`[^a-zA-Z0-9 ._-]`)
