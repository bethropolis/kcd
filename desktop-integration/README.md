# Desktop Integration

Widget scripts and configs for showing kcd state (battery, music, connection)
in your desktop shell — Waybar, eww, ags, quickshell, or anything that can
read a Unix socket.

## How it works

All integrations follow the same pattern:

1. Connect to the Unix socket at `$XDG_RUNTIME_DIR/kcd/kcd.sock`
2. Send a JSON watch request
3. Read the ack line, then stream newline-delimited JSON events
4. Parse each event and update your widget

## Event stream reference

Send this once per connection:

```json
{"cmd":"watch","payload":{"events":["device.connected","device.disconnected","battery.update","mpris.update"]}}
```

Events arrive as NDJSON (one JSON object per line):

```
{"type":"battery.update","deviceId":"abc123","payload":{"charge":85,"charging":false}}
{"type":"mpris.update","deviceId":"abc123","payload":{"title":"Song","artist":"Band","album":"Album","isPlaying":true,"length":200000}}
{"type":"device.connected","deviceId":"abc123","payload":{}}
{"type":"device.disconnected","deviceId":"abc123","payload":{}}
```

After connecting, the first line is always `{"ok":true}` — read and discard it,
then process subsequent lines as events.

### Available event types

| Type | Payload fields |
|---|---|
| `device.connected` | `{}` |
| `device.disconnected` | `{}` |
| `battery.update` | `charge` (int 0–100), `charging` (bool) |
| `mpris.update` | `title`, `artist`, `album`, `isPlaying` (bool), `length` (μs), `position` (μs) |
| `telephony.ringing` | `event`, `contactName`, `phoneNumber` |
| `telephony.talking` | `event`, `contactName`, `phoneNumber` |
| `telephony.missed` | `event`, `contactName`, `phoneNumber` |
| `telephony.canceled` | `event`, `contactName`, `phoneNumber` |

Subscribe only to the event types you actually use — the payload field
filters what the daemon sends.

## Minimal integration template

```bash
#!/bin/bash
# Replace this with any language: Python, Lua, Go, etc.

SOCKET="${XDG_RUNTIME_DIR}/kcd/kcd.sock"
PAYLOAD='{"cmd":"watch","payload":{"events":["battery.update","mpris.update","device.connected","device.disconnected"]}}'

connect_and_stream() {
    [[ -S "$SOCKET" ]] || return 1
    nc -U "$SOCKET" <<< "$PAYLOAD" 2>/dev/null
}

# Discard ack, then loop over events
{
    read -r _
    while read -r event; do
        type=$(echo "$event" | jq -r '.type')
        case "$type" in
            "battery.update")
                charge=$(echo "$event" | jq -r '.payload.charge')
                # Update your widget with $charge
                ;;
            "mpris.update")
                title=$(echo "$event" | jq -r '.payload.title // empty')
                # Update your widget with $title
                ;;
        esac
    done
} < <(connect_and_stream)
```

A few details that matter:

- **Reconnection:** When the daemon restarts, the socket connection drops.
  Wrap the read loop in `while true; do ... done` — the read blocks
  during normal operation, and only retries the connection on failure.
  Add a 1-second delay before the retry to avoid busy-waiting on a
  dead socket. Your process is idle 99.9% of the time.
- **Stale state:** After a disconnect, clear battery/music values immediately
  so you don't show stale data while waiting for a reconnect.
- **Socket path:** Uses `$XDG_RUNTIME_DIR/kcd/kcd.sock` — note the `kcd/`
  subdirectory, the socket is NOT directly in `/run/user/$uid/`.
- **nc variants:** OpenBSD nc (`-U`) works out of the box. Other nc flavours
  (GNU, Nmap) may need `-N` or `-q0` to keep reading after EOF.

## Available integrations

| Directory | Target | Tools required |
|---|---|---|
| `waybar/` | Waybar custom module | `jq` + `nc` / `socat` |

The Waybar script is a complete reference — ~180 lines of bash handling
reconnection, state clearing, Nerd Font icons, and JSON rendering.

## Building your own

Drop a subdirectory here if you want to share it. Or keep your script
in your dotfiles — either works. The socket protocol is stable; the
only thing that changes is how you render the data.
