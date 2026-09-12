# IPC Protocol Reference

kcd exposes a Unix socket for inter-process communication. This document is the
authoritative reference for client authors who want to build tools against the
daemon without importing Go packages.

---

## 1. Transport

- **Socket path:** `$XDG_RUNTIME_DIR/kcd/kcd.sock` (typically
  `/run/user/<uid>/kcd/kcd.sock`)
- **Type:** Unix stream socket (`SOCK_STREAM`)
- **Framing:** Newline-delimited JSON (NDJSON). Every message is a single JSON
  object terminated by `\n`. The daemon reads one line per request from its
  `bufio.Scanner`.
- **Max request payload:** No explicit limit (scanner reads a single line; Unix
  socket buffer is the practical bound). Do not send payloads exceeding ~1 MiB
  over IPC; use the side-channel TLS port for file transfers instead.

---

## 2. Request / Response Format

### Request

```json
{"cmd": "<command>", "payload": <optional JSON value>}
```

- `cmd` (`string`, required) — the command name.
- `payload` (`JSON`, optional) — per-command parameters. Parsed as
  `json.RawMessage` by the handler. If absent, the field must be omitted or
  `null`.

### Response (non-watch commands)

```json
{"ok": true, "data": <optional JSON>}
{"ok": false, "error": "human-readable message"}
```

- `ok` (`bool`) — success.
- `error` (`string`, present only when `ok` is `false`) — error description.
- `data` (`JSON`, present only when `ok` is `true` and the command has a
  payload) — response payload. Refer to the per-command reference for its shape.

### Error responses

All errors return the same shape. The `error` string is human-readable and may
change between releases. Do not parse it programmatically beyond logging.

```json
{"ok": false, "error": "device not found"}
```

---

## 3. Command Reference

Every command, its request payload, and its response data shape.

### 3.1 Built-in Commands (handler.go)

#### `devices`

List all known devices (paired + unpaired).

**Request payload:** none

**Response data:** `[]DeviceInfo`

```json
{
  "ok": true,
  "data": [
    {
      "id": "a1b2c3d4e5f6_...",
      "name": "Pixel 9",
      "type": "phone",
      "state": "PAIRED",
      "cert_fp": "",
      "last_seen": "0001-01-01T00:00:00Z",
      "connected": true
    }
  ]
}
```

Fields:

| Field | Type | Description |
|---|---|---|
| `id` | string | Permanent device identifier |
| `name` | string | Human-readable device name |
| `type` | string | `"phone"`, `"tablet"`, `"laptop"`, `"desktop"` |
| `state` | string | `"UNPAIRED"`, `"PAIR_REQUESTED"`, `"PAIR_REQUESTED_BY_PEER"`, `"PAIRED"` (`"UNKNOWN"` may appear in state files written by older versions and means unpaired) |
| `cert_fp` | string | Not populated in this response (empty) |
| `last_seen` | string (RFC3339) | Not populated in this response (zero time) |
| `connected` | bool | Whether the device currently has an active TCP connection. Note: `connected: true` alone does **not** mean usable — a stranger on the LAN can hold a raw connection while `state` is `UNPAIRED`. Clients must check `state == "PAIRED"` before sending commands or auto-selecting a device. |

#### `pair`

Initiate pairing with a device, or respond to an incoming pairing request.

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_..."}
```

Optional fields:

```json
{"deviceId": "a1b2c3d4e5f6_...", "accept": true, "reject": false}
```

- If neither `accept` nor `reject` is set, sends a pair request to the device.
- If `accept: true`, accepts an incoming pair request from the device.
- If `reject: true`, rejects or unpairs.
- If the device has no active connection, the daemon dials it on demand
  using its last-seen discovery address (background auto-dial no longer
  connects to unpaired devices), then sends the pair request.

**Response data:** none (`{"ok": true}`)

#### `pair_listen`

Wait for an incoming pairing request and return the verification key.

**Request payload:** none

**Response data:** `PairListenResult`

```json
{
  "ok": true,
  "data": {
    "deviceId": "a1b2c3d4e5f6_...",
    "deviceName": "Pixel 9",
    "verificationKey": "ABCD1234EFGH5678"
  }
}
```

The handler blocks until a pair request arrives or the context is cancelled.
The 16-character verification key should be displayed to the user to verify
identity match on both sides.

#### `unpair`

Remove a paired device.

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_..."}
```

**Response data:** none

#### `ping`

Send a ping packet to a device.

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_..."}
```

**Response data:** none

### 3.2 Plugin Commands (registered via `handler.Register`)

#### `connect`

Connect to a device by IP address. Used when LAN broadcast is unavailable or
the device is on a different subnet.

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_...", "ip": "192.168.1.42"}
```

**Response data:** none

#### `broadcast_start` / `broadcast_stop`

Disable/enable LAN UDP broadcasting at runtime.

**Request payload:** none

**Response data:** none

#### `status`

Return daemon status info.

**Request payload:** none

**Response data:** `StatusResponse`

```json
{
  "ok": true,
  "data": {
    "version": "1.13.0",
    "startedAt": "2026-05-27T10:00:00Z",
    "uptimeHuman": "2h34m",
    "socketPath": "/run/user/1000/kcd/kcd.sock",
    "configPath": "/home/user/.config/kcd/kcd.toml",
    "plugins": ["pair", "ping", "battery", "share", "sftp", "clipboard", "mpris", "notification", "sms", "telephony", "connectivity", "systemvolume", "mousepad", "lockdevice", "findmyphone", "runcommand"],
    "deviceCount": 3,
    "connectedCount": 1
  }
}
```

#### `battery`

Request battery state from a device.

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_..."}
```

**Response data:** `{"charge": 85, "charging": true}` (the daemon waits for the
device to respond and returns the value).

| Field | Type | Description |
|---|---|---|
| `charge` | number | Battery percentage (0–100) |
| `charging` | bool | Whether the device is currently charging |

#### `clipboard_push`

Push the local clipboard content to a device.

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_..."}
```

The daemon reads the local clipboard via `wl-paste` or `xclip` and sends it.

**Response data:** none

#### `share`

Send a file to a device.

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_...", "file": "/path/to/file.pdf"}
```

**Response data:** none

The daemon opens a side-channel TLS port for the actual file transfer.

#### `send_sms`

Send an SMS via a paired phone.

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_...", "phoneNumber": "+1234567890", "message": "Hello!"}
```

**Response data:** none

#### `sms_request_conversations`

Request a list of SMS conversations from a device.

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_..."}
```

**Response data:** none (results arrive as `sms.incoming` events if the phone
uses the deprecated event-based protocol) or via the conversation response
packet (handled internally). For client authors: subscribe to `notification`
or watch the event stream for the reply.

#### `sms_request_conversation`

Request messages from a specific conversation thread.

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_...", "threadID": 42}
```

Optional fields: `rangeStartTimestamp` (int64), `numberToRequest` (int64).

**Response data:** none

#### `sms_request_attachment`

Request an MMS attachment file from a device.

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_...", "threadID": 42, "partID": 1, "uniqueIdentifier": "..."}
```

**Response data:** none (attachment arrives via side-channel transfer, emitted
as `sms.attachment` event).

#### `call_mute`

Mute an incoming phone call.

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_..."}
```

**Response data:** none

#### `notify_reply`

Reply to a notification that supports inline replies.

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_...", "replyId": "notification-id-here", "message": "OK, I'll be there"}
```

The `replyId` comes from the `requestReplyId` field of a `notification` event.

**Response data:** none

#### `findmyphone` (also aliased as `ring`)

Make a paired phone ring loudly.

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_..."}
```

**Response data:** none

#### `lock`

Lock a paired device's screen.

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_..."}
```

**Response data:** none

#### `unlock`

Unlock a paired device's screen (if the device supports it).

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_..."}
```

**Response data:** none

#### `run_list`

Request a device's list of configured run commands.

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_..."}
```

**Response data:** none (results arrive via `kdeconnect.runcommand` response
packet).

#### `run_exec`

Execute a command on a device by its command key.

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_...", "key": "my_command_key"}
```

**Response data:** none

#### `sftp_info`

Get SFTP connection details for a device.

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_..."}
```

**Response data:** `SftpInfo`

```json
{
  "ok": true,
  "data": {
    "ip": "192.168.1.42",
    "port": "31588",
    "user": "u0_a123",
    "password": "sftp-password-here",
    "path": "/storage/emulated/0",
    "volumes": [
      {"name": "Internal shared storage", "path": "/storage/emulated/0"}
    ]
  }
}
```

#### `sftp_volumes`

List storage volumes available on a device.

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_..."}
```

**Response data:** `[]StorageVolume` (array of `{"name": "...", "path": "..."}`)

#### `sftp_mount`

Mount a device's storage via SFTP (requires `sshfs`).

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_..."}
```

**Response data:** none

#### `sftp_mount_local`

Mount a device's storage at a local temporary path.

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_..."}
```

**Response data:** `{"path": "/tmp/kcd-sftp-abcdef123456"}`

#### `sftp_unmount`

Unmount a previously mounted SFTP filesystem.

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_..."}
```

**Response data:** none

#### `sftp_browse`

Request fresh SFTP credentials and either list available storage volumes or
mount a specific one.

**Request payload:**

- Without volume (list mode): `{"deviceId": "a1b2c3d4e5f6_..."}`
- With volume (mount mode): `{"deviceId": "a1b2c3d4e5f6_...", "volume": "/storage/emulated/0"}`

The `volume` field accepts an index (0-based), volume name, or path. The
daemon resolves it against the device's reported volumes after receiving
fresh credentials.

**Response data:** `SftpBrowseResponse`

```json
{
  "ok": true,
  "data": {
    "path": "/home/user/Downloads/kcd/mnt/kcd-sftp-a1b2c3d4",
    "volumes": [
      {"name": "Internal shared storage", "path": "/storage/emulated/0"},
      {"name": "SD card", "path": "/storage/ABCD-1234"}
    ]
  }
}
```

In list mode (`volume` omitted or empty), `path` is empty and `volumes` is
populated. In mount mode, `path` contains the local sshfs mount point.

**Error cases:**

- `"device not found"` — the device ID is unknown or was removed
- `"sftp plugin not enabled"` — SFTP is disabled in config
- `"timed out after 20s waiting for SFTP response"` — phone did not respond

#### `mpris_status`

Get MPRIS watcher status (which local players are tracked).

**Request payload:** none

**Response data:** `MprisDebugStatus`

```json
{
  "ok": true,
  "data": {
    "watcherRunning": true,
    "deviceCount": 2,
    "players": ["spotify", "firefox"],
    "playerMappings": {
      "a1b2c3d4e5f6_...": "spotify"
    }
  }
}
```

#### `mpris_action`

Send an MPRIS control action to a device.

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_...", "action": "play"}
```

Supported actions: `"play"`, `"pause"`, `"playpause"`, `"next"`, `"previous"`,
`"stop"`, `"raise"`, `"quit"`. Volume can be set with `"setVolume"` (requires
an integer value field). Seek with `"seek"` (int64, offset in ms) or
`"setPosition"` (int64, absolute position in ms).

**Response data:** none

#### `remote_volume_list`

List the last known audio sinks for a device.

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_..."}
```

**Response data:** `[]SinkInfo`

```json
{
  "ok": true,
  "data": [
    {"name": "media", "description": "Media", "volume": 75, "muted": false, "maxVolume": 100}
  ]
}
```

Returns an empty array if the plugin has not yet received a sink list from the device (connect to the device first).

**Error cases:**
- `"device not found"` — the device ID is unknown or was removed
- `"remotesystemvolume plugin not enabled"` — plugin disabled in config

#### `remote_volume_set`

Set the volume of a specific audio sink on a remote device (0–100).

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_...", "name": "media", "volume": 50}
```

**Response data:** none

#### `remote_volume_mute`

Mute or unmute a specific audio sink on a remote device.

**Request payload:**

```json
{"deviceId": "a1b2c3d4e5f6_...", "name": "media", "muted": true}
```

**Response data:** none

#### `mpris_remote`

List remote MPRIS players (players on paired devices).

**Request payload:** none

**Response data:** `MprisRemoteResponse`

```json
{
  "ok": true,
  "data": {
    "players": [
      {"deviceId": "a1b2c3d4e5f6_...", "player": "spotify"}
    ]
  }
}
```

---

## 4. Watch Protocol

The `watch` command establishes a persistent connection that streams live
events as NDJSON. Unlike all other commands, the connection stays open.

### Request

```json
{"cmd": "watch", "payload": {"events": ["battery.update", "notification"]}}
```

The `payload.events` array is optional. If omitted, **all** event types are
streamed. If present, only events whose type string matches one of the entries
are delivered.

### Initial Sequence

1. **Ack line:** The daemon sends one `{"ok": true}` immediately upon
   accepting the watch request. The client must read (and discard) this line
   before processing events.

2. **State dump:** For each **connected** device, in arbitrary order:

   **2a. `device.connected`:**
   ```json
   {"type":"device.connected","deviceId":"...","timestamp":"2026-05-27T10:00:00Z","payload":{"id":"...","name":"Pixel 9","type":"phone"}}
   ```

   **2b. `battery.update`:**
   ```json
   {"type":"battery.update","deviceId":"...","timestamp":"...","payload":{"charge":85,"charging":true}}
   ```

   **2c. `mpris.update`** (only if MPRIS plugin is registered AND the cached
       state is less than 10 seconds old):
   ```json
   {"type":"mpris.update","deviceId":"...","timestamp":"...","payload":{...NowPlaying...}}
   ```

   The daemon keeps now-playing state fresh by re-requesting it every 5
   seconds from devices with an **actively-playing** player (see the
   `mpris.update` section below), so this initial dump fires reliably for
   mid-track state — a pure-push client can mount and see the current track
   without polling. Stopped/paused players are deliberately not polled, so
   they age past the 10-second gate and no ghost track is shown after a
   reconnect.

3. **Live stream:** All matching events are streamed as they occur, one per
   line, until the client disconnects or the daemon shuts down.

### Event Wire Format

```json
{
  "type": "battery.update",
  "timestamp": "2026-05-27T10:00:00Z",
  "deviceId": "a1b2c3d4e5f6_...",
  "payload": { ... }
}
```

| Field | Type | Description |
|---|---|---|
| `type` | string | Event type identifier |
| `timestamp` | string (RFC3339) | UTC time when the event was published |
| `deviceId` | string | The device that triggered the event (empty for daemon-level events) |
| `payload` | JSON | Per-type payload, see section 4.2 |

### Event Filters

The `events` filter is applied on the server side. Only events whose type
string exactly matches one of the filters are delivered. Invalid/unknown
filter strings are silently ignored — they simply match nothing.

### Reconnection

The official `kcd watch` CLI client implements automatic reconnection with
exponential backoff (1 second initial, 30 second maximum, randomized jitter).
Client authors are encouraged to adopt a similar strategy.

---

## 5. Event Types

### 5.1 Device Events

#### `device.added`

A new device was discovered on the network.

**Payload:** `string` (the device name)

#### `device.removed`

A device disappeared from the network (or was manually removed).

**Payload:** none (`null`)

#### `device.connected`

A TCP connection was established with a device.

**Payload:**

```json
{"id": "...", "name": "Pixel 9", "type": "phone"}
```

#### `device.disconnected`

A TCP connection was lost.

**Payload:** none (`null`)

### 5.2 Pairing Events

#### `pair.requested`

A remote device is requesting pairing.

**Payload:**

```json
{"name": "Pixel 9", "type": "phone", "verificationKey": "ABCD1234EFGH5678"}
```

The 16-character verification key should be displayed to the user to confirm
the same key is shown on the remote device.

#### `pair.accepted`

A pairing was accepted.

**Payload:**

```json
{"name": "Pixel 9", "type": "phone"}
```

#### `pair.rejected`

A pairing request was rejected, or a device was unpaired.

**Payload:**

```json
{"name": "Pixel 9", "type": "phone"}
```

### 5.3 Battery Events

#### `battery.update`

Battery state changed or was requested.

**Payload:**

```json
{"charge": 85, "charging": true}
```

| Field | Type | Description |
|---|---|---|
| `charge` | number | Battery percentage (0–100) |
| `charging` | bool | Whether the device is currently charging |

#### `battery.threshold`

A battery threshold event was received from the device (e.g. low battery
warning).

**Payload:**

```json
{"charge": 15, "charging": false, "event": 1}
```

### 5.4 Notification Events

#### `notification`

A notification was received from a device.

**Payload:**

```json
{
  "appName": "WhatsApp",
  "title": "John Doe",
  "text": "See you at 5",
  "requestReplyId": "reply-123",
  "id": "notif-456"
}
```

| Field | Type | Description |
|---|---|---|
| `appName` | string | Application name (Android `appName` field) |
| `title` | string | Notification title (Android `title` field) |
| `text` | string | Notification body text (Android `ticker` field) |
| `requestReplyId` | string | Present if the notification supports inline replies |
| `id` | string | Notification identifier |

> **Desktop popups:** the daemon shows each phone notification via `notify-send`.
> Popups render **without an icon by default** (`show_icons = false`); set
> `show_icons = true` to display the phone's app icon (downloaded via
> `fetch_icons` and reused across re-posts). Because Android re-posts a
> notification on every update with a stable `id` (e.g. a scrobbler's
> now-playing notification), the daemon replaces the existing desktop popup
> in place (`--replace-id`) so repeated updates collapse to one popup instead
> of flooding the screen — mirroring the reference desktop's
> `Notification::update()`. Disable with `replace_notifications = false`.
>
> When the phone **cancels** a notification (e.g. a scrobbler's now-playing
> popup torn down on pause), the daemon defers closing the desktop popup by
> `cancel_grace_ms` (default `1500`). If the same notification id is re-posted
> within that window (rapid play/pause toggling), the popup is updated in
> place instead of flickering closed and open. A real dismissal — no re-post —
> closes the popup after the grace window. Set `cancel_grace_ms = 0` to close
> immediately on cancel. The `notification.canceled` event is always emitted
> immediately on the cancel packet, regardless of the grace window.

#### `notification.canceled`

A notification was dismissed by the device.

**Payload:**

```json
{"id": "notif-456"}
```

### 5.5 Share Events

#### `share.progress`

A file transfer is in progress.

**Payload:**

```json
{"file": "photo.jpg", "current": 524288, "total": 2097152}
```

| Field | Type | Description |
|---|---|---|
| `file` | string | Filename being transferred |
| `current` | number | Bytes received so far |
| `total` | number | Total file size in bytes |

#### `share.complete`

A file transfer completed (or failed).

**Payload (success):**

```json
{"file": "photo.jpg", "success": true}
```

**Payload (failure):**

```json
{"file": "photo.jpg", "success": false, "error": "permission denied"}
```

#### `share.text`

Text was shared from the device.

**Payload:**

```json
{"text": "Hello, check this out!"}
```

#### `share.url`

A URL was shared from the device.

**Payload:**

```json
{"url": "https://example.com"}
```

### 5.6 Ping Events

#### `ping.received`

A ping was received from a device.

**Payload:**

```json
{"message": "Ping!"}
```

### 5.7 Telephony Events

#### `telephony.ringing`

An incoming call is ringing.

**Payload:**

```json
{"event": "ringing", "contactName": "John Doe", "phoneNumber": "+1234567890", "isCancel": false}
```

#### `telephony.talking`

A call is in progress.

**Payload:** same shape as `telephony.ringing` with `"event": "talking"`.

#### `telephony.missed`

A call was missed.

**Payload:** same shape with `"event": "missed"`.

#### `telephony.canceled`

A call was cancelled.

**Payload:** full `TelephonyBody` struct (includes `event`, `contactName`,
`phoneNumber`).

### 5.8 Connectivity Events

#### `connectivity.update`

Cellular/Wi-Fi signal strength report from the device.

**Payload:**

```json
{
  "signalStrengths": {
    "wlan0": {
      "networkType": "Wi-Fi",
      "networkDetailedType": "Wi-Fi",
      "signalStrength": 4
    },
    "rmnet0": {
      "networkType": "mobile",
      "networkDetailedType": "LTE",
      "signalStrength": 3
    }
  }
}
```

### 5.9 SFTP Events

#### `sftp.mount`

SFTP credentials received (success) or error.

**Payload (success):**

```json
{
  "uri": "sftp://192.168.1.42:31588",
  "ip": "192.168.1.42",
  "port": "31588",
  "user": "u0_a123",
  "password": "sftp-password-here",
  "path": "/storage/emulated/0",
  "volumes": [{"name": "Internal shared storage", "path": "/storage/emulated/0"}]
}
```

**Payload (error):**

```json
{"error": "SFTP server rejected credentials"}
```

### 5.10 Volume Events

#### `volume.update`

Device volume level changed (sent in two shapes).

**Payload (per-sink update):**

```json
{"name": "media", "volume": 70, "muted": false}
```

| Field | Type | Description |
|---|---|---|
| `name` | string | Audio stream name |
| `volume` | number | Volume level (0–100) |
| `muted` | bool | Whether the stream is muted |

**Payload (full sink list):**

```json
{
  "sinks": [
    {"name": "media", "description": "Media", "volume": 75, "muted": false, "maxVolume": 100}
  ]
}
```

### 5.11 SMS Events

#### `sms.incoming`

An SMS or MMS message was received.

**Payload:**

```json
{
  "body": "Hello!",
  "sender": "+1234567890",
  "date": 1716800000000,
  "type": 1,
  "thread_id": 42,
  "read": false,
  "event": 0,
  "u_id": 98765,
  "sub_id": 0,
  "addresses": [{"address": "+1234567890"}],
  "attachments": [{"part_id": 1, "mime_type": "image/jpeg", "unique_identifier": "..."}]
}
```

#### `sms.attachment`

An MMS attachment has been downloaded.

**Payload:**

```json
{"filename": "image.jpg", "path": "/tmp/kcd-sms-attachment-...", "thread_id": 42}
```

### 5.12 Ring Events

#### `ring.received`

A ring/find-my-phone request was received from a device (the phone is asking
this daemon to ring).

**Payload:** none (`null`)

### 5.13 MPRIS Events

#### `mpris.update`

Now-playing state from a device's media player.

> **Album art:** when the phone advertises a `kdeconnect:/artUri?...` URI,
> the daemon requests the art bytes over a side channel and caches them to
> `$XDG_CACHE_HOME/kcd/art/<kdeArtHash>.<ext>`. Once fetched, `albumArtUrl`
> is emitted as a loadable `file://` path. A second `mpris.update` event is
> published when the art arrives. If the fetch is still in flight or fails,
> the original URI is left untouched — clients should fall back to a
> placeholder.

> **Freshness:** the daemon re-requests now-playing from every connected
> device with an **actively-playing** player every 5 seconds
> (`kdeconnect.mpris.request` with `requestNowPlaying: true`). Responses are
> deduplicated — an event is only emitted when the state actually changes.
> This keeps `pos`/state current for pure-push clients (widgets, Waybar)
> that never poll the CLI. Devices that haven't reported a player yet, or
> whose player is stopped/paused, are not polled — a stopped-but-alive
> player keeps its last pushed track (so it can be resumed), but stops being
> refreshed and ages out of the 10-second initial-dump gate.

> **Session teardown:** when the phone reports a `playerList` that no longer
> contains the device's currently-tracked player (the media session was
> destroyed, e.g. the app was swiped away), the daemon drops the cached
> state and publishes an empty `mpris.update` (`{...NowPlaying...}` with no
> title) so watchers fall back to "no media playing". An empty `playerList`
> (all sessions destroyed) has the same effect. A stopped-but-alive session
> is **not** cleared — only sessions removed from `playerList` are.

**Payload:**

```json
{
  "player": "spotify",
  "title": "Song Title",
  "artist": "Artist Name",
  "album": "Album Name",
  "albumArtUrl": "file:///home/user/.cache/kcd/art/1141556203.jpg",
  "url": "spotify:track:...",
  "length": 240000,
  "pos": 45000,
  "isPlaying": true,
  "volume": 80,
  "canControl": true,
  "canGoNext": true,
  "canGoPrevious": true,
  "canPause": true,
  "canPlay": true,
  "canSeek": true,
  "playbackStatus": "Playing",
  "shuffle": false,
  "loopStatus": "None"
}
```

The extension is sniffed from the received bytes (JPEG/PNG/GIF/WebP),
defaulting to `.jpg`. The cache directory holds at most 500 files and is
cleared when it overflows.

---

## 6. Outbound Packet Reference

Packets that the daemon sends to remote devices. These are relevant for
understanding protocol-level interactions but are generally abstracted by the
IPC interface. Included here for completeness and for advanced client authors
who may want to implement a full network-level implementation.

| Packet Type | Direction (Daemon → Device) | Trigger |
|---|---|---|
| `kdeconnect.identity` | Handshake | TLS identity exchange (sent twice: plaintext pre-TLS + full after TLS) |
| `kdeconnect.pair` | Pairing | Pair/unpair/reject actions |
| `kdeconnect.battery` | Battery | Reply to battery request + push on connect |
| `kdeconnect.battery.request` | Battery | Request phone battery state on connect |
| `kdeconnect.clipboard` | Clipboard | Push clipboard content to phone |
| `kdeconnect.clipboard.connect` | Clipboard | Push clipboard with timestamp on connect |
| `kdeconnect.mousepad.keyboardstate` | Mousepad | Advertise keyboard capability on connect |
| `kdeconnect.mpris` | MPRIS | Player list, NowPlaying state, seek positions, album art (broadcast + request-reply) |
| `kdeconnect.mpris.request` | MPRIS | Request player list, now-playing, volume, album art; send control actions |
| `kdeconnect.notification.reply` | Notification | Reply to a notification with inline reply support |
| `kdeconnect.notification` | RunCommand | Command output notification pushed to phone |
| `kdeconnect.runcommand` | RunCommand | Send command list to phone |
| `kdeconnect.runcommand.request` | RunCommand | Request phone's command list / execute command |
| `kdeconnect.share.request` | Share | File transfer invitation (side-channel) |
| `kdeconnect.sftp.request` | SFTP | Request the phone to start its SFTP server |
| `kdeconnect.sms.request` | SMS | Send an SMS |
| `kdeconnect.sms.request_conversations` | SMS | Request conversation list |
| `kdeconnect.sms.request_conversation` | SMS | Request a specific thread's messages |
| `kdeconnect.sms.request_attachment` | SMS | Request an MMS attachment file |
| `kdeconnect.telephony.request_mute` | Telephony | Mute incoming call ringer |
| `kdeconnect.systemvolume` | SystemVolume | Push local sink list to phone |
| `kdeconnect.systemvolume.request` | RemoteSystemVolume | Set phone volume/mute or request sink list |
| `kdeconnect.findmyphone.request` | FindMyPhone | Trigger phone ringer |
| `kdeconnect.connectivity_report.request` | Connectivity | Request signal strength report |
| `kdeconnect.ping` | Ping | Send a ping |

---

## 7. Inbound Packet Reference

Packets that the daemon handles from remote devices. Each entry shows which
plugin processes it and a link to the body struct definition.

| Packet Type | Plugin | Body Struct |
|---|---|---|
| `kdeconnect.pair` | Pair | `PairBody{Pair bool, Timestamp int64}` |
| `kdeconnect.battery` | Battery | `BatteryBody{CurrentCharge int, IsCharging bool, ThresholdEvent int}` |
| `kdeconnect.battery.request` | Battery | (empty, triggers a battery reply) |
| `kdeconnect.notification` | Notification | `NotificationBody{ID, AppName, Title, Text, IsCancel, IsClearable, Silent, RequestReplyId string}` |
| `kdeconnect.share.request` | Share | `ShareBody{Filename, NumberOfFiles, TotalPayloadSize, LastModified, CreationTime, Text, Url}` |
| `kdeconnect.sftp` | SFTP | `SftpBody{IP, Port, User, Password, Path, MultiPaths, PathNames, ErrorMessage}` |
| `kdeconnect.ping` | Ping | `PingBody{Message string}` |
| `kdeconnect.mousepad.request` | Mousepad | `MousepadBody{Dx, Dy, X, Y, SingleClick, DoubleClick, MiddleClick, RightClick, SingleHold, SingleRel, Scroll, Key, SpecialKey, Shift, Ctrl, Alt, Super}` |
| `kdeconnect.systemvolume.request` | SystemVolume | `VolumeBody{RequestSinks, Name, Volume, Muted, MaxVolume}` |
| `kdeconnect.telephony` | Telephony | `TelephonyBody{Event, ContactName, PhoneNumber, IsCancel}` |
| `kdeconnect.sms.messages` | SMS | `SMSMessagesPacket{Version, Messages []SMSMessage}` |
| `kdeconnect.sms.attachment_file` | SMS | `AttachmentFileBody{Filename, ThreadID}` |
| `kdeconnect.findmyphone.request` | FindMyPhone | (empty, triggers ring event) |
| `kdeconnect.connectivity_report` | Connectivity | `ConnectivityBody{SignalStrengths map[string]SignalStrength}` |
| `kdeconnect.clipboard` | Clipboard | `ClipboardBody{Content string, Timestamp int64}` |
| `kdeconnect.clipboard.connect` | Clipboard | `ClipboardBody{Content string, Timestamp int64}` |
| `kdeconnect.clipboard.file` | Clipboard | `ClipboardFileBody{Filename string}` |
| `kdeconnect.lock` | LockDevice | `LockBody{RequestLocked, SetLocked, IsLocked}` |
| `kdeconnect.lock.request` | LockDevice | `LockBody{}` (triggers lock/unlock) |
| `kdeconnect.mpris` | MPRIS | `MPRISRequest{RequestPlayerList, RequestNowPlaying, RequestVolume, Player, Action, AlbumArtUrl, TransferringAlbumArt, ...}` — inbound packets with `transferringAlbumArt: true` + `payloadTransferInfo` carry album art bytes (side channel) that the daemon caches to `$XDG_CACHE_HOME/kcd/art/` |
| `kdeconnect.mpris.request` | MPRIS | `MPRISRequest{}` (same struct, different semantics) — an outbound `kdeconnect.mpris.request` with `player` + `albumArtUrl` asks the phone to stream art back |
| `kdeconnect.runcommand.request` | RunCommand | `RequestBody{RequestCommandList bool, Key string}` |
| `kdeconnect.presenter` | Presenter | `PresenterBody{Dx, Dy *float64, Stop *bool}` |
| `kdeconnect.systemvolume` | RemoteSystemVolume | `VolumeBody{SinkList, Name, Volume, Muted}` |

---

## 8. Complete Walkthroughs

### 8.1 List Devices (nc)

```bash
$ echo '{"cmd":"devices"}' | nc -U /run/user/1000/kcd/kcd.sock
{"ok":true,"data":[{"id":"a1b2c3d4e5f6_...","name":"Pixel 9","type":"phone","state":"PAIRED","cert_fp":"","last_seen":"0001-01-01T00:00:00Z","connected":true}]}
```

### 8.2 Watch Events (socat)

```bash
$ socat - UNIX-CONNECT:/run/user/1000/kcd/kcd.sock
{"cmd":"watch","payload":{"events":["battery.update","notification"]}}
```

After sending the request, read the ack line, then process events:

```bash
# With jq for formatting:
$ echo '{"cmd":"watch","payload":{"events":["battery.update"]}}' | socat - UNIX-CONNECT:/run/user/1000/kcd/kcd.sock | while read -r line; do
    [ "$line" = '{"ok":true}' ] && continue
    echo "$line" | jq .
done
```

### 8.3 Pairing Flow

1. **Start listening on the daemon:**
   ```bash
   $ echo '{"cmd":"pair_listen"}' | nc -U /run/user/1000/kcd/kcd.sock
   ```

   This blocks until a pair request arrives. The output:
   ```json
   {"ok":true,"data":{"deviceId":"a1b2...","deviceName":"Pixel 9","verificationKey":"ABCD1234EFGH5678"}}
   ```

2. **In another terminal, accept the pairing:**
   ```bash
   $ echo '{"cmd":"pair","payload":{"deviceId":"a1b2...","accept":true}}' | nc -U /run/user/1000/kcd/kcd.sock
   {"ok":true}
   ```

3. **The phone shows the same verification key.** User confirms on both ends.

### 8.4 Ping a Device

```bash
$ echo '{"cmd":"ping","payload":{"deviceId":"a1b2..."}}' | nc -U /run/user/1000/kcd/kcd.sock
{"ok":true}
```

---

## 9. Error Handling

### Error Response

All commands return errors in the same format:

```json
{"ok": false, "error": "device not found"}
```

Common error strings:

| Error string | Meaning |
|---|---|
| `"device not found"` | The device ID is unknown or not paired |
| `"device not connected"` | The device is paired but not currently connected |
| `"plugin not enabled"` | The required plugin is disabled in config |
| `"invalid payload"` | The request payload could not be deserialized |
| `"command not found"` | The requested IPC command does not exist |

### Connection Loss

- If the daemon is not running, the socket connect will fail with `ENOENT` or
  `ECONNREFUSED`.
- The daemon creates the socket directory and socket file on startup, and
  removes the socket file on shutdown. The state directory (`$XDG_STATE_HOME`)
  is preserved.
- For the `watch` protocol, the daemon does not send explicit keepalive
  pings. A client can detect disconnection by a read returning 0 bytes or an
  error. Implement reconnection with exponential backoff.

### Concurrency

The IPC server is single-threaded per connection; each connection is handled
in its own goroutine. Long-running commands (`pair_listen`, `watch`) block
that connection but do not block other connections. Send one request per
connection and use a separate connection for concurrent commands.
