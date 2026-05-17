# kcd CLI Reference

`kcd` is both the daemon and the command-line client. Sub-commands that interact with a running daemon communicate over a Unix socket (`/run/user/$UID/kcd/kcd.sock`). The daemon itself is started with `kcd daemon`.

---

## Global Flags

These flags apply to every command:

| Flag | Default | Description |
|---|---|---|
| `--config <path>` | `~/.config/kcd/kcd.toml` | Path to config file |
| `--log-level <level>` | `info` | Log verbosity: `debug`, `info`, `warn`, `error`, `quiet` |
| `--help`, `-h` | — | Show help |
| `--version`, `-v` | — | Show version, commit, and build date |

`--config` and `--log-level` can also be set via environment variables `KCD_CONFIG` and `KCD_LOG_LEVEL`.

---

## daemon

Start the `kcd` background daemon. This is the only command that does not connect to a running daemon — it *is* the daemon.

```
kcd daemon [--config <path>] [--log-level <level>]
```

The daemon:
- Listens for device announcements via UDP and mDNS (always on)
- Broadcasts its own identity only during `kcd pair` (listen mode)
- Accepts inbound TCP connections on port 1716
- Runs all enabled plugins
- Opens the IPC Unix socket for CLI clients
- Sends SIGINT or SIGTERM to stop cleanly

**Running as a systemd user service (recommended)**

```bash
cp packaging/kcd-user.service ~/.config/systemd/user/kcd.service
systemctl --user daemon-reload
systemctl --user enable --now kcd
```

Check status:
```bash
systemctl --user status kcd
journalctl --user -u kcd -f
```

---

## doctor

Check runtime dependencies and print a coloured pass/fail table.
Exits with code 1 if any check fails. Works even when the daemon
is not running (the daemon-running check will show as failed).

    kcd doctor

Checks performed:
- Daemon reachable via IPC socket
- `notify-send` installed (desktop notifications)
- `wl-copy` or `xclip` installed (clipboard sync)
- `sshfs` installed (SFTP mount)
- `ydotool` or `xdotool` installed (mousepad)
- Port 1716/UDP available
- Port 1716/TCP available
- Config file readable
- TLS cert file exists

---

## status

Show daemon runtime information.

    kcd status [--json]

**Flags**

| Flag | Description |
|---|---|
| `--json` | Output raw JSON |

**Example output**

    kcd v1.0.5 — up 3h 12m
    Socket:    /run/user/1000/kcd/kcd.sock
    Config:    /home/user/.config/kcd/kcd.toml
    Devices:   2 known, 1 connected
    Plugins:   Battery, Clipboard, Notification, Share, ...

---

## devices

List all known devices — both currently connected and remembered from previous sessions.

```
kcd devices [--json]
```

**Flags**

| Flag | Description |
|---|---|
| `--json` | Output as a JSON array |
| `--watch`, `-w` | Stream device changes live (clears screen on each change) |

**Example output**

```
DEVICE ID                            NAME              TYPE       STATE      CONNECTED
---------------------------------------------------------------------------------------------------
a1b2c3d4_e5f6_7890_abcd_ef1234567890 Pixel 8 Pro       phone      Paired     true
b9e1f234_0000_1111_2222_333344445555 Galaxy Tab S9     tablet     Unpaired   false
```

**JSON output**

```bash
kcd devices --json | jq '.[0].ID'
```

```json
[
  {
    "ID": "a1b2c3d4_e5f6_7890_abcd_ef1234567890",
    "Name": "Pixel 8 Pro",
    "Type": "phone",
    "State": "Paired",
    "Connected": true
  }
]
```

**Example — live mode**

    kcd devices --watch

---

## connect

Manually connect to a device by IP address. Use this when UDP broadcast and mDNS are blocked (corporate Wi-Fi, Docker, university networks).

```
kcd connect <ip-address>
```

**Example**

```bash
kcd connect 192.168.1.50
```

> On the Android side, open KDE Connect → ⋮ → "Add device by IP address" and enter your PC's IP if the phone isn't finding the PC either.

> **Auto-reconnect:** paired devices reconnect automatically after a
> connection drop. The daemon dials the last known IP with exponential
> backoff up to 5 minutes. To stop reconnection, unpair the device.

---

## pair

Pair with a device. Two modes depending on whether you provide a device ID.

### Pair to a specific device

```bash
kcd pair <device-id>
```

If the device has already sent a pair request to `kcd` (state `PairRequestedByPeer`), this accepts it. Otherwise, it sends a new pair request — accept on your phone.

### Listen mode (headless / server)

```bash
kcd pair
```

No arguments = listen mode. Broadcast is started automatically so the phone can discover the PC. The CLI waits for any incoming pair request and accepts it immediately, printing the verification code:

```
Listening for pair requests… (Ctrl+C to cancel)
Paired with Pixel 8 Pro (a1b2c3d4...)
Verification code: 3a8f
```

Broadcast stops when pairing completes or you press Ctrl+C.

---

## unpair

Revoke trust and remove a device from the paired list.

```
kcd unpair <device-id>
```

This sends a rejection packet to the device and removes it from the local device registry.

---

## ping

Send a ping notification to a device. The phone displays a "Ping!" notification.

```
kcd ping <device-id>
```

---

## battery

Fetch the current battery level and charging state of a device.

```
kcd battery <device-id>
```

**Example output**

```
Battery: 74% (charging)
Battery: 31% (discharging)
```

> For continuous monitoring, use `kcd watch --events=battery.update` instead.

---

## clipboard

Push the local clipboard content to a device.

```
kcd clipboard [device-id]
```

If `device-id` is omitted, `kcd` automatically targets the first connected device.

**Clipboard backend detection**

- Wayland (`$WAYLAND_DISPLAY` set): reads via `wl-paste`, writes via `wl-copy`
- X11: reads/writes via `xclip`

**Example — Sway keybinding**

```bash
bindsym Super+c exec kcd clipboard
```

---

## share

Send a local file to a device.

```
kcd share <device-id> <file-path>
```

The file is transferred over a dedicated TCP side-channel (ports 1739–1764) and saved to the phone's Downloads folder.

**Example**

```bash
kcd share a1b2c3d4_e5f6_7890_abcd_ef1234567890 ~/Documents/report.pdf
```

Watch transfer progress:

```bash
kcd watch --events=share.progress,share.complete
```

---

## reply

Send a text reply to a notification that supports it (e.g. a messaging app notification).

```
kcd reply <device-id> <reply-id> <message>
```

The `reply-id` is included in the `notification` event payload. Capture it with `kcd watch --json`:

```bash
kcd watch --json | jq 'select(.type=="notification") | {id: .payload.id, app: .payload.appName}'
```

**Example**

```bash
kcd reply a1b2... abc-123 "On my way!"
```

---

## call

Manage phone calls.

### call mute

Silence the ringtone for an incoming call without answering.

```
kcd call mute <device-id>
```

---

## findmyphone

Make the phone play a loud ringtone to help locate it.

```
kcd findmyphone <device-id>
```

---

## lock / unlock

Lock or unlock the current desktop session.

```
kcd lock   <device-id>
kcd unlock <device-id>
```

Uses `loginctl lock-session` / `loginctl unlock-session` under the hood.

---

## sftp

Manage SFTP access to the phone's filesystem.

The phone's SFTP server may expose multiple storage volumes (e.g. "Internal shared storage" and "SD card"). These are reported via `multiPaths` and `pathNames` in the KDE Connect protocol.

### sftp request

Ask the device for SFTP credentials (host, port, user, password). The result is emitted as an `sftp.mount` event.

```
kcd sftp request <device-id>
```

Capture the credentials:

```bash
kcd watch --json --events=sftp.mount | jq -r 'select(.type=="sftp.mount") | .payload.uri'
```

### sftp info

Show cached SFTP connection details for a paired device, including available storage volumes:

```
kcd sftp info <device-id>
```

**Example output**

```
Device: a1b2c3d4_e5f6_7890_abcd_ef1234567890 (Pixel 8 Pro)
IP:     192.168.1.50
Port:   8022
User:   sftp-user
Password: ********

Volumes:
  1. Internal shared storage  →  /storage/emulated/0
  2. SD card                  →  /storage/ABCD-1234
```

If the phone returned an error (e.g. storage permission not granted), the `errorMessage` field is shown instead.

### sftp volumes

List available storage volumes without the full info output:

```
kcd sftp volumes <device-id>
```

**Example output**

```
Internal shared storage  →  /storage/emulated/0
SD card                  →  /storage/ABCD-1234
```

> The phone must have an active SFTP session (run `kcd sftp request` first if
> the volumes list is empty).

### sftp mount

Request credentials and immediately mount the phone's filesystem using `sshfs`.

```
kcd sftp mount <device-id>
```

The mount point is printed to stdout. Unmount with `fusermount -u <mountpoint>`.

### sftp unmount

Cleanly unmount a previously mounted phone filesystem.

    kcd sftp unmount <device-id>

Calls `fusermount3` (or `fusermount` on older systems) and removes the
temporary mount point directory. Returns an error if the device was never
mounted in this daemon session.

---

## run

Execute and manage remote commands configured on the phone's KDE Connect app, or expose local shell commands to the phone via the `[commands]` config table.

### run list

Request the list of commands available on the remote device.

```
kcd run list <device-id>
```

Results arrive as a `runcommand.list` event; watch for them:

```bash
kcd watch --json | jq 'select(.type=="runcommand.list")'
```

### run exec

Execute one of the local commands registered in `[commands]` config, triggered from the phone, or send a command execution request to the phone.

```
kcd run exec <device-id> <command-key>
```

**Example config (`kcd.toml`)**

```toml
[commands]
uptime  = "uptime"
lock    = "loginctl lock-session"
suspend  = "systemctl suspend"
```

```bash
kcd run exec a1b2... uptime
```

---

## sms

Send an SMS via a connected phone.

```
kcd sms <device-id> <phone-number> <message>
```

**Example**

```bash
kcd sms a1b2... +1555000111 "Heading home in 10"
```

> Incoming SMS threads are not yet supported. Only sending is implemented.

---

## watch

Monitor real-time events from the daemon as an NDJSON stream. This is the primary way to observe what is happening across all devices.

For the full list of event types and their payload fields, see
[README.md § Events](README.md#events).

```
kcd watch [--events <type,...>] [--json]
```

**Flags**

| Flag | Description |
|---|---|
| `--events`, `-e` | Comma-separated list of event types to filter (default: all) |
| `--json` | Output raw NDJSON instead of human-readable text |

`kcd watch` reconnects automatically if the daemon restarts, with exponential backoff up to 30 seconds.

### Available event types

| Event type | Description |
|---|---|
| `device.added` | A new device was seen for the first time |
| `device.removed` | A device was unpaired and removed |
| `device.connected` | A device established a TCP connection |
| `device.disconnected` | A device's connection dropped |
| `pair.requested` | The phone sent a pairing request |
| `pair.accepted` | Pairing was accepted on both sides |
| `pair.rejected` | Pairing was denied or cancelled |
| `battery.update` | New battery reading: `{charge, charging}` |
| `notification` | Notification from the phone: `{appName, title, text, id, ...}` |
| `notification.canceled` | The phone dismissed a notification: `{id}` |
| `share.progress` | File transfer progress: `{file, current, total}` |
| `share.complete` | File transfer finished: `{file, path}` |
| `share.text` | Plain text received: `{text}` |
| `share.url` | URL received: `{url}` |
| `ping.received` | A ping arrived |
| `telephony.ringing` | Incoming call: `{contactName, phoneNumber}` |
| `telephony.missed` | Missed call: `{contactName, phoneNumber}` |
| `telephony.canceled` | Call ended |
| `connectivity.update` | Signal strength: `{signal, networkType}` |
| `sftp.mount` | SFTP credentials: `{uri, ip, port, user, password, path, multiPaths, pathNames, errorMessage}` |

### Examples

**Watch everything, human-readable**

```bash
kcd watch
```

```
[a1b2...] battery: 62% (charging: false)
[a1b2...] notification: WhatsApp - Alice: "Hey, are you free?"
[a1b2...] telephony.ringing — Bob (+15550001234)
```

**Filter to battery and calls only**

```bash
kcd watch --events=battery.update,telephony.ringing
```

**Raw NDJSON for scripting**

```bash
kcd watch --json
```

```json
{"type":"battery.update","timestamp":"2025-04-21T10:22:01Z","deviceId":"a1b2...","payload":{"charge":62,"charging":false}}
{"type":"notification","timestamp":"2025-04-21T10:22:45Z","deviceId":"a1b2...","payload":{"appName":"WhatsApp","title":"Alice","text":"Hey, are you free?","id":"notif-abc"}}
```

**Waybar phone battery integration**

```bash
kcd watch --json | jq -r '
  select(.type=="battery.update") |
  "\(.payload.charge)%" + (if .payload.charging then " " else "" end)
'
```

**Desktop notification on incoming call**

```bash
kcd watch --json --events=telephony.ringing | \
  jq -r '.payload | "Call from \(.contactName // .phoneNumber)"' | \
  xargs -I{} notify-send "📞 Incoming Call" "{}"
```

**Auto-mute calls when screen is locked**

```bash
kcd watch --json --events=telephony.ringing | while read -r ev; do
    id=$(echo "$ev" | jq -r '.deviceId')
    if loginctl show-session self -p LockedHint | grep -q yes; then
        kcd call mute "$id"
    fi
done
```

---

## Tips

**Get the first connected device ID**

```bash
kcd devices --json | jq -r '[.[] | select(.Connected)] | .[0].ID'
```

**Send a file from a Nautilus script**

Install [`packaging/nautilus-kcd.py`](../packaging/nautilus-kcd.py) for right-click → "Send to phone" in GNOME Files.

**Debug connection issues**

```bash
kcd daemon --log-level=debug 2>&1 | grep -E "discovery|transport|pair"
```

> Broadcast is off by default (only active during `kcd pair` listen mode).
> The daemon sits at 0.0% idle CPU — no config change needed.