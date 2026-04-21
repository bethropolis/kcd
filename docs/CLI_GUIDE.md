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
- Discovers devices via UDP broadcast and mDNS
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

## devices

List all known devices — both currently connected and remembered from previous sessions.

```
kcd devices [--json]
```

**Flags**

| Flag | Description |
|---|---|
| `--json` | Output as a JSON array |

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

---

## pair

Initiate a pairing request to a discovered device, or accept an incoming request from one.

```
kcd pair <device-id>
```

- If the device has sent a pair request to `kcd` (State: `PairRequestedByPeer`), this command **accepts** it.
- Otherwise, this command **initiates** a new request and waits for the user to accept on the phone.

**Workflow**

```bash
# 1. Find the device ID
kcd devices

# 2. Send pair request
kcd pair a1b2c3d4_e5f6_7890_abcd_ef1234567890

# 3. Accept on the phone when prompted, then confirm:
kcd watch --events=pair.accepted,pair.rejected
```

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

### sftp request

Ask the device for SFTP credentials (host, port, user, password). The result is emitted as an `sftp.mount` event.

```
kcd sftp request <device-id>
```

Capture the credentials:

```bash
kcd watch --json --events=sftp.mount | jq -r 'select(.type=="sftp.mount") | .payload.uri'
```

### sftp mount

Request credentials and immediately mount the phone's filesystem using `sshfs`.

```
kcd sftp mount <device-id>
```

The mount point is printed to stdout. Unmount with `fusermount -u <mountpoint>`.

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
suspend = "systemctl suspend"
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
| `sftp.mount` | SFTP credentials: `{uri, host, port, user, password}` |

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

**Reduce idle CPU to zero**

Once devices are paired, add to `~/.config/kcd/kcd.toml`:

```toml
enable_broadcast = false
```

Restart the daemon. Paired phones reconnect automatically; new devices require manual IP entry.

**Debug connection issues**

```bash
kcd daemon --log-level=debug 2>&1 | grep -E "discovery|transport|pair"
```