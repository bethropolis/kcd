package notification

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

// NotificationPlugin handles incoming notifications and displays them on the desktop.
type NotificationPlugin struct {
	bus            *events.Bus
	tlsConfig      *tls.Config
	logger         *zap.Logger
	notifIDs       sync.Map           // maps body.ID (string) -> desktop notify-send ID (string)
	iconDir        string             // temp dir for cached notification icons
	cfg            config.NotificationPluginConfig
	canCloseNotifs bool               // whether notify-send supports --print-id
	mu             sync.RWMutex
	filters        config.NotificationConfig
}

// NewNotificationPlugin creates a NotificationPlugin.
// tlsConfig is used to fetch notification icon payloads over the KDE Connect
// side-channel; pass nil to disable icon fetching.
func NewNotificationPlugin(cfg config.NotificationPluginConfig, bus *events.Bus, tlsConfig *tls.Config, logger *zap.Logger) *NotificationPlugin {
	p := &NotificationPlugin{
		cfg:       cfg,
		bus:       bus,
		tlsConfig: tlsConfig,
		logger:    logger.With(zap.String("plugin", "notification")),
	}

	// Probe --print-id support by checking --help output.
	// This is side-effect-free and immune to version string format changes.
	if out, err := exec.Command("notify-send", "--help").CombinedOutput(); err == nil {
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
	Silent         bool   `json:"silent,omitempty"`
	RequestReplyId string `json:"requestReplyId,omitempty"`
}

func (p *NotificationPlugin) Name() string           { return "Notification" }
func (p *NotificationPlugin) Timeout() time.Duration { return 5 * time.Second }
func (p *NotificationPlugin) IncomingTypes() []string {
	return []string{"kdeconnect.notification"}
}
func (p *NotificationPlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.notification.reply"}
}

// nonAlphaNumeric sanitises app names to be safe for exec / notify-send args.
var nonAlphaNumeric = regexp.MustCompile(`[^a-zA-Z0-9 ._-]`)

// Handle processes an incoming notification.
func (p *NotificationPlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	var body NotificationBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	// Handle cancellation — close the corresponding desktop notification.
	if body.IsCancel {
		if body.ID != "" {
			if desktopID, ok := p.notifIDs.LoadAndDelete(body.ID); ok {
				go func() {
					_ = exec.Command("gdbus", "call", "--session",
						"--dest", "org.freedesktop.Notifications",
						"--object-path", "/org/freedesktop/Notifications",
						"--method", "org.freedesktop.Notifications.CloseNotification",
						desktopID.(string),
					).Run()
				}()
			}
		}
		if p.bus != nil {
			p.bus.Publish(events.TypeNotificationCanceled, dev.ID(), map[string]string{"id": body.ID})
		}
		return nil
	}

	if body.Silent {
		return nil
	}

	// Apply per-app notification filter.
	action := p.resolveAction(body.AppName)
	if action == "silent" {
		// Still publish the event for scripts/watch, but skip the desktop popup.
		if p.bus != nil {
			payload := map[string]any{
				"appName": body.AppName,
				"title":   body.Title,
				"text":    body.Text,
			}
			if body.RequestReplyId != "" {
				payload["requestReplyId"] = body.RequestReplyId
			}
			p.bus.Publish(events.TypeNotification, dev.ID(), payload)
		}
		return nil
	}

	// Truncate text to keep notifications readable.
	text := body.Text
	if p.cfg.MaxBodyLength > 0 && len(text) > p.cfg.MaxBodyLength {
		text = text[:p.cfg.MaxBodyLength] + "…"
	} else if p.cfg.MaxBodyLength == 0 && len(text) > 512 {
		// Maintain the old default limit if no config is set
		text = text[:512] + "…"
	}

	appName := nonAlphaNumeric.ReplaceAllString(body.AppName, "")

	if p.bus != nil {
		payload := map[string]any{
			"appName": body.AppName,
			"title":   body.Title,
			"text":    body.Text,
		}
		if body.RequestReplyId != "" {
			payload["requestReplyId"] = body.RequestReplyId
		}
		p.bus.Publish(events.TypeNotification, dev.ID(), payload)
	}

	// Capture payload info before the goroutine — pkt may be released.
	var (
		hasIcon     = pkt.PayloadSize > 0 && pkt.PayloadTransferInfo != nil
		payloadSize = pkt.PayloadSize
		payloadPort int
		remoteIP    net.IP
	)
	if hasIcon {
		payloadPort = pkt.PayloadTransferInfo.Port
		remoteIP = dev.RemoteIP()
		if remoteIP == nil {
			hasIcon = false
		}
	}

	// Handlers must not block — all I/O in a goroutine.
	go func() {
		iconPath := p.fetchIcon(ctx, appName, body.ID, remoteIP, payloadPort, payloadSize, hasIcon)
		p.sendDesktopNotification(appName, body.ID, body.Title, text, iconPath)
	}()

	return nil
}

// fetchIcon downloads the notification icon payload and returns the path to the
// saved file, or an empty string if unavailable.
func (p *NotificationPlugin) fetchIcon(
	ctx context.Context,
	appName, notifID string,
	remoteIP net.IP,
	port int,
	size int64,
	hasIcon bool,
) string {
	if !hasIcon || !p.cfg.FetchIcons || p.tlsConfig == nil || p.iconDir == "" {
		// Fall back to icon name derived from app name.
		return ""
	}

	// Use the notification ID as the filename so the same app reuses the
	// cached icon rather than downloading it on every notification.
	safeName := nonAlphaNumeric.ReplaceAllString(appName, "_")
	iconPath := filepath.Join(p.iconDir, fmt.Sprintf("%s-%s.png", safeName, notifID))

	// Already cached from a previous notification from this app.
	if _, err := os.Stat(iconPath); err == nil {
		return iconPath
	}

	addr := fmt.Sprintf("%s:%d", remoteIP, port)
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 10 * time.Second},
		Config:    p.tlsConfig,
	}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		p.logger.Debug("notification: icon dial failed", zap.Error(err))
		return ""
	}
	defer conn.Close()

	f, err := os.Create(iconPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	if _, err := io.Copy(f, io.LimitReader(conn, size)); err != nil {
		p.logger.Debug("notification: icon download failed", zap.Error(err))
		_ = os.Remove(iconPath)
		return ""
	}

	return iconPath
}

// sendDesktopNotification calls notify-send with the collected parameters.
func (p *NotificationPlugin) sendDesktopNotification(appName, id, title, text, iconPath string) {
	// Derive a fallback icon name from the app name when no payload icon is available.
	iconArg := strings.ToLower(strings.ReplaceAll(appName, " ", "-"))
	if iconPath != "" {
		iconArg = iconPath
	}
	if iconArg == "" {
		iconArg = "smartphone"
	}

	// Dunst / mako / swaync: stack notifications from the same app so they
	// replace each other instead of flooding the screen.
	groupHint := "string:x-dunst-stack-tag:kcd-" + appName
	
	args := []string{"-a", appName}
	if p.cfg.Urgency != "" {
		args = append(args, "-u", p.cfg.Urgency)
	}
	if p.cfg.ExpireMS >= 0 {
		args = append(args, "-t", strconv.Itoa(p.cfg.ExpireMS))
	}
	args = append(args, "-i", iconArg, "-h", groupHint)
	
	if p.canCloseNotifs && id != "" {
		args = append(args, "--print-id", title, text)
	} else {
		args = append(args, title, text)
	}

	out, err := exec.Command("notify-send", args...).Output()
	if err == nil && p.canCloseNotifs && id != "" {
		if desktopID := strings.TrimSpace(string(out)); desktopID != "" {
			p.notifIDs.Store(id, desktopID)
		}
	}
}

// RequestReply sends a reply back to an Android notification.
func (p *NotificationPlugin) RequestReply(dev device.Sender, replyID, message string) error {
	pkt, err := protocol.NewPacket("kdeconnect.notification.reply", map[string]string{
		"requestReplyId": replyID,
		"message":        message,
	})
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}

func (p *NotificationPlugin) OnConnect(_ device.Sender)    {}
func (p *NotificationPlugin) OnDisconnect(_ device.Sender) {}
