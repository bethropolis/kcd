package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/plugins/connectivity"
	"github.com/bethropolis/kcd/internal/plugins/remotesystemvolume"
	"github.com/bethropolis/kcd/internal/plugins/telephony"
)

// truncate shortens free-form text to keep one event on one line.
func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max-1] + "…"
	}
	return s
}

// runCommandLabel names the execution, falling back to the id when the plugin
// published no key.
func runCommandLabel(payload map[string]any) string {
	if key := str(payload, "key"); key != "" {
		return oneLine(truncate(key, 40))
	}
	return fmt.Sprintf("#%v", payload["id"])
}

// runCommandDetail renders one runcommand event body, branching on status:
// an absent boolean would otherwise make a start look like a failure.
func runCommandDetail(payload map[string]any) string {
	if lines, ok := payload["stdout"].([]any); ok {
		groups := make([]string, 0, 2)
		if out := runCommandLines(lines, "out"); out != "" {
			groups = append(groups, out)
		}
		if errOut := runCommandLines(payload["stderr"], "err"); errOut != "" {
			groups = append(groups, errOut)
		}
		return strings.Join(groups, " | ")
	}
	if str(payload, "status") == "started" {
		return "running"
	}
	if text := oneLine(truncate(str(payload, "output"), 200)); text != "" {
		return text
	}
	if ok, _ := payload["success"].(bool); ok {
		return "ok"
	}
	return "failed"
}

func runCommandLines(raw any, tag string) string {
	lines, ok := raw.([]any)
	if !ok || len(lines) == 0 {
		return ""
	}
	parts := make([]string, 0, len(lines))
	for _, l := range lines {
		if s, ok := l.(string); ok && s != "" {
			parts = append(parts, tag+": "+oneLine(s))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " | ")
}

// oneLine collapses newlines so a multi-line message cannot break the stream's
// line-per-event shape.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// volumeSuffix renders the event's optional volume: absent (phone picked it)
// renders as nothing, never a stray separator.
func volumeSuffix(payload map[string]any) string {
	if v, ok := payload["volume"].(string); ok && v != "" {
		return " (" + v + ")"
	}
	return ""
}

// decodePayload re-decodes a payload into a concrete type: daemon JSON arrives
// untyped, and round-tripping reuses the dedicated commands' formatter.
func decodePayload(payload any, out any) bool {
	b, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	return json.Unmarshal(b, out) == nil
}

func str(payload map[string]any, key string) string {
	s, _ := payload[key].(string)
	return s
}

// num reads a numeric payload field. Values arrive as float64 from JSON but
// may be integers in-process: accept all widths rather than reading zero.
func num(payload map[string]any, key string) (int, bool) {
	switch v := payload[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	case json.Number:
		n, err := v.Int64()
		return int(n), err == nil
	default:
		return 0, false
	}
}

// formatEvent renders one event human-readable, newline included (or leading
// carriage return for in-place progress), so callers print without adding one.
func formatEvent(ev events.Event) string {
	// A nil or non-map payload still renders for the types below that read
	// nothing; the map assertion simply yields nil and lookups return "".
	payload, _ := ev.Payload.(map[string]interface{})

	switch ev.Type {
	case events.TypeBatteryUpdate:
		return fmt.Sprintf("[%s] battery: %v%% (charging: %v)\n", ev.DeviceID, payload["charge"], payload["charging"])

	case events.TypeBatteryThreshold:
		level := "full"
		if e, ok := num(payload, "event"); ok && e == 1 {
			level = "low"
		}
		return fmt.Sprintf("[%s] battery %s: %v%%%s\n", ev.DeviceID, level, payload["charge"], chargingSuffix(payload))

	case events.TypeNotification:
		return fmt.Sprintf("[%s] notification: %s - %s\n", ev.DeviceID, payload["appName"], payload["title"])

	case events.TypeNotificationCanceled:
		return fmt.Sprintf("[%s] notification cancelled: %s\n", ev.DeviceID, payload["id"])

	case events.TypeShareProgress:
		return fmt.Sprintf("\r[%s] transfer: %s... %v/%v bytes", ev.DeviceID, payload["file"], payload["current"], payload["total"])

	case events.TypeShareComplete:
		return fmt.Sprintf("\n[%s] transfer complete: %s\n", ev.DeviceID, payload["file"])

	case events.TypeShareText:
		return fmt.Sprintf("[%s] share text: %s\n", ev.DeviceID, payload["text"])

	case events.TypeShareURL:
		return fmt.Sprintf("[%s] share url: %s\n", ev.DeviceID, payload["url"])

	case events.TypeSftpMount:
		return fmt.Sprintf("[%s] SFTP credentials received: %s\n", ev.DeviceID, payload["uri"])

	case events.TypeSftpMounted:
		return fmt.Sprintf("[%s] SFTP mounted at %s%s\n", ev.DeviceID, payload["mountPoint"], volumeSuffix(payload))

	case events.TypeSftpUnmounted:
		return fmt.Sprintf("[%s] SFTP unmounted (was %s)\n", ev.DeviceID, payload["mountPoint"])

	case events.TypePairRequested:
		return fmt.Sprintf("[%s] pair request from %s (%s). code: %v\n", ev.DeviceID, payload["name"], payload["type"], payload["verificationKey"])

	case events.TypePairAccepted:
		return fmt.Sprintf("[%s] paired with %s\n", ev.DeviceID, payload["name"])

	case events.TypePairRejected:
		return fmt.Sprintf("[%s] pair rejected or cancelled by %s\n", ev.DeviceID, payload["name"])

	case events.TypeMprisUpdate:
		state := "⏹"
		if isPlaying, _ := payload["isPlaying"].(bool); isPlaying {
			state = "▶"
		} else if ps, _ := payload["playbackStatus"].(string); ps == "Paused" {
			state = "⏸"
		}
		player, _ := payload["player"].(string)
		title, _ := payload["title"].(string)
		artist, _ := payload["artist"].(string)
		var b strings.Builder
		fmt.Fprintf(&b, "[%s] %s %s", ev.DeviceID, state, player)
		if title != "" {
			fmt.Fprintf(&b, " - %s", title)
		}
		if artist != "" {
			fmt.Fprintf(&b, " (%s)", artist)
		}
		b.WriteString("\n")
		return b.String()

	case events.TypeSMSIncoming:
		return fmt.Sprintf("[%s] sms from %s: %s\n", ev.DeviceID, payload["sender"], oneLine(truncate(str(payload, "body"), 72)))

	case events.TypeSMSAttachment:
		return fmt.Sprintf("[%s] sms attachment saved: %s\n", ev.DeviceID, oneLine(truncate(str(payload, "filename"), 48)))

	case events.TypePingReceived:
		return fmt.Sprintf("[%s] ping: %s\n", ev.DeviceID, oneLine(truncate(str(payload, "message"), 60)))

	case events.TypeConnectivityUpdate:
		var body connectivity.ConnectivityBody
		if !decodePayload(ev.Payload, &body) {
			break
		}
		lines := formatConnectivity(body)
		if len(lines) == 0 {
			break
		}
		return fmt.Sprintf("[%s] connectivity: %s\n", ev.DeviceID, strings.Join(lines, ", "))

	case events.TypeTelephonyRinging, events.TypeTelephonyTalking, events.TypeTelephonyMissed, events.TypeTelephonyCanceled:
		var body telephony.TelephonyBody
		if !decodePayload(ev.Payload, &body) {
			break
		}
		// Wording matches the desktop notification this same event raises, so
		// the terminal line and the popup read the same way.
		caller := callerLabel(&body)
		if caller == "" {
			return fmt.Sprintf("[%s] %s\n", ev.DeviceID, telephonyLabel(ev.Type))
		}
		return fmt.Sprintf("[%s] %s: %s\n", ev.DeviceID, telephonyLabel(ev.Type), caller)

	case events.TypeVolumeUpdate:
		// Two publishers share this type: the systemvolume plugin reports a
		// single stream, remotesystemvolume can report a whole sink list.
		if _, ok := payload["sinks"]; ok {
			return fmt.Sprintf("[%s] volume: %s\n", ev.DeviceID, sinkSummary(payload["sinks"]))
		}
		name := str(payload, "name")
		if name == "" {
			name = "output"
		}
		name = oneLine(truncate(name, 40))
		if m, _ := payload["muted"].(bool); m {
			return fmt.Sprintf("[%s] volume: %s (muted)\n", ev.DeviceID, name)
		}
		return fmt.Sprintf("[%s] volume: %s %v%%\n", ev.DeviceID, name, payload["volume"])

	case events.TypeRunCommandOutput:
		return fmt.Sprintf("[%s] runcommand %s: %s\n", ev.DeviceID, runCommandLabel(payload), runCommandDetail(payload))

	case events.TypeContactsUpdated:
		if str(payload, "phase") == "vcards" {
			stored, _ := num(payload, "stored")
			skipped, _ := num(payload, "skipped")
			if skipped == 0 {
				return fmt.Sprintf("[%s] contacts: %d saved\n", ev.DeviceID, stored)
			}
			return fmt.Sprintf("[%s] contacts: %d saved, %d skipped\n", ev.DeviceID, stored, skipped)
		}
		return fmt.Sprintf("[%s] contacts: %s\n", ev.DeviceID, contactDelta(payload))

	// device.connected / device.added keep the bare type token as the first
	// field so anything grepping for it still matches; the detail follows.
	case events.TypeDeviceAdded:
		// The payload is a full device view. Still tolerating a bare string
		// keeps the renderer working if a payload ever predates that change.
		var name string
		if s, ok := ev.Payload.(string); ok {
			name = s
		} else if info, ok := ev.Payload.(map[string]any); ok {
			name = str(info, "name")
		}
		if name == "" {
			break
		}
		return fmt.Sprintf("[%s] device.added: %s\n", ev.DeviceID, oneLine(truncate(name, 40)))

	case events.TypeDeviceConnected:
		name, typeName := str(payload, "name"), str(payload, "type")
		if name == "" && typeName == "" {
			break
		}
		return fmt.Sprintf("[%s] device.connected: %s (%s)\n", ev.DeviceID, oneLine(truncate(name, 32)), typeName)

	default:
		// state.snapshot is written straight to the socket by IPC; rendering
		// it would flood the terminal. The rest here carry no payload.
		return fmt.Sprintf("[%s] %s\n", ev.DeviceID, ev.Type)
	}

	return fmt.Sprintf("[%s] %s\n", ev.DeviceID, ev.Type)
}

// callerLabel prefers a contact name and falls back to the raw number, the way
// the desktop notification does.
func callerLabel(body *telephony.TelephonyBody) string {
	if body.ContactName != "" {
		if body.PhoneNumber != "" {
			return fmt.Sprintf("%s (%s)", oneLine(truncate(body.ContactName, 32)), body.PhoneNumber)
		}
		return oneLine(truncate(body.ContactName, 40))
	}
	if body.PhoneNumber != "" {
		return body.PhoneNumber
	}
	return ""
}

// telephonyLabel names the call event in the notification's plain wording,
// so CLI and desktop never describe the same event differently.
func telephonyLabel(typ events.EventType) string {
	switch typ {
	case events.TypeTelephonyRinging:
		return "incoming call"
	case events.TypeTelephonyTalking:
		return "call answered"
	case events.TypeTelephonyMissed:
		return "missed call"
	default:
		return "call ended"
	}
}

// chargingSuffix only mentions charging when it is actually happening.
func chargingSuffix(payload map[string]any) string {
	if c, _ := payload["charging"].(bool); c {
		return " (charging)"
	}
	return ""
}

// contactDelta describes a contacts sync pass, dropping zero counts so a
// no-op phase does not read as "+0 ~0 -0".
func contactDelta(payload map[string]any) string {
	added, _ := num(payload, "added")
	updated, _ := num(payload, "updated")
	deleted, _ := num(payload, "deleted")
	pending, _ := num(payload, "pending")

	var parts []string
	if added > 0 {
		parts = append(parts, fmt.Sprintf("%d added", added))
	}
	if updated > 0 {
		parts = append(parts, fmt.Sprintf("%d updated", updated))
	}
	if deleted > 0 {
		parts = append(parts, fmt.Sprintf("%d deleted", deleted))
	}
	if pending > 0 {
		parts = append(parts, fmt.Sprintf("%d pending", pending))
	}
	if len(parts) == 0 {
		return "no changes"
	}
	return strings.Join(parts, ", ")
}

// sinkSummary renders a sink list as "Speaker 42%, Headset 80%": raw %v would
// print Go slice syntax no person should have to read.
func sinkSummary(raw any) string {
	var sinks []remotesystemvolume.SinkInfo
	if !decodePayload(raw, &sinks) {
		return "unknown"
	}
	if len(sinks) == 0 {
		return "no streams"
	}

	const maxSinks = 3
	parts := make([]string, 0, maxSinks+1)
	for i, s := range sinks {
		if i == maxSinks {
			parts = append(parts, fmt.Sprintf("+%d more", len(sinks)-maxSinks))
			break
		}
		label := oneLine(truncate(s.Name, 28))
		if label == "" {
			label = s.Description
		}
		if label == "" {
			label = "stream"
		}
		if s.Muted {
			parts = append(parts, fmt.Sprintf("%s (muted)", label))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %d%%", label, s.Volume))
	}
	return strings.Join(parts, ", ")
}
