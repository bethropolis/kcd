package main

import (
	"strings"
	"testing"

	"github.com/bethropolis/kcd/internal/events"
)

// These tests pin the exact output of every format that existed before
// formatEvent was extracted. The extraction is a pure refactor, so any change
// here means a line a script may already parse has moved. New formats are
// covered separately in TestFormatEventNewTypes.
func TestFormatEventExistingFormats(t *testing.T) {
	tests := []struct {
		name string
		ev   events.Event
		want string
	}{
		{
			name: "battery",
			ev:   events.Event{Type: events.TypeBatteryUpdate, DeviceID: "d1", Payload: map[string]any{"charge": 62, "charging": false}},
			want: "[d1] battery: 62% (charging: false)\n",
		},
		{
			name: "notification",
			ev:   events.Event{Type: events.TypeNotification, DeviceID: "d1", Payload: map[string]any{"appName": "WhatsApp", "title": "Alice"}},
			want: "[d1] notification: WhatsApp - Alice\n",
		},
		{
			name: "notification canceled",
			ev:   events.Event{Type: events.TypeNotificationCanceled, DeviceID: "d1", Payload: map[string]any{"id": "notif-abc"}},
			want: "[d1] notification cancelled: notif-abc\n",
		},
		{
			name: "share progress keeps leading carriage return and no newline",
			ev:   events.Event{Type: events.TypeShareProgress, DeviceID: "d1", Payload: map[string]any{"file": "a.png", "current": 10, "total": 20}},
			want: "\r[d1] transfer: a.png... 10/20 bytes",
		},
		{
			name: "share complete keeps leading newline",
			ev:   events.Event{Type: events.TypeShareComplete, DeviceID: "d1", Payload: map[string]any{"file": "a.png"}},
			want: "\n[d1] transfer complete: a.png\n",
		},
		{
			name: "share text",
			ev:   events.Event{Type: events.TypeShareText, DeviceID: "d1", Payload: map[string]any{"text": "hi"}},
			want: "[d1] share text: hi\n",
		},
		{
			name: "share url",
			ev:   events.Event{Type: events.TypeShareURL, DeviceID: "d1", Payload: map[string]any{"url": "https://kdeconnect.org"}},
			want: "[d1] share url: https://kdeconnect.org\n",
		},
		{
			name: "sftp mount",
			ev:   events.Event{Type: events.TypeSftpMount, DeviceID: "d1", Payload: map[string]any{"uri": "sftp://x"}},
			want: "[d1] SFTP credentials received: sftp://x\n",
		},
		{
			name: "sftp mounted",
			ev:   events.Event{Type: events.TypeSftpMounted, DeviceID: "d1", Payload: map[string]any{"mountPoint": "/mnt/kcd-sftp-d1"}},
			want: "[d1] SFTP mounted at /mnt/kcd-sftp-d1\n",
		},
		{
			name: "sftp mounted with volume",
			ev:   events.Event{Type: events.TypeSftpMounted, DeviceID: "d1", Payload: map[string]any{"mountPoint": "/mnt/kcd-sftp-d1", "volume": "/storage/ABCD-1234"}},
			want: "[d1] SFTP mounted at /mnt/kcd-sftp-d1 (/storage/ABCD-1234)\n",
		},
		{
			name: "sftp unmounted",
			ev:   events.Event{Type: events.TypeSftpUnmounted, DeviceID: "d1", Payload: map[string]any{"mountPoint": "/mnt/kcd-sftp-d1"}},
			want: "[d1] SFTP unmounted (was /mnt/kcd-sftp-d1)\n",
		},
		{
			name: "pair requested",
			ev:   events.Event{Type: events.TypePairRequested, DeviceID: "d1", Payload: map[string]any{"name": "Pixel", "type": "phone", "verificationKey": "12345"}},
			want: "[d1] pair request from Pixel (phone). code: 12345\n",
		},
		{
			name: "pair accepted",
			ev:   events.Event{Type: events.TypePairAccepted, DeviceID: "d1", Payload: map[string]any{"name": "Pixel"}},
			want: "[d1] paired with Pixel\n",
		},
		{
			name: "pair rejected",
			ev:   events.Event{Type: events.TypePairRejected, DeviceID: "d1", Payload: map[string]any{"name": "Pixel"}},
			want: "[d1] pair rejected or cancelled by Pixel\n",
		},
		{
			name: "mpris playing with title and artist",
			ev:   events.Event{Type: events.TypeMprisUpdate, DeviceID: "d1", Payload: map[string]any{"isPlaying": true, "player": "Firefox", "title": "Song", "artist": "Band"}},
			want: "[d1] ▶ Firefox - Song (Band)\n",
		},
		{
			name: "mpris paused",
			ev:   events.Event{Type: events.TypeMprisUpdate, DeviceID: "d1", Payload: map[string]any{"playbackStatus": "Paused", "player": "Firefox"}},
			want: "[d1] ⏸ Firefox\n",
		},
		{
			name: "mpris stopped omits empty title and artist",
			ev:   events.Event{Type: events.TypeMprisUpdate, DeviceID: "d1", Payload: map[string]any{"isPlaying": false, "player": "Firefox", "title": "", "artist": ""}},
			want: "[d1] ⏹ Firefox\n",
		},
		{
			name: "sms incoming",
			ev:   events.Event{Type: events.TypeSMSIncoming, DeviceID: "d1", Payload: map[string]any{"sender": "+1555", "body": "hello"}},
			want: "[d1] sms from +1555: hello\n",
		},
		{
			name: "unrendered type falls back to the bare type token",
			ev:   events.Event{Type: events.EventType("something.new"), DeviceID: "d1", Payload: nil},
			want: "[d1] something.new\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatEvent(tc.ev); got != tc.want {
				t.Errorf("formatEvent() =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

func TestFormatEventNewTypes(t *testing.T) {
	tests := []struct {
		name string
		ev   events.Event
		want string
	}{
		{
			name: "battery low threshold omits the charging noise",
			ev:   events.Event{Type: events.TypeBatteryThreshold, DeviceID: "d1", Payload: map[string]any{"charge": 15, "charging": false, "event": 1}},
			want: "[d1] battery low: 15%\n",
		},
		{
			name: "battery full threshold mentions charging when true",
			ev:   events.Event{Type: events.TypeBatteryThreshold, DeviceID: "d1", Payload: map[string]any{"charge": 100, "charging": true, "event": 0}},
			want: "[d1] battery full: 100% (charging)\n",
		},
		{
			name: "sms attachment",
			ev:   events.Event{Type: events.TypeSMSAttachment, DeviceID: "d1", Payload: map[string]any{"filename": "photo.jpg", "path": "/tmp/x", "thread_id": 3}},
			want: "[d1] sms attachment saved: photo.jpg\n",
		},
		{
			name: "ping",
			ev:   events.Event{Type: events.TypePingReceived, DeviceID: "d1", Payload: map[string]any{"message": "pong"}},
			want: "[d1] ping: pong\n",
		},
		{
			name: "telephony ringing names the event in plain language",
			ev:   events.Event{Type: events.TypeTelephonyRinging, DeviceID: "d1", Payload: map[string]any{"contactName": "Bob", "phoneNumber": "+15550001234"}},
			want: "[d1] incoming call: Bob (+15550001234)\n",
		},
		{
			name: "telephony missed falls back to the number",
			ev:   events.Event{Type: events.TypeTelephonyMissed, DeviceID: "d1", Payload: map[string]any{"phoneNumber": "+15550001234"}},
			want: "[d1] missed call: +15550001234\n",
		},
		{
			name: "telephony talking with no caller still names the event",
			ev:   events.Event{Type: events.TypeTelephonyTalking, DeviceID: "d1", Payload: map[string]any{}},
			want: "[d1] call answered\n",
		},
		{
			name: "telephony canceled",
			ev:   events.Event{Type: events.TypeTelephonyCanceled, DeviceID: "d1", Payload: map[string]any{"contactName": "Bob"}},
			want: "[d1] call ended: Bob\n",
		},
		{
			name: "volume keeps the stream name",
			ev:   events.Event{Type: events.TypeVolumeUpdate, DeviceID: "d1", Payload: map[string]any{"name": "Speaker", "volume": 42, "muted": false}},
			want: "[d1] volume: Speaker 42%\n",
		},
		{
			name: "volume muted",
			ev:   events.Event{Type: events.TypeVolumeUpdate, DeviceID: "d1", Payload: map[string]any{"name": "Speaker", "volume": 42, "muted": true}},
			want: "[d1] volume: Speaker (muted)\n",
		},
		{
			name: "volume partial unmute",
			ev:   events.Event{Type: events.TypeVolumeUpdate, DeviceID: "d1", Payload: map[string]any{"name": "Speaker", "muted": false}},
			want: "[d1] volume: Speaker (unmuted)\n",
		},
		{
			name: "volume sink list renders names not Go slice syntax",
			ev: events.Event{Type: events.TypeVolumeUpdate, DeviceID: "d1", Payload: map[string]any{"sinks": []map[string]any{
				{"name": "Speaker", "volume": 42, "muted": false},
				{"name": "Headset", "volume": 80, "muted": true},
			}}},
			want: "[d1] volume: Speaker 42%, Headset (muted)\n",
		},
		{
			name: "volume sink list caps the number shown",
			ev: events.Event{Type: events.TypeVolumeUpdate, DeviceID: "d1", Payload: map[string]any{"sinks": []map[string]any{
				{"name": "a", "volume": 1}, {"name": "b", "volume": 2},
				{"name": "c", "volume": 3}, {"name": "d", "volume": 4},
				{"name": "e", "volume": 5},
			}}},
			want: "[d1] volume: a 1%, b 2%, c 3%, +2 more\n",
		},
		{
			name: "contacts uids phase drops zero counts",
			ev:   events.Event{Type: events.TypeContactsUpdated, DeviceID: "d1", Payload: map[string]any{"phase": "uids", "added": 2, "updated": 0, "deleted": 0, "pending": 5}},
			want: "[d1] contacts: 2 added, 5 pending\n",
		},
		{
			name: "runcommand started",
			ev:   events.Event{Type: events.TypeRunCommandOutput, DeviceID: "d1", Payload: map[string]any{"id": 7, "key": "uptime", "status": "started"}},
			want: "[d1] runcommand uptime: running\n",
		},
		{
			name: "runcommand output batch",
			ev: events.Event{Type: events.TypeRunCommandOutput, DeviceID: "d1", Payload: map[string]any{
				"id": 7, "key": "uptime", "status": "output",
				"stdout": []any{"up 3 days"}, "stderr": []any{"warn: x"},
			}},
			want: "[d1] runcommand uptime: out: up 3 days | err: warn: x\n",
		},
		{
			name: "runcommand finished",
			ev: events.Event{Type: events.TypeRunCommandOutput, DeviceID: "d1", Payload: map[string]any{
				"id": 7, "key": "uptime", "status": "finished", "success": true, "output": "up 3 days",
			}},
			want: "[d1] runcommand uptime: up 3 days\n",
		},
		{
			name: "runcommand finished without output",
			ev: events.Event{Type: events.TypeRunCommandOutput, DeviceID: "d1", Payload: map[string]any{
				"id": 7, "key": "lock", "status": "finished", "success": false, "output": "",
			}},
			want: "[d1] runcommand lock: failed\n",
		},
		{
			name: "contacts vcards phase drops a zero skip count",
			ev:   events.Event{Type: events.TypeContactsUpdated, DeviceID: "d1", Payload: map[string]any{"phase": "vcards", "stored": 4, "skipped": 0}},
			want: "[d1] contacts: 4 saved\n",
		},
		{
			name: "contacts vcards phase keeps a real skip count",
			ev:   events.Event{Type: events.TypeContactsUpdated, DeviceID: "d1", Payload: map[string]any{"phase": "vcards", "stored": 4, "skipped": 1}},
			want: "[d1] contacts: 4 saved, 1 skipped\n",
		},
		{
			name: "contacts with nothing to report",
			ev:   events.Event{Type: events.TypeContactsUpdated, DeviceID: "d1", Payload: map[string]any{"phase": "uids", "added": 0, "updated": 0, "deleted": 0, "pending": 0}},
			want: "[d1] contacts: no changes\n",
		},
		{
			name: "connectivity reuses the dedicated command's formatting",
			ev: events.Event{Type: events.TypeConnectivityUpdate, DeviceID: "d1", Payload: map[string]any{
				"signalStrengths": map[string]any{
					"0": map[string]any{"networkType": "LTE", "signalStrength": 3},
				},
			}},
			want: "[d1] connectivity: SIM 0: LTE      ●●●○\n",
		},
		{
			name: "device added keeps type token first",
			ev:   events.Event{Type: events.TypeDeviceAdded, DeviceID: "d1", Payload: map[string]any{"id": "d1", "name": "Pixel 8", "type": "phone", "state": "UNPAIRED", "connected": false}},
			want: "[d1] device.added: Pixel 8\n",
		},
		{
			name: "device connected keeps type token first",
			ev:   events.Event{Type: events.TypeDeviceConnected, DeviceID: "d1", Payload: map[string]any{"name": "Pixel 8", "type": "phone"}},
			want: "[d1] device.connected: Pixel 8 (phone)\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatEvent(tc.ev); got != tc.want {
				t.Errorf("formatEvent() =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

// state.snapshot is written straight to the socket by the IPC layer with a full
// device+plugin dump. Rendering it would flood the terminal, so it must stay on
// the bare type-token line.
func TestFormatEventStateSnapshotStaysBare(t *testing.T) {
	ev := events.Event{
		Type:     events.TypeStateSnapshot,
		DeviceID: "d1",
		Payload:  map[string]any{"devices": []any{"a", "b", "c"}, "plugins": []any{"Battery", "MPRIS"}},
	}
	got := formatEvent(ev)
	if got != "[d1] state.snapshot\n" {
		t.Errorf("state.snapshot rendered with payload: %q", got)
	}
}

// A malformed or missing payload must degrade to the bare line, never panic and
// never print Go syntax like map[...] or <nil> for a whole struct.
func TestFormatEventSurvivesBadPayloads(t *testing.T) {
	types := []events.EventType{
		events.TypeBatteryUpdate, events.TypeBatteryThreshold, events.TypeNotification,
		events.TypeSftpMount, events.TypeSftpMounted, events.TypeSftpUnmounted,
		events.TypeRunCommandOutput,
		events.TypePairAccepted, events.TypeMprisUpdate,
		events.TypeSMSIncoming, events.TypeSMSAttachment, events.TypePingReceived,
		events.TypeConnectivityUpdate, events.TypeTelephonyRinging, events.TypeTelephonyMissed,
		events.TypeTelephonyTalking, events.TypeTelephonyCanceled, events.TypeVolumeUpdate,
		events.TypeContactsUpdated, events.TypeDeviceAdded, events.TypeDeviceConnected,
	}

	for _, typ := range types {
		t.Run(string(typ), func(t *testing.T) {
			for _, payload := range []any{nil, "a bare string", 42, []any{1, 2}} {
				got := formatEvent(events.Event{Type: typ, DeviceID: "d1", Payload: payload})
				if !strings.HasPrefix(got, "[d1] ") {
					t.Fatalf("payload %#v: line does not start with the device prefix: %q", payload, got)
				}
				if !strings.HasSuffix(got, "\n") {
					t.Fatalf("payload %#v: line is not newline terminated: %q", payload, got)
				}
			}
		})
	}
}

func TestTruncateAndOneLine(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("short string was altered: %q", got)
	}
	if got := truncate("hello world", 8); got != "hello w…" {
		t.Errorf("truncate = %q, want %q", got, "hello w…")
	}
	if got := oneLine("a\n  b\tc  "); got != "a b c" {
		t.Errorf("oneLine = %q, want %q", got, "a b c")
	}
}
