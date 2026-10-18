package notification

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/transport"
)

// notifIDChars restricts phone-provided notification IDs to filename-safe
// characters when they're embedded in icon cache paths.
var notifIDChars = regexp.MustCompile(`[^a-zA-Z0-9._-]`)
var payloadHashPattern = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)

// sanitizeNotifID strips everything but safe chars so IDs can't escape the
// icon cache via separators; empty results fall back to "default".
func sanitizeNotifID(id string) string {
	if safe := notifIDChars.ReplaceAllString(id, "_"); safe != "" {
		return safe
	}
	return "default"
}

// notifKey scopes a notification id to its device so two paired phones with
// colliding Android notification keys don't replace each other's popups.
func (p *NotificationPlugin) notifKey(devID, id string) string {
	return devID + "|" + id
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
	expectedFP string,
	payloadHash ...string,
) string {
	if !p.cfg.FetchIcons || p.tlsConfig == nil || p.iconDir == "" {
		// Fall back to icon name derived from app name.
		return ""
	}

	// Use the notification ID as the filename, restricted to safe chars:
	// ../ in a phone-supplied ID would otherwise escape the icon dir.
	safeName := nonAlphaNumeric.ReplaceAllString(appName, "_")
	safeID := sanitizeNotifID(notifID)
	cacheName := fmt.Sprintf("%s-%s", safeName, safeID)
	if len(payloadHash) > 0 && payloadHash[0] != "" {
		if !payloadHashPattern.MatchString(payloadHash[0]) {
			return ""
		}
		cacheName = strings.ToLower(payloadHash[0])
	}
	iconPath := filepath.Join(p.iconDir, cacheName+".png")

	// Belt and braces: confine the result to the icon dir even if the
	// sanitizer above ever regresses.
	if rel, err := filepath.Rel(p.iconDir, iconPath); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		p.logger.Warn("notification: icon path escapes cache dir, refusing",
			log.String("id", notifID))
		return ""
	}

	// Android sends icon bytes only when the hash changes; a matching cached
	// file can be reused when the packet carries metadata without a payload.
	if !hasIcon {
		if _, err := os.Stat(iconPath); err == nil {
			return iconPath
		}
		return ""
	}
	if size <= 0 || size > 1024*1024 {
		return ""
	}

	if _, err := transport.ReceiveToFile(ctx, remoteIP, port, size, iconPath, p.tlsConfig, expectedFP, p.logger, transport.ReceiveOptions{MaxSize: 1024 * 1024, Sidechannel: p.sidechannel}); err != nil {
		p.logger.Debug("notification: icon download failed", log.Error(err))
		return ""
	}

	return iconPath
}
