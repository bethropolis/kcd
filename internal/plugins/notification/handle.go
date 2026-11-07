package notification

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
)

// Handle processes an incoming notification.
func (p *NotificationPlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	var body NotificationBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	// Handle cancellation — close the corresponding desktop notification.
	if body.IsCancel {
		if body.ID != "" {
			key := p.notifKey(dev.ID(), body.ID)
			if desktopID, ok := p.notifIDs.Load(key); ok {
				if p.cfg.CancelGraceMS > 0 {
					// Hold the popup through the cancel grace so a same-id re-post
					// updates it in place; close when the window passes with none.
					if t, ok := p.pendingCloses.Load(key); ok {
						t.(*time.Timer).Stop()
					}
					want := desktopID.(string)
					p.pendingCloses.Store(key, time.AfterFunc(
						time.Duration(p.cfg.CancelGraceMS)*time.Millisecond,
						func() {
							if cur, ok := p.notifIDs.LoadAndDelete(key); ok {
								// Only close the popup we scheduled — if a
								// re-post replaced it, leave the new one.
								if cur.(string) == want {
									p.closeNotification(want)
								}
							}
							p.pendingCloses.Delete(key)
						},
					))
				} else {
					p.notifIDs.Delete(key)
					p.closeNotification(desktopID.(string))
				}
			}
		}
		if p.bus != nil {
			p.bus.Publish(events.TypeNotificationCanceled, dev.ID(), map[string]string{"id": body.ID})
		}
		return nil
	}

	// Skip non-clearable notifications (media playback, foreground services).
	if p.cfg.SkipNonClearable && !body.IsClearable {
		return nil
	}

	if p.bus != nil {
		payload := map[string]any{
			"id":      body.ID,
			"appName": body.AppName,
			"title":   body.Title,
			"text":    body.Text,
		}
		if body.Silent {
			payload["silent"] = true
		}
		if body.Ticker != "" {
			payload["ticker"] = body.Ticker
		}
		if body.Time != "" {
			payload["time"] = body.Time
		}
		if body.GroupName != "" {
			payload["groupName"] = body.GroupName
		}
		if len(body.Actions) > 0 {
			payload["actions"] = body.Actions
		}
		if len(body.Conversation) > 0 {
			payload["conversation"] = body.Conversation
		}
		if body.RequestReplyId != "" {
			payload["requestReplyId"] = body.RequestReplyId
		}
		if body.PayloadHash != "" {
			payload["payloadHash"] = body.PayloadHash
		}
		p.bus.Publish(events.TypeNotification, dev.ID(), payload)
	}

	// Apply per-app notification filter and popup policy.
	action := p.resolveAction(body.AppName)
	if body.Silent || action == "silent" || !p.popups.Allows("notification") {
		return nil
	}

	// Format notification text, rendering conversation transcripts when present.
	text := body.Text
	if len(body.Conversation) > 0 {
		var lines []string
		for _, msg := range body.Conversation {
			if msg.Sender != "" {
				lines = append(lines, msg.Sender+": "+msg.Content)
			} else if msg.Content != "" {
				lines = append(lines, msg.Content)
			}
		}
		if len(lines) > 0 {
			text = strings.Join(lines, "\n")
		}
	}

	// Truncate text to keep notifications readable.
	if p.cfg.MaxBodyLength > 0 && len(text) > p.cfg.MaxBodyLength {
		text = text[:p.cfg.MaxBodyLength] + "…"
	} else if p.cfg.MaxBodyLength == 0 && len(text) > 512 {
		// Maintain the old default limit if no config is set
		text = text[:512] + "…"
	}

	appName := nonAlphaNumeric.ReplaceAllString(body.AppName, "")
	title := body.Title
	if title == "" && body.GroupName != "" {
		title = body.GroupName
	}

	// Capture payload info before the goroutine — pkt may be released.
	var (
		hasIcon     = pkt.PayloadSize > 0 && pkt.PayloadTransferInfo != nil
		payloadSize = pkt.PayloadSize
		payloadPort int
		remoteIP    net.IP
		expectedFP  string
	)
	if hasIcon {
		payloadPort = pkt.PayloadTransferInfo.Port
		remoteIP = dev.RemoteIP()
		if remoteIP == nil {
			hasIcon = false
		} else {
			expectedFP = cert.PinnedFingerprint(dev.PeerCert())
		}
	}

	// Handlers must not block — all I/O in a goroutine.
	go func() {
		dlCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		var iconPath string
		if p.cfg.ShowIcons {
			iconPath = p.fetchIcon(dlCtx, appName, body.ID, remoteIP, payloadPort, payloadSize, hasIcon, expectedFP, body.PayloadHash)
		}
		p.sendDesktopNotification(dev.ID(), appName, body.ID, title, text, iconPath)
	}()

	return nil
}
