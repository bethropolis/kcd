package notification

import (
	"context"
	"encoding/json"
	"net"
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
					// Cancel-grace: hold the popup open so a same-id re-post
					// (media now-playing toggling play/pause) updates it in
					// place instead of closing and re-opening. If no re-post
					// arrives, close after the grace window.
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

	if body.Silent {
		return nil
	}

	// Skip non-clearable notifications (media playback, foreground services).
	if p.cfg.SkipNonClearable && !body.IsClearable {
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
		var iconPath string
		if p.cfg.ShowIcons {
			iconPath = p.fetchIcon(ctx, appName, body.ID, remoteIP, payloadPort, payloadSize, hasIcon, expectedFP)
		}
		p.sendDesktopNotification(dev.ID(), appName, body.ID, body.Title, text, iconPath)
	}()

	return nil
}
