# Client Developer Guide

This guide explains how to write a client (GUI, CLI, widget) that communicates
with the `kcd` daemon through its Unix socket IPC interface. The daemon is
headless — there is no built-in GUI. Everything a client can do goes through
the IPC protocol documented in [`IPC_PROTOCOL.md`](IPC_PROTOCOL.md).

If you are building a Rust/GTK4 client, see the separate reference documents
in [`../../rust/kcd-client/`](../../rust/kcd-client/) (`PROJECT_GUIDE.md`,
`AGENT.md`, `ROADMAP.md`).

---

## 1. Finding the Socket

The IPC socket path depends on the platform:

- **Linux (systemd user session):** `/run/user/<uid>/kcd/kcd.sock`
- **Linux (no systemd):** `$XDG_RUNTIME_DIR/kcd/kcd.sock`
- **Fallback:** Run `kcd doctor` which prints the socket path and whether the
  daemon is reachable.

The socket is a Unix stream socket with `S_IRUSR|S_IWUSR` permissions (the
owning user only). The directory `kcd/` is created inside the runtime dir.

**Python: Helper to find the socket:**

```python
import os
import pwd

def socket_path():
    uid = os.getuid()
    runtime_dir = os.environ.get("XDG_RUNTIME_DIR", f"/run/user/{uid}")
    return os.path.join(runtime_dir, "kcd", "kcd.sock")
```

---

## 2. Connecting

All IPC communication uses newline-delimited JSON (NDJSON) over a Unix stream
socket. Send one JSON object per line, terminated by `\n`. Read one line per
response.

**Python: Basic request-response:**

```python
import json
import socket

def ipc_request(sock, cmd, payload=None):
    req = {"cmd": cmd}
    if payload is not None:
        req["payload"] = payload
    sock.sendall((json.dumps(req) + "\n").encode())
    # Read one line
    buf = b""
    while True:
        c = sock.recv(1)
        if c == b"\n" or not c:
            break
        buf += c
    return json.loads(buf.decode())

sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
sock.connect(socket_path())

resp = ipc_request(sock, "devices")
if resp["ok"]:
    for dev in resp["data"]:
        print(f'{dev["name"]} ({dev["id"]}) — {dev["state"]}')
```

---

## 3. Device Lifecycle

### 3.1 Listing Devices

The `devices` command returns all known devices (paired and unpaired).

```python
resp = ipc_request(sock, "devices")
for dev in resp["data"]:
    print(f'{dev["name"]} — {dev["state"]} — connected={dev["connected"]}')
```

States: `UNPAIRED`, `PAIR_REQUESTED`, `PAIR_REQUESTED_BY_PEER`, `PAIRED`.

### 3.2 Pairing Flow

Pairing requires a persistent watch connection to receive the pairing request
event.

**Step 1: Start listening for a pair request (in a thread or second socket):**

```python
import threading, json, socket

def listen_for_pair():
    lsock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    lsock.connect(socket_path())
    lsock.sendall(b'{"cmd":"pair_listen"}\n')
    buf = b""
    while True:
        c = lsock.recv(1)
        if c == b"\n" or not c:
            break
        buf += c
    resp = json.loads(buf.decode())
    if resp["ok"]:
        data = resp["data"]
        print(f'Pair request from: {data["deviceName"]}')
        print(f'Verification key: {data["verificationKey"]}')
        # User verifies the key matches on both devices
        return data["deviceId"]
    return None

# Run in background:
pair_thread = threading.Thread(target=listen_for_pair, daemon=True)
pair_thread.start()
```

**Step 2: Have the user initiate pairing on the phone** (KDE Connect → kcd
device → Request Pair). The `pair_listen` handler will return the request.

**Step 3: Accept the pairing:**

```python
resp = ipc_request(sock, "pair", {"deviceId": device_id, "accept": True})
if resp["ok"]:
    print("Paired successfully!")
```

### 3.3 Unpairing

```python
resp = ipc_request(sock, "unpair", {"deviceId": device_id})
```

---

## 4. Watching Events

The `watch` command creates a persistent connection that streams live events.

### 4.1 Basic Watch Loop

```python
import json
import socket
import threading

class KcdWatcher:
    def __init__(self, socket_path, event_types=None):
        self.socket_path = socket_path
        self.event_types = event_types
        self._running = False
        self._callbacks = {}

    def on(self, event_type, callback):
        self._callbacks.setdefault(event_type, []).append(callback)

    def _read_line(self, sock):
        buf = b""
        while True:
            c = sock.recv(1)
            if c == b"\n" or not c:
                break
            buf += c
        return buf.decode()

    def start(self):
        self._running = True
        thread = threading.Thread(target=self._run, daemon=True)
        thread.start()

    def stop(self):
        self._running = False

    def _run(self):
        import time
        backoff = 1
        while self._running:
            try:
                sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
                sock.settimeout(30)
                sock.connect(self.socket_path)

                payload = None
                if self.event_types:
                    payload = {"events": self.event_types}
                req = {"cmd": "watch"}
                if payload:
                    req["payload"] = payload
                sock.sendall((json.dumps(req) + "\n").encode())

                # Consume the ack line
                ack = self._read_line(sock)
                if not json.loads(ack).get("ok"):
                    continue

                backoff = 1  # Reset on successful connect
                while self._running:
                    line = self._read_line(sock)
                    if not line:
                        break
                    event = json.loads(line)
                    etype = event.get("type")
                    if etype in self._callbacks:
                        for cb in self._callbacks[etype]:
                            cb(event["deviceId"], event.get("payload"))

            except (socket.error, json.JSONDecodeError, ConnectionError) as e:
                print(f"Watch error: {e}")
            finally:
                try:
                    sock.close()
                except Exception:
                    pass

            # Exponential backoff 1s → 30s
            if self._running:
                time.sleep(backoff)
                backoff = min(backoff * 2, 30)
```

### 4.2 Using the Watcher

```python
w = KcdWatcher(socket_path(), ["battery.update", "notification", "mpris.update"])

@w.on("battery.update")
def on_battery(device_id, payload):
    print(f'Battery: {payload["charge"]}% ({"charging" if payload["charging"] else "discharging"})')

@w.on("notification")
def on_notification(device_id, payload):
    print(f'{payload["appName"]}: {payload["title"]} — {payload["text"]}')

@w.on("mpris.update")
def on_mpris(device_id, payload):
    if payload and payload.get("isPlaying"):
        print(f'Now playing: {payload["title"]} by {payload["artist"]}')
    else:
        print("Paused/stopped")

w.start()

# Keep main thread alive
import time
try:
    while True:
        time.sleep(1)
except KeyboardInterrupt:
    w.stop()
```

### 4.3 Event Types Quick Reference

| Filter string | When it fires |
|---|---|
| `device.connected` | TCP connection established |
| `device.disconnected` | TCP connection lost |
| `battery.update` | Battery level or charging state changed |
| `notification` | Push notification from device |
| `share.progress` | File transfer progress update |
| `share.complete` | File transfer finished |
| `mpris.update` | Now-playing state changed |
| `sms.incoming` | SMS/MMS received |
| `pair.requested` | Remote device wants to pair |
| `ping.received` | Ping from device |

See [`IPC_PROTOCOL.md §5`](IPC_PROTOCOL.md#5-event-types) for the full list.

---

## 5. Sending Commands

### 5.1 Request Battery State

```python
resp = ipc_request(sock, "battery", {"deviceId": dev_id})
if resp["ok"]:
    data = resp["data"]
    print(f'Battery: {data["charge"]}% (charging: {data["charging"]})')
```

### 5.2 Send a Ping

```python
ipc_request(sock, "ping", {"deviceId": dev_id})
```

The phone should vibrate/show a notification.

### 5.3 Share a File

```python
ipc_request(sock, "share", {"deviceId": dev_id, "file": "/path/to/file.pdf"})
```

### 5.4 MPRIS Control

```python
# Play/Pause
ipc_request(sock, "mpris_action", {"deviceId": dev_id, "action": "playpause"})

# Next track
ipc_request(sock, "mpris_action", {"deviceId": dev_id, "action": "next"})

# Set volume
ipc_request(sock, "mpris_action", {"deviceId": dev_id, "action": "setVolume", "value": 50})
```

### 5.5 Send SMS

```python
ipc_request(sock, "send_sms", {
    "deviceId": dev_id,
    "phoneNumber": "+1234567890",
    "message": "Hello from kcd!"
})
```

### 5.6 Ring the Phone

```python
ipc_request(sock, "findmyphone", {"deviceId": dev_id})
# or: ipc_request(sock, "ring", {"deviceId": dev_id})
```

### 5.7 Lock/Unlock

```python
ipc_request(sock, "lock", {"deviceId": dev_id})
ipc_request(sock, "unlock", {"deviceId": dev_id})
```

### 5.8 Push Clipboard

```python
ipc_request(sock, "clipboard_push", {"deviceId": dev_id})
```

The daemon reads the local clipboard (`wl-paste`/`xclip`) and sends it.

### 5.9 Remote Volume Control

```python
# List audio sinks
resp = ipc_request(sock, "remote_volume_list", {"deviceId": dev_id})
if resp["ok"]:
    for sink in resp["data"]:
        print(f'{sink["name"]}: {sink["volume"]}% (muted: {sink["muted"]})')

# Set volume
ipc_request(sock, "remote_volume_set", {
    "deviceId": dev_id, "name": "media", "volume": 50
})

# Mute/unmute
ipc_request(sock, "remote_volume_mute", {
    "deviceId": dev_id, "name": "media", "muted": True
})
```

Volume changes from the phone arrive as `volume.update` events. The event payload
is either `{"name", "volume", "muted"}` for a single sink change or `{"sinks": [...]}`
for the full sink list.

### 5.10 Get SFTP Connection Info

```python
resp = ipc_request(sock, "sftp_info", {"deviceId": dev_id})
if resp["ok"]:
    info = resp["data"]
    print(f'SSH: {info["user"]}@{info["ip"]} -p {info["port"]}')
```

### 5.10 Get Daemon Status

```python
resp = ipc_request(sock, "status")
if resp["ok"]:
    s = resp["data"]
    print(f'kcd v{s["version"]} — {s["uptimeHuman"]} uptime')
    print(f'Plugins: {", ".join(s["plugins"])}')
    print(f'{s["connectedCount"]}/{s["deviceCount"]} devices connected')
```

---

## 6. Handling Payloads

Each event type carries a different payload shape. Here are the common ones:

### Battery Update

```json
{"charge": 85, "charging": true}
```

### Notification

```json
{
  "appName": "Signal",
  "title": "Alice",
  "text": "See you later",
  "requestReplyId": "reply-123",
  "id": "notif-456"
}
```

- `requestReplyId` is present only for notifications that support inline
  replies. Use it with the `notify_reply` command.

### Share Progress

```json
{"file": "video.mp4", "current": 1048576, "total": 8388608}
```

### MPRIS Update

```json
{
  "player": "spotify",
  "title": "Song Title",
  "artist": "Artist",
  "album": "Album",
  "isPlaying": true,
  "pos": 45000,
  "length": 240000,
  "volume": 80,
  "canControl": true,
  "shuffle": false,
  "loopStatus": "None"
}
```

### SMS Incoming

```json
{
  "body": "Hello!",
  "sender": "+1234567890",
  "date": 1716800000000,
  "type": 1,
  "thread_id": 42,
  "read": false
}
```

### Device Connected

```json
{"id": "a1b2...", "name": "Pixel 9", "type": "phone"}
```

---

## 7. Building Features

### 7.1 File Transfer Tracker

Watch for `share.progress` and `share.complete` events to build a transfer
progress UI:

```python
transfers = {}

@w.on("share.progress")
def on_progress(dev_id, payload):
    fname = payload["file"]
    cur, total = payload["current"], payload["total"]
    pct = (cur / total) * 100 if total > 0 else 0
    transfers[fname] = pct
    print(f'{fname}: {pct:.0f}%')

@w.on("share.complete")
def on_complete(dev_id, payload):
    fname = payload["file"]
    if payload["success"]:
        print(f'{fname}: complete!')
    else:
        print(f'{fname}: FAILED — {payload.get("error", "unknown")}')
    transfers.pop(fname, None)
```

### 7.2 Notification Inbox

Subscribe to all `notification` events and build an inbox:

```python
inbox = []

@w.on("notification")
def on_notification(dev_id, payload):
    inbox.append({
        "app": payload["appName"],
        "title": payload["title"],
        "text": payload["text"],
        "time": import_time.time(),
        "can_reply": "requestReplyId" in payload
    })
    # Keep last 50
    if len(inbox) > 50:
        inbox.pop(0)

def reply_to_notification(dev_id, notif_id, message):
    ipc_request(command_sock, "notify_reply", {
        "deviceId": dev_id,
        "replyId": notif_id,
        "message": message
    })
```

### 7.3 SMS Viewer

```python
conversations = {}

@w.on("sms.incoming")
def on_sms(dev_id, payload):
    thread_id = payload["thread_id"]
    if thread_id not in conversations:
        conversations[thread_id] = []
    conversations[thread_id].append({
        "from": payload["sender"],
        "body": payload["body"],
        "date": payload["date"],
        "type": "received" if payload["type"] == 1 else "sent"
    })

def send_sms(dev_id, phone_number, message):
    ipc_request(command_sock, "send_sms", {
        "deviceId": dev_id,
        "phoneNumber": phone_number,
        "message": message
    })
```

### 7.4 Remote Media Controller

```python
class MediaController:
    def __init__(self, sock, dev_id):
        self.sock = sock
        self.dev_id = dev_id
        self.now_playing = None

    def refresh(self):
        resp = ipc_request(self.sock, "mpris_action", {
            "deviceId": self.dev_id,
            "action": "playpause"  # triggers state push
        })

    def play_pause(self):
        ipc_request(self.sock, "mpris_action", {
            "deviceId": self.dev_id, "action": "playpause"
        })

    def next(self):
        ipc_request(self.sock, "mpris_action", {
            "deviceId": self.dev_id, "action": "next"
        })

    def previous(self):
        ipc_request(self.sock, "mpris_action", {
            "deviceId": self.dev_id, "action": "previous"
        })

    def set_volume(self, vol):
        ipc_request(self.sock, "mpris_action", {
            "deviceId": self.dev_id, "action": "setVolume", "value": vol
        })
```

---

## 8. Error Recovery

### 8.1 Daemon Not Running

```python
def check_daemon(path):
    try:
        sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        sock.settimeout(2)
        sock.connect(path)
        sock.close()
        return True
    except (FileNotFoundError, ConnectionRefusedError, socket.error):
        return False
```

### 8.2 Watch Reconnection

The `KcdWatcher` class above implements exponential backoff (1s → 30s max).
The reconnection strategy should:

1. Close the old socket on any read error.
2. Wait with backoff (1s, 2s, 4s, 8s, 16s, 30s) before retrying.
3. Reset backoff to 1s on successful connection.
4. Re-send the `watch` request with the same event type filter.
5. After reconnecting, wait for the initial state dump to catch up on missed
   state (device.connected events will re-establish connectivity).

### 8.3 Device Disconnection

When a device disconnects:
- A `device.disconnected` event is emitted.
- The device remains in `PAIRED` state and will auto-reconnect when the phone
  re-enters the network (if LAN broadcast is enabled).
- Your UI should show the device as offline but keep its configuration.
- On reconnection, a `device.connected` event is emitted followed by the state
  dump for that device.

### 8.4 Pairing Loss

If the phone is reset or the app is reinstalled:
- The device ID changes (permanent identifier generated on first app launch).
- The old device entry will never reconnect.
- Remove the stale entry with `unpair` and initiate a fresh pairing.

---

## 9. Integration Examples

### 9.1 Desktop Widget (Waybar)

The `desktop-integration/` directory contains ready-to-use scripts. For a
simple waybar custom module:

```bash
#!/bin/bash
# ~/.config/waybar/scripts/kcd-custom.sh
while true; do
    socat - UNIX-CONNECT:/run/user/$(id -u)/kcd/kcd.sock <<'EOF' | while read -r line; do
{"cmd":"watch","payload":{"events":["battery.update","mpris.update"]}}
EOF
        [ "$line" = '{"ok":true}' ] && continue
        echo "$line" | jq -c 'select(.type=="battery.update") | {text: ("🔋 \(.payload.charge)%")}'
    done
    sleep 1
done
```

See `desktop-integration/README.md` for the maintained integration scripts.

### 9.2 Minimal Python Event Monitor

```python
#!/usr/bin/env python3
"""Simple kcd event monitor."""
import json, socket, sys, os

SOCK = os.path.join(
    os.environ.get("XDG_RUNTIME_DIR", f"/run/user/{os.getuid()}"),
    "kcd", "kcd.sock"
)

def main():
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    sock.connect(SOCK)
    sock.sendall(b'{"cmd":"watch","payload":{"events":' +
                 sys.argv[1:].encode() + b'}}\n')
    # Discard ack
    ack = sock.makefile("r").readline()
    for line in sock.makefile("r"):
        event = json.loads(line)
        print(json.dumps(event, indent=2))

if __name__ == "__main__":
    main()
```

Usage: `python3 monitor.py '["battery.update","mpris.update"]'`

### 9.3 GTK4/Shell Proxy

For desktop shell widgets (eww, ags, quickshell), run `kcd watch` in the
background and pipe the JSON output to a named pipe or parse it directly:

```bash
# In the shell widget's startup:
kcd watch --json '["battery.update","mpris.update"]' | while read -r line; do
    [ "$line" = '{"ok":true}' ] && continue
    # Parse and update widget state
done
```

---

## 10. Going Further

### Rust/GTK4 Client

A reference Rust GTK4 client implementation is being developed separately at
[`../../rust/kcd-client/`](../../rust/kcd-client/). Its `PROJECT_GUIDE.md`,
`AGENT.md`, and `ROADMAP.md` documents provide additional architectural
context for building a full-featured GUI client.

### Protocol-Level Implementation

If you are implementing a full KDE Connect network-level client (not just IPC),
refer to the upstream protocol spec at
<https://valent.andyholmes.ca/documentation/protocol.html>.

### Getting Help

- **Daemon issues:** `/home/bet/Projects/go/kde-connect` — open a GitHub
  issue.
- **Protocol questions:** The IPC protocol reference in
  [`IPC_PROTOCOL.md`](IPC_PROTOCOL.md) is the source of truth.
- **API stability:** The IPC protocol is considered stable within a major
  version. Breaking changes will be documented in release notes.
