package notification

import (
	"context"
	"crypto/tls"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
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
	popups         config.PopupsConfig
	backend        string
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

// ConversationMessage represents a single message in a conversation notification.
type ConversationMessage struct {
	Sender  string `json:"sender"`
	Content string `json:"content"`
}

// NotificationBody represents the fields of a notification packet.
type NotificationBody struct {
	ID             string                `json:"id"`
	AppName        string                `json:"appName"`
	Title          string                `json:"title"`
	Text           string                `json:"text"`
	Ticker         string                `json:"ticker,omitempty"`
	Time           string                `json:"time,omitempty"`
	GroupName      string                `json:"groupName,omitempty"`
	Actions        []string              `json:"actions,omitempty"`
	Conversation   []ConversationMessage `json:"conversation,omitempty"`
	IsCancel       bool                  `json:"isCancel,omitempty"`
	IsClearable    bool                  `json:"isClearable,omitempty"`
	Silent         bool                  `json:"silent,omitempty"`
	RequestReplyId string                `json:"requestReplyId,omitempty"`
	PayloadHash    string                `json:"payloadHash,omitempty"`
}

// nonAlphaNumeric sanitises app names to be safe for exec / notify-send args.
var nonAlphaNumeric = regexp.MustCompile(`[^a-zA-Z0-9 ._-]`)
