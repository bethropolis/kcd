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
	// Stack same-app notifications so they replace instead of flooding; daemons
	// ignoring the hint are covered by --replace-id below.
	groupHint := "string:x-dunst-stack-tag:kcd-" + appName

	args := []string{"-a", appName}
	if p.cfg.Urgency != "" {
		args = append(args, "-u", p.cfg.Urgency)
	}
	if p.cfg.ExpireMS >= 0 {
		args = append(args, "-t", strconv.Itoa(p.cfg.ExpireMS))
	}

	// No icon by default: pass explicit empty so daemons don't derive one
	// from the app name and render a placeholder. show_icons uses the download.
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
		// Android re-posts each update under a stable id; replaces_id keeps
		// them one popup. `replace_notifications = false` disables this.
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
		// "--" ends option parsing so a phone title/body starting with '-'
		// isn't misparsed as a notify-send flag.
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
