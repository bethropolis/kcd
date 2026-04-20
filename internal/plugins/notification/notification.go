package notification

import (
	"context"
	"encoding/json"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
)

// NotificationPlugin handles incoming notifications and displays them on the desktop.
type NotificationPlugin struct {
	bus            *events.Bus
	notifIDs       sync.Map // maps body.ID (string) -> desktop notify-send ID (string)
	canCloseNotifs bool     // whether notify-send supports --print-id
}

func NewNotificationPlugin(bus *events.Bus) *NotificationPlugin {
	p := &NotificationPlugin{bus: bus}
	// Check if notify-send supports --print-id (libnotify >= 0.8.0)
	out, err := exec.Command("notify-send", "--version").Output()
	if err == nil && strings.Contains(string(out), "0.") {
		// Try to detect version >= 0.8
		parts := strings.Fields(string(out))
		for _, part := range parts {
			if len(part) > 0 && part[0] >= '0' && part[0] <= '9' {
				minor := 0
				if sp := strings.SplitN(part, ".", 3); len(sp) >= 2 {
					minor, _ = strconv.Atoi(sp[1])
				}
				if minorVal, _ := strconv.Atoi(strings.Split(part, ".")[0]); minorVal > 0 || minor >= 8 {
					p.canCloseNotifs = true
				}
				break
			}
		}
	}
	return p
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

// Name returns the plugin name.
func (p *NotificationPlugin) Name() string { return "Notification" }

// Timeout returns the timeout.
func (p *NotificationPlugin) Timeout() time.Duration { return 5 * time.Second }

// IncomingTypes returns the packet types this plugin handles.
func (p *NotificationPlugin) IncomingTypes() []string {
	return []string{"kdeconnect.notification"}
}

// OutgoingTypes returns the packet types this plugin may send.
func (p *NotificationPlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.notification.reply"}
}

// nonAlphaNumeric is used to sanitize app names to be safe for exec/notify-send.
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
					// Use gdbus to call CloseNotification on the Freedesktop interface.
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

	// Truncate text to 512 bytes per absolute rule.
	text := body.Text
	if len(text) > 512 {
		text = text[:512] + "..."
	}

	// Sanitize app name to avoid shell metacharacters per absolute rule.
	appName := nonAlphaNumeric.ReplaceAllString(body.AppName, "")

	if p.bus != nil {
		payload := map[string]interface{}{
			"appName": body.AppName,
			"title":   body.Title,
			"text":    body.Text,
		}
		if body.RequestReplyId != "" {
			payload["requestReplyId"] = body.RequestReplyId
		}
		p.bus.Publish(events.TypeNotification, dev.ID(), payload)
	}

	// Spawning a goroutine as Handlers must not block.
	go func() {
		var args []string

		// Attempt to map the App Name to a standard Linux icon (lowercased without spaces)
		iconName := strings.ToLower(strings.ReplaceAll(appName, " ", "-"))
		if iconName == "" {
			iconName = "smartphone"
		}

		// Use dunst/mako/swaync stacking tags so notifications from the same app replace each other
		// instead of spamming the screen.
		groupHint := "string:x-dunst-stack-tag:kcd-" + appName

		if p.canCloseNotifs && body.ID != "" {
			args = []string{"-a", appName, "-i", iconName, "-h", groupHint, "--print-id", body.Title, text}
		} else {
			args = []string{"-a", appName, "-i", iconName, "-h", groupHint, body.Title, text}
		}

		out, err := exec.Command("notify-send", args...).Output()
		if err == nil && p.canCloseNotifs && body.ID != "" {
			desktopID := strings.TrimSpace(string(out))
			if desktopID != "" {
				p.notifIDs.Store(body.ID, desktopID)
			}
		}
	}()

	return nil
}

func (p *NotificationPlugin) OnConnect(dev device.Sender) {}

func (p *NotificationPlugin) OnDisconnect(dev device.Sender) {
}

// RequestReply sends a reply back to an Android notification.
func (p *NotificationPlugin) RequestReply(dev device.Sender, replyId, message string) error {
	pkt, err := protocol.NewPacket("kdeconnect.notification.reply", map[string]string{
		"requestReplyId": replyId,
		"message":        message,
	})
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}
