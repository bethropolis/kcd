package notification

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// closeNotification closes a previously-shown desktop popup by id.
func (p *NotificationPlugin) closeNotification(desktopID string) {
	go func() {
		_ = p.newExec(context.Background(), "gdbus", "call", "--session",
			"--dest", "org.freedesktop.Notifications",
			"--object-path", "/org/freedesktop/Notifications",
			"--method", "org.freedesktop.Notifications.CloseNotification",
			desktopID,
		).Run()
	}()
}

// sendDesktopNotification calls notify-send with the collected parameters.
func (p *NotificationPlugin) sendDesktopNotification(devID, appName, id, title, text, iconPath string) {
	// Dunst / mako / swaync: stack notifications from the same app so they
	// replace each other instead of flooding the screen. Daemons that ignore
	// this hint (e.g. Quickshell) are covered by --replace-id below.
	groupHint := "string:x-dunst-stack-tag:kcd-" + appName

	args := []string{"-a", appName}
	if p.cfg.Urgency != "" {
		args = append(args, "-u", p.cfg.Urgency)
	}
	if p.cfg.ExpireMS >= 0 {
		args = append(args, "-t", strconv.Itoa(p.cfg.ExpireMS))
	}

	// No icon by default. Pass an explicit empty icon so daemons (e.g.
	// Quickshell) don't fall back to deriving an icon name from the app name
	// and render a placeholder. When show_icons is enabled, pass the phone's
	// downloaded icon, falling back to a name derived from the app.
	iconArg := ""
	if p.cfg.ShowIcons {
		iconArg = strings.ToLower(strings.ReplaceAll(appName, " ", "-"))
		if iconPath != "" {
			iconArg = iconPath
		}
		if iconArg == "" {
			iconArg = "smartphone"
		}
	}
	args = append(args, "-i", iconArg, "-h", groupHint)

	if p.canCloseNotifs && id != "" {
		// Android re-posts notifications on every update with a stable id
		// (e.g. media/scrobble now-playing). Replace the existing desktop
		// popup via D-Bus replaces_id so repeated updates collapse to one,
		// mirroring the reference desktop's Notification::update(). Disable
		// via `replace_notifications = false`.
		if p.cfg.ReplaceNotifications {
			key := p.notifKey(devID, id)
			// Cancel-grace: if a cancel was deferred for this key, drop it so
			// the popup is updated in place rather than closed and re-opened.
			if t, ok := p.pendingCloses.LoadAndDelete(key); ok {
				t.(*time.Timer).Stop()
			}
			if prevID, ok := p.notifIDs.Load(key); ok {
				if s, ok := prevID.(string); ok && s != "" {
					args = append(args, "-r", s)
				}
			}
		}
		// "--" ends option parsing so a phone-provided title/body starting with
		// '-' can't be misparsed as a notify-send flag (notify-send uses
		// GOption, which honors the POSIX separator).
		args = append(args, "--print-id", "--", title, text)
	} else {
		args = append(args, "--", title, text)
	}

	out, err := p.newExec(context.Background(), "notify-send", args...).Output()
	if err == nil && p.canCloseNotifs && id != "" {
		if desktopID := strings.TrimSpace(string(out)); desktopID != "" {
			p.notifIDs.Store(p.notifKey(devID, id), desktopID)
		}
	}
}
