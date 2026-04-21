# kcd Architecture

`kcd` is a headless, concurrent, event-driven implementation of the KDE Connect v8 protocol written in Go. This document describes every major subsystem, how they fit together, and the design decisions behind them.

---

## High-Level Overview

```
                  ┌─────────────────────────────────────────────┐
                  │                  kcd daemon                  │
                  │                                              │
 Android phone ──►│  Discovery  ──►  Transport  ──►  Device     │
  (KDE Connect)   │  (UDP + mDNS)    (TLS/TCP)       Registry   │
                  │                                      │       │
                  │                               Plugin Router  │
                  │                                      │       │
                  │  battery  clipboard  share  mpris  ...│...   │
                  │                                      │       │
                  │                               Event Bus      │
                  │                                      │       │
                  │                             IPC Server       │
                  └──────────────────────────────────┬──────────┘
                                                     │ Unix socket
                                              ┌──────▼──────┐
                                              │  kcd <cmd>  │
                                              │  (CLI client)│
                                              └─────────────┘
```

---

## 1. Discovery (`internal/discovery`)

Devices are found via two parallel mechanisms that run concurrently:

### UDP Broadcast

`Broadcaster` sends the local identity packet to `255.255.255.255:1716` on a timer. For multi-homed machines it additionally sends directed broadcasts to each interface's subnet broadcast address, improving reliability on complex network setups.

`Listener` binds to `0.0.0.0:1716` and parses every incoming UDP packet. Packets whose `type` is not `kdeconnect.identity` or whose `deviceId` matches the local device are silently dropped.

The broadcast interval is adaptive: when `shouldReduce()` returns true (all known devices already connected) the interval steps up to 60 seconds, cutting network chatter during idle operation. Setting `enable_broadcast = false` in config disables UDP entirely after pairing.

### mDNS / Zeroconf (`_kdeconnect._udp`)

At startup the `Broadcaster` registers the local device as a Zeroconf service with the `grandcat/zeroconf` library. TXT records carry `id`, `name`, `type`, and `protocol` fields per the KDE Connect spec.

`Listener.runMdnsDiscovery` browses `_kdeconnect._udp.local.` and synthesises a `protocol.Packet` for every discovered peer — feeding it through the same `onDeviceFound` callback used by UDP. This makes mDNS transparent to the rest of the stack.

**Why both?** UDP broadcast covers the common case instantly. mDNS handles restricted networks (Docker bridges, enterprise Wi-Fi, newer Android versions) where broadcast is filtered.

---

## 2. Transport (`internal/transport`)

The KDE Connect TLS handshake is non-standard: **the TCP initiator acts as TLS server, and the acceptor acts as TLS client**. This is the opposite of conventional TLS and must be handled correctly.

### Listener

`Listener` accepts inbound TCP connections on port 1716. Each accepted connection is handed off to a goroutine that reads the initial plaintext identity packet, upgrades the connection to TLS (as client), and calls the registered `OnConnect` callback.

### Outbound connections

When a device is discovered via UDP/mDNS, the daemon dials out to the device's TCP port. The dialling side acts as TLS server (providing the local certificate) and the accepting side acts as TLS client — the reverse of conventional roles.

### Conn

`transport.Conn` wraps a `net.Conn` with buffered JSON framing. Packets are newline-delimited JSON. The `ReadPacket` / `WritePacket` methods acquire from the `protocol.PacketPool` to minimise allocations on the hot path.

### Certificate management (`internal/cert`)

On first run, `cert.go` generates a 2048-bit RSA self-signed certificate stored at the paths given in config (`cert_file`, `key_file`). On subsequent runs the certificate is loaded from disk. The SHA-256 fingerprint of the peer's certificate is compared during pairing to establish trust.

---

## 3. Protocol (`internal/protocol`)

All on-wire data is JSON. The top-level envelope is:

```json
{
  "id": 1234567890,
  "type": "kdeconnect.battery",
  "body": { ... }
}
```

`protocol.Packet` models this envelope. The `Body` field is kept as `json.RawMessage` to defer parsing until the routing plugin claims it, avoiding unnecessary allocations.

A `sync.Pool` (`PacketPool`) is used for `Packet` objects; callers must call `ReleasePacket` after use.

`protocol.IdentityBody` models the identity exchange that every device sends on first contact. It advertises the device's ID, name, type, protocol version, TCP port, and the list of incoming/outgoing plugin types it supports.

---

## 4. Device (`internal/device`)

### Device struct

A `Device` wraps an active TCP connection with:

- Identity fields (ID, Name, Type, CertFP)
- A `state` field (Unpaired, PairRequested, PairRequestedByPeer, Paired) protected by `sync.RWMutex`
- A `sender.go` — a goroutine-safe write queue that serialises all outbound packets, preventing concurrent writes to the underlying TCP connection
- `IsConnected()` — true when the underlying connection is alive

### Registry (`device.Registry`)

`Registry` holds all known devices (both connected and remembered-from-disk). It is safe for concurrent reads and writes. On disk, device state is persisted to `$XDG_STATE_HOME/kcd/devices.json` so that paired devices survive daemon restarts.

### State machine

```
  Unpaired
      │
      ├─ local initiates ──► PairRequested
      │                            │
      │                      peer accepts ──► Paired
      │
      └─ peer initiates ──► PairRequestedByPeer
                                   │
                             local accepts ──► Paired
                             local rejects ──► Unpaired
```

---

## 5. Plugin System (`internal/plugin`)

### Interface

Every feature implements `Plugin`:

```go
type Plugin interface {
    Name()          string
    IncomingTypes() []string          // packet types this plugin handles
    OutgoingTypes() []string          // packet types it sends
    Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error
    OnConnect(dev device.Sender)
    OnDisconnect(dev device.Sender)
    Timeout()       time.Duration
}
```

### Routing

`plugin.Registry.Dispatch` receives every inbound packet and calls `Handle` on the registered plugin for that `pkt.Type`. Dispatch is sequential per device — one packet at a time per connection. Any plugin that performs I/O (disk writes, subprocess execution, D-Bus calls) **must** spawn a goroutine internally, so it doesn't block the TCP read loop.

Plugin execution is wrapped in a context with the plugin's declared `Timeout()` deadline.

### Implemented plugins

| Package | Types handled | Notes |
|---|---|---|
| `battery` | `kdeconnect.battery` | Tracks charge + charging; fires `battery.update` events |
| `clipboard` | `kdeconnect.clipboard.connect` | Wayland (`wl-copy/wl-paste`) and X11 (`xclip`) |
| `findmyphone` | `kdeconnect.findmyphone.request` | Runs `paplay` or `aplay` |
| `lockdevice` | `kdeconnect.lock.request` | Calls `loginctl lock/unlock-session` |
| `mousepad` | `kdeconnect.mousepad.request` | `ydotool` (Wayland) / `xdotool` (X11) |
| `mpris` | `kdeconnect.mpris.request` | D-Bus via `godbus`; controls any MPRIS2 player |
| `notification` | `kdeconnect.notification` | `notify-send`; fires `notification` / `notification.canceled` events |
| `pair` | `kdeconnect.pair` | Manages the pairing handshake and certificate fingerprint verification |
| `ping` | `kdeconnect.ping` | Fires `ping.received`; can be sent outbound |
| `runcommand` | `kdeconnect.runcommand` | Executes commands from the `[commands]` config table |
| `sendnotification` | — | Sends `kdeconnect.notification` outbound |
| `sftp` | `kdeconnect.sftp.mountrequest` | Requests SFTP credentials; optionally calls `sshfs` |
| `share` | `kdeconnect.share.request` | Streaming file receive + URL/text handling; fires progress events |
| `sms` | — | Sends `kdeconnect.sms.request` outbound (receive not yet implemented) |
| `systemvolume` | `kdeconnect.systemvolume` | D-Bus PulseAudio/PipeWire volume control |
| `telephony` | `kdeconnect.telephony` | Fires `telephony.ringing`, `.missed`, `.canceled` |
| `connectivity` | `kdeconnect.connectivity_report` | Fires `connectivity.update` |

---

## 6. Event Bus (`internal/events`)

`Bus` is a non-blocking, fan-out pub/sub system. Plugins publish typed events; IPC streams and external watchers subscribe.

```go
bus.Publish(events.TypeBatteryUpdate, deviceID, map[string]any{
    "charge":   85,
    "charging": true,
})
```

Each `Subscriber` holds a buffered channel (capacity 64). If a subscriber falls behind, events are dropped (with a warning) rather than blocking the publisher. Subscribers can filter by event type; an empty filter receives all events.

### Event types

| Event | Trigger |
|---|---|
| `device.added` | New device seen for the first time |
| `device.removed` | Device unpaired and removed from registry |
| `device.connected` | TCP connection established |
| `device.disconnected` | TCP connection closed |
| `pair.requested` | Incoming pair request from peer |
| `pair.accepted` | Pairing completed |
| `pair.rejected` | Pairing denied or cancelled |
| `battery.update` | New battery reading received |
| `notification` | Notification forwarded from phone |
| `notification.canceled` | Phone dismissed a notification |
| `share.progress` | File transfer progress update |
| `share.complete` | File transfer finished |
| `share.text` | Plain text received via Share plugin |
| `share.url` | URL received via Share plugin |
| `ping.received` | Ping packet arrived |
| `telephony.ringing` | Incoming call |
| `telephony.missed` | Missed call |
| `telephony.canceled` | Call ended |
| `connectivity.update` | Signal strength report |
| `sftp.mount` | SFTP credentials received |

---

## 7. IPC (`internal/ipc`)

The daemon opens a Unix domain socket at `$XDG_RUNTIME_DIR/kcd/kcd.sock` (typically `/run/user/1000/kcd/kcd.sock`). The CLI (`cmd/kcd`) connects to this socket as a client.

### Request / Response

Commands are single JSON objects followed by a newline:

```json
{"cmd": "pair", "payload": {"deviceId": "a1b2..."}}
```

Responses are also JSON:

```json
{"ok": true}
{"ok": false, "error": "device not found"}
{"ok": true, "data": [...]}
```

### Event streaming (`watch`)

The `CmdWatch` command keeps the connection open and streams NDJSON events as they arrive on the event bus. The CLI's `watch` command applies optional filters sent in the `WatchPayload` and pipes the stream to stdout — making it composable with `jq`, `grep`, Waybar, etc.

### Handler routing

`Handler.HandleRequest` dispatches built-in commands (`devices`, `pair`, `unpair`, `ping`). Additional commands are registered at startup via `Handler.Register`, keeping IPC extensible without modifying the core handler.

---

## 8. CLI (`cmd/kcd`)

The `kcd` binary is both daemon and client. Sub-commands that don't require the daemon (just `kcd daemon`) connect to the Unix socket via `pkg/client.Client`.

```
kcd daemon        — start the background daemon
kcd devices       — list known/connected devices
kcd connect <ip>  — manually connect by IP
kcd pair <id>     — initiate or accept pairing
kcd unpair <id>   — revoke trust
kcd ping <id>     — send a ping
kcd battery <id>  — fetch battery status
kcd share <id> <file>         — send a file
kcd clipboard [id]            — push local clipboard to phone
kcd sftp request <id>         — request SFTP credentials
kcd sftp mount <id>           — mount via sshfs
kcd run list <id>             — list remote commands
kcd run exec <id> <key>       — execute a remote command
kcd reply <id> <reply-id> <msg>  — reply to a notification
kcd call mute <id>            — mute an incoming call
kcd findmyphone <id>          — ring the phone
kcd lock <id>                 — lock the desktop
kcd unlock <id>               — unlock the desktop
kcd sms <id> <number> <msg>   — send an SMS
kcd watch [--events=...] [--json]  — stream live events
```

---

## 9. Daemon Lifecycle (`internal/daemon`)

`daemon.Run` orchestrates startup in this order:

1. Validate config; ensure TLS certificate exists
2. Load persisted device state from disk
3. Start the event bus
4. Register all enabled plugins
5. Start the TCP listener (inbound connections)
6. Start the IPC Unix socket server
7. Start UDP broadcaster and mDNS registration
8. Start the mDNS browser (discovery listener)
9. Block until context is cancelled (SIGINT / SIGTERM)
10. Graceful shutdown: close listener, shutdown mDNS, stop broadcaster

---

## 10. File Layout

```
kcd/
├── cmd/kcd/main.go               — CLI entry point (daemon + client sub-commands)
├── pkg/client/client.go          — Public client library (wraps Unix socket calls)
├── internal/
│   ├── cert/                     — TLS certificate generation and loading
│   ├── config/                   — TOML config loading, defaults, validation
│   ├── daemon/                   — Startup orchestration; transport wiring
│   ├── dbusutil/                 — Shared D-Bus / MPRIS helpers
│   ├── device/                   — Device struct, Registry, state machine, sender
│   ├── discovery/                — UDP broadcaster + listener; mDNS register + browse
│   ├── events/                   — Non-blocking fan-out event bus
│   ├── ipc/                      — Unix socket server, request handler, protocol types
│   ├── plugin/                   — Plugin interface, registry, dispatcher
│   ├── plugins/                  — One package per KDE Connect plugin
│   │   ├── battery/
│   │   ├── clipboard/
│   │   ├── connectivity/
│   │   ├── findmyphone/
│   │   ├── lockdevice/
│   │   ├── mousepad/
│   │   ├── mpris/
│   │   ├── notification/
│   │   ├── pair/
│   │   ├── ping/
│   │   ├── runcommand/
│   │   ├── sendnotification/
│   │   ├── sftp/
│   │   ├── share/
│   │   ├── sms/
│   │   ├── systemvolume/
│   │   └── telephony/
│   ├── protocol/                 — Packet pool, identity, pair packet helpers
│   └── transport/                — TLS conn wrapper, TCP listener, plaintext bootstrap
├── packaging/                    — systemd units, firewall rules, example config
├── kcd-waybar-integration/       — Ready-made Waybar config and stylesheet
├── scripts/                      — install / uninstall / post-install hooks
└── Dockerfile
```

---

## Design Decisions

**No CGo.** The entire daemon is pure Go. This keeps cross-compilation trivial and the binary fully static-linkable.

**No GUI / D-Bus session bus dependency.** D-Bus is only used optionally by the MPRIS and system-volume plugins. All other plugins use subprocess calls (`notify-send`, `wl-copy`, `ydotool`, etc.) so the daemon can run in a headless session or inside a container.

**Pool-based packet allocation.** `protocol.PacketPool` eliminates per-packet heap allocation on the TCP read loop — important when receiving high-frequency events like mousepad movement.

**Sequential plugin dispatch per device.** Plugins handle packets one at a time per connection. This avoids data races in plugin state without requiring locks in individual plugins. Heavy operations (disk I/O, subprocesses) are explicitly goroutine-spawned inside the plugin.

**Adaptive broadcast interval.** Once all known devices are connected the broadcast cadence drops to 60 seconds. With `enable_broadcast = false` the daemon sits at 0.0% idle CPU.

**NDJSON event stream.** The `kcd watch` event stream is newline-delimited JSON, making it directly composable with standard Unix tools (`jq`, `grep`, `while read`) and Waybar's `exec` module without any custom parsing.