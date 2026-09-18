package notification

import (
	"context"
	"fmt"
	"io"
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

// sanitizeNotifID strips everything but alphanumerics, dot, underscore and
// hyphen from a notification ID so it can't escape the icon cache directory
// via path separators. Empty results fall back to "default".
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
) string {
	if !p.cfg.FetchIcons || p.tlsConfig == nil || p.iconDir == "" {
		// Fall back to icon name derived from app name.
		return ""
	}

	// Use the notification ID as the filename so the same app reuses the
	// cached icon rather than downloading it on every notification.
	// The ID comes from the phone, so restrict it to filename-safe chars
	// (no separators) — otherwise ../ in an ID would escape the icon dir.
	safeName := nonAlphaNumeric.ReplaceAllString(appName, "_")
	safeID := sanitizeNotifID(notifID)
	iconPath := filepath.Join(p.iconDir, fmt.Sprintf("%s-%s.png", safeName, safeID))

	// Belt and braces: confine the result to the icon dir even if the
	// sanitizer above ever regresses.
	if rel, err := filepath.Rel(p.iconDir, iconPath); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		p.logger.Warn("notification: icon path escapes cache dir, refusing",
			log.String("id", notifID))
		return ""
	}

	// Reuse the cached icon even when the phone re-posts the notification
	// without an icon payload (Android only sends the bytes when the icon
	// hash changes). Without this, every re-post would fall back to a theme
	// icon name that doesn't exist, showing a placeholder image.
	if _, err := os.Stat(iconPath); err == nil {
		return iconPath
	}

	if !hasIcon {
		return ""
	}

	conn, err := transport.DialSidechannel(ctx, remoteIP, port, p.tlsConfig, expectedFP, p.logger, p.sidechannel)
	if err != nil {
		p.logger.Debug("notification: icon dial failed", log.Error(err))
		return ""
	}
	defer conn.Close()

	f, err := os.Create(iconPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	if _, err := io.Copy(f, io.LimitReader(conn, size)); err != nil {
		p.logger.Debug("notification: icon download failed", log.Error(err))
		_ = os.Remove(iconPath)
		return ""
	}

	return iconPath
}
