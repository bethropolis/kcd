# kcd — Agent Instructions

## Project Identity

**`kcd`** is a headless Go daemon implementing KDE Connect protocol v8. Single binary — daemon and CLI client share the same entry point via `urfave/cli/v2` subcommands. The daemon exposes a Unix socket for IPC; the CLI connects to it as a client. No GUI. D-Bus is used only by the MPRIS and SystemVolume plugins, and those fail gracefully if D-Bus is unavailable.

- **Language:** Go 1.25, `CGO_ENABLED=0` everywhere
- **Module:** `github.com/bethropolis/kcd`
- **Static binary** — `ldd kcd` must print "not a dynamic executable"
- **Core constraint:** Memory efficiency. Never buffer what can be streamed. Reuse allocations via the packet pool and a single `bufio.Reader` per connection.

---

## Absolute Rules

Never violate these. If a change would break one, stop and reconsider the approach entirely.

1. **CGO_ENABLED=0 always.** No cgo, no dynamic linking. Build with `CGO_ENABLED=0 go build`.
2. **Never buffer file payloads.** Receive with `io.Copy(dst, io.LimitReader(conn, payloadSize))`. Buffering the entire file causes OOM.
3. **Never write to a `net.Conn` directly.** Always send via `device.Send(packet)`. The device's internal send channel is the only goroutine-safe write path.
4. **One `bufio.Reader` per connection, allocated once at accept/dial time.** Never allocate a new one inside the packet read loop.
5. **Use the packet pool.** Acquire with `protocol.AcquirePacket()`, release with `protocol.ReleasePacket(pkt)` after the plugin finishes — not before.
6. **Protocol version = 8.** Hardcoded as `protocol.ProtocolVersion`. Never send a different value.
7. **deviceId is permanent.** Generated once via `config.EnsureDeviceID`, stored in `kcd.toml`. Never regenerate. It is the stable identity used for cert fingerprint pairing.
8. **Self-signed TLS, `InsecureSkipVerify: true`.** Authentication happens via the SHA-256 fingerprint stored in `devices.json` after pairing — not via CA chain.
9. **`Plugin.Handle()` must return immediately.** Any D-Bus call, subprocess (`exec.Command`), or disk I/O must be spawned in a goroutine *inside* the plugin. Blocking `Handle()` stalls the entire TCP read loop for that device.
10. **No deprecated packages.** No `ioutil` (use `os`/`io`). No `log` (use `go.uber.org/zap`). No `cobra` (use `urfave/cli/v2`).
11. **Keep `Body` as `json.RawMessage` in the router.** Plugins unmarshal their own body types. The packet router never touches body content.
12. **One goroutine per connection.** Dispatch is sequential per device — one packet handled at a time. This is intentional; it removes the need for per-plugin locks.
13. **Cap incoming payload size with `io.LimitReader`.** Never trust the `payloadSize` field from the remote device without a cap.
14. **TLS handshake order:** exchange plaintext identity → upgrade to TLS (initiator = TLS server, acceptor = TLS client) → send full identity over TLS. The inverted TLS roles are a KDE Connect protocol requirement.

---

## Architecture Invariants

These structural constraints must hold at all times:

| Invariant | Why |
|---|---|
| `internal/protocol/` has **zero external imports** (stdlib only) | Protocol types are used everywhere; external deps would create cycles |
| `internal/config/` only imports `github.com/BurntSushi/toml` | Config must stay lean and cycle-free |
| `internal/plugin/plugin.go` only imports `internal/protocol` and `internal/device` | Plugins never import each other |
| Plugins are registered in `daemon.go`, never in their own `init()` | Explicit, ordered, conditional on config |
| `pkg/client/` only imports `internal/ipc` from the `internal/` tree | Public client API must not depend on internals beyond the IPC protocol |
| All binaries built with `CGO_ENABLED=0` | Required for static distribution |

---

## Key File Locations

| Purpose | Path |
|---|---|
| Config file | `~/.config/kcd/kcd.toml` (`$XDG_CONFIG_HOME/kcd/kcd.toml`) |
| Device state (persisted pairs) | `~/.local/state/kcd/devices.json` (`$XDG_STATE_HOME/kcd/devices.json`) |
| TLS cert / key | `~/.config/kcd/cert.pem`, `~/.config/kcd/key.pem` |
| IPC Unix socket | `/run/user/<uid>/kcd/kcd.sock` (`$XDG_RUNTIME_DIR/kcd/kcd.sock`) |
| Downloaded files | `~/Downloads/kcd/` (overridable via `download_dir` in config) |
| systemd user unit | `~/.config/systemd/user/kcd.service` |

> **Note:** The socket lives in a `kcd/` subdirectory of the runtime dir, not directly in `/run/user/<uid>/`.

---

## Daemon Startup Order

`daemon.Run()` wires everything in this exact sequence. Preserve the order when modifying startup:

1. Build `zap.Logger` from config log level
2. Load or generate TLS certificate (`cert.LoadOrGenerate`)
3. Create event bus (`events.NewBus`)
4. Create device registry (`device.NewRegistry`) and load persisted state from `devices.json`
5. Create plugin registry (`plugin.NewRegistry`)
6. Register all enabled plugins — **pair plugin always first**, then the rest conditioned on `cfg.Plugins.*`
7. Create IPC handler (`ipc.NewHandler`) and register per-plugin IPC command handlers via `handler.Register`
8. Start IPC server in a goroutine (`ipc.NewServer → Listen`)
9. Build identity packet (`protocol.NewIdentityPacket`) using `plugins.Capabilities()`
10. Start transport layer in a goroutine (`runTransport` → TCP listener + discovery broadcaster + mDNS)
11. Send `READY=1` to `$NOTIFY_SOCKET` if present (systemd sd_notify)
12. Block on `<-ctx.Done()`

---

## Plugin System

```
⚠  CONSTRUCTORS: Every plugin now requires bus *events.Bus and
logger *zap.Logger. Never use struct literals (&battery.BatteryPlugin{})
— always call the constructor. The compiler will catch this but the
error message may be confusing.
```

### Constructor Signature Reference

| Plugin | Correct constructor signature |
|---|---|
| Battery | `battery.NewBatteryPlugin(cfg config.BatteryConfig, bus *events.Bus, logger *zap.Logger) *BatteryPlugin` |
| Notification | `notification.NewNotificationPlugin(cfg config.NotificationPluginConfig, bus *events.Bus, tlsConfig *tls.Config, logger *zap.Logger) *NotificationPlugin` |
| Share | `share.NewSharePlugin(cfg config.ShareConfig, tlsConfig *tls.Config, logger *zap.Logger) *SharePlugin` |
| SFTP | `sftp.NewSftpPlugin(cfg config.SFTPConfig, bus *events.Bus, logger *zap.Logger) *SftpPlugin` |
| Ping | `ping.NewPingPlugin(cfg config.PingConfig, bus *events.Bus, logger *zap.Logger) *PingPlugin` |
| Pair | `pair.NewPairPlugin(devices *device.Registry, localCert *x509.Certificate, autoAccept bool, cfg config.PairingConfig, onStateChanged func(), bus *events.Bus, logger *zap.Logger) *PairPlugin` |
| Mousepad | `mousepad.NewMousepadPlugin(cfg config.MousepadConfig, logger *zap.Logger) *MousepadPlugin` |
| SystemVolume | `systemvolume.NewSystemVolumePlugin(bus *events.Bus, logger *zap.Logger) *SystemVolumePlugin` |

### Interface

```go
type Plugin interface {
    Name()          string
    IncomingTypes() []string   // packet types this plugin handles inbound
    OutgoingTypes() []string   // packet types it sends outbound
    Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error
    OnConnect(dev device.Sender)
    OnDisconnect(dev device.Sender)
    Timeout()       time.Duration
}
```

### Adding a new plugin — checklist

A plugin that needs both a packet handler *and* a CLI command requires changes in two places:

**1. Create `internal/plugins/<name>/<name>.go`**

- Implement the `Plugin` interface
- `Handle()` must return immediately (spawn goroutines for any blocking work)
- Publish events via `bus.Publish(events.TypeXxx, dev.ID(), payload)` for anything the CLI's `watch` command should expose
- If the plugin calls D-Bus or subprocesses, handle failure gracefully — log and return, don't crash

**2. Register in `daemon.go`**

```go
// Plugin handler (packet routing)
if cfg.Plugins.MyPlugin {
    plugins.Register(myplugin.NewMyPlugin(bus, logger))
}

// IPC command handler (CLI → daemon bridge), if the plugin exposes CLI actions
if cfg.Plugins.MyPlugin {
    handler.Register(ipc.CmdMyAction, func(req ipc.Request) ipc.Response {
        var p ipc.DevicePayload
        if err := json.Unmarshal(req.Payload, &p); err != nil {
            return ipc.Response{OK: false, Error: "invalid payload"}
        }
        pl, ok := plugins.GetByName("MyPlugin")
        if !ok {
            return ipc.Response{OK: false, Error: "myplugin not enabled"}
        }
        dev, ok := devices.Get(p.DeviceID)
        if !ok {
            return ipc.Response{OK: false, Error: "device not found"}
        }
        if err := pl.(*myplugin.MyPlugin).DoThing(dev); err != nil {
            return ipc.Response{OK: false, Error: err.Error()}
        }
        return ipc.Response{OK: true}
    })
}
```

**3. Add config toggle in `internal/config/config.go`**

```go
type PluginConfig struct {
    // ...
    MyPlugin bool `toml:"myplugin"`
}
```

Default it to `true` in `Defaults()`.

If the plugin needs specific settings beyond a boolean toggle, add a new struct to `internal/config/config.go` (e.g. `type MyPluginConfig struct { ... }`) and include it in the main `Config` struct.

**4. Add IPC command constant in `internal/ipc/proto.go`**

```go
const CmdMyAction = "my_action"
```

**5. Add client method in `pkg/client/client.go`**

**6. Add CLI subcommand in `cmd/kcd/main.go`**

**7. Add config field to `packaging/kcd.example.toml`**

### Plugin lookup

To get a plugin and type-assert to its concrete type from an IPC handler:

```go
pl, ok := plugins.GetByName("Share")
if !ok { /* plugin disabled */ }
sharePl := pl.(*share.SharePlugin)
```

### Plugin capabilities in identity

`plugins.Capabilities()` returns `(incoming, outgoing []string)` — the union of all registered plugins' `IncomingTypes()` / `OutgoingTypes()`. These are sent in the identity packet so the remote device knows what this daemon supports.

---

## IPC Protocol

The daemon listens on a Unix socket. All messages are newline-delimited JSON.

**Request:**
```json
{"cmd": "pair", "payload": {"deviceId": "a1b2_..."}}
```

**Response:**
```json
{"ok": true}
{"ok": false, "error": "device not found"}
{"ok": true, "data": [...]}
```

**Event stream (`watch`):** The `CmdWatch` command keeps the socket connection open and streams `events.Event` as NDJSON. Filters are sent in the payload:
```json
{"cmd": "watch", "payload": {"events": ["battery.update", "notification"]}}
```

The `kcd watch` CLI command reconnects automatically on disconnect with exponential backoff (1s → 30s max).

---

## Discovery

Discovery is dual-mode and runs concurrently. Both paths call the same `onDeviceFound` callback and feed into the same transport dial logic.

| Method | How | When it works |
|---|---|---|
| UDP broadcast | Sends identity to `255.255.255.255:1716` + per-interface directed broadcasts | Same LAN, simple home networks |
| mDNS / Zeroconf | Registers `_kdeconnect._udp.local.` via `grandcat/zeroconf`; browses for peers | Restricted networks, Docker, corporate Wi-Fi, newer Android |

The broadcast interval is adaptive. When `shouldReduce()` returns true (all paired devices already connected), the UDP interval increases to 60 seconds. Setting `enable_broadcast = false` in config disables UDP entirely — the daemon reaches 0.0% idle CPU. Paired phones reconnect automatically via remembered IP.

---

## Event Bus

`events.Bus` is a non-blocking fan-out pub/sub. Plugins publish; IPC watch streams and internal subscribers receive.

```go
bus.Publish(events.TypeBatteryUpdate, dev.ID(), map[string]any{
    "charge":   85,
    "charging": true,
})
```

Rules:
- Subscriber channels have capacity 64. If a slow subscriber fills its channel, events are **dropped** (with a `zap.Warn`), never blocked.
- Filters: `bus.Subscribe(events.TypeBatteryUpdate, events.TypeNotification)` — empty filter = all events.
- Always call `sub.Close()` when done to avoid goroutine leaks.

```
bus.Subscribe takes capacity as its first argument:
  sub := bus.Subscribe(0, events.TypeBatteryUpdate)       // default cap (64)
  sub := bus.Subscribe(events.WatchSubscriberCap, ...)    // 256, for watch streams

Never pass filters as the first argument. The old zero-argument form no
longer compiles after Phase 5-A.
```

---

## Device State Machine

```
Unpaired
   ├─ local initiates ──► PairRequested
   │                            │
   │                      peer accepts ──► Paired
   │                      peer rejects ──► Unpaired
   │
   └─ peer initiates ──► PairRequestedByPeer
                               │
                         local accepts ──► Paired
                         local rejects ──► Unpaired
```

`device.State` is protected by `sync.RWMutex`. Always use `dev.State()` and `dev.SetState()`.

Device state is persisted to `devices.json` on every change. `DeviceInfo` fields that are saved: `ID`, `Name`, `Type`, `State`, `CertFP`, `LastSeen`.

---

## Known Gaps (do not regress these as "done")

| Area | Status |
|---|---|
| **SMS receive** | Not implemented. `SMSPlugin.IncomingTypes()` returns `[]string{}`. Sending SMS works. This gap is known and accepted — no plans to implement. |
| **Mousepad keyboard** special keys | Intentionally unhandled — absent from the KDE Connect spec. |

---

## Common Issues

| Symptom | Cause | Fix |
|---|---|---|
| Device not discovered | Firewall blocking UDP 1716 | `ufw allow 1716/udp` — check with `nc -ulvp 1716` |
| Device discovered but can't connect | Firewall blocking TCP 1716 | `ufw allow 1716/tcp` |
| File transfer stalls | Firewall blocking side-channel ports | `ufw allow 1739:1764/tcp` |
| TLS handshake fails | `InsecureSkipVerify` not set on one side | Ensure `cert.TLSConfig()` is used on both dial and accept paths |
| Plugin receives no packets | Plugin not registered | Check `plugins.Capabilities()` output at startup with `--log-level=debug` |
| `kcd devices` returns nothing | Daemon not running or wrong socket path | `systemctl --user status kcd`; check `cfg.SocketPath` matches |
| Binary is dynamically linked | CGO leaked in a dependency | `CGO_ENABLED=0 go build`; check with `file kcd` |
| OOM on large file transfer | File buffered instead of streamed | Use `io.LimitReader` + `io.Copy` in share plugin |
| Cert fingerprint mismatch loop | Phone was reinstalled or cert regenerated | Delete the device entry from `devices.json`, re-pair |
| MPRIS plugin disabled silently | D-Bus session bus not available | Expected — MPRIS logs a `Warn` and continues without it |
| mDNS not advertising | `grandcat/zeroconf` registration error | Check for port conflicts on mDNS port 5353; log level debug shows the error |
| Desktop notifications show no icon | `libnotify < 0.8.0` | Upgrade to libnotify ≥ 0.8 (Ubuntu 22.04+, Fedora 36+) |
| `kcd sftp mount` shows "permission denied" | sshfs path misconfiguration | Ensure kcd version includes the chroot fix — mount root is `user@ip:` not `user@ip:/storage/emulated/0` |
| Paired device never auto-reconnects | Broadcast disabled and no IP cached | Connect manually once (`kcd connect <ip>`) to prime `LastIP`; subsequent drops will auto-reconnect |
| `battery.update` events never arrive | Old binary before Phase 2 fix | Battery plugin now requires `NewBatteryPlugin(bus, logger)` — rebuild |

---

## References

- KDE Connect protocol spec: https://valent.andyholmes.ca/documentation/protocol.html
- KDE Connect meta / plugin list: https://github.com/KDE/kdeconnect-meta
- `godbus` (D-Bus bindings): https://pkg.go.dev/github.com/godbus/dbus/v5
- MPRIS2 spec: https://specifications.freedesktop.org/mpris-spec/latest/
- `grandcat/zeroconf` (mDNS): https://github.com/grandcat/zeroconf
- `urfave/cli/v2` (CLI framework): https://cli.urfave.org/v2/
- `go.uber.org/zap` (logging): https://pkg.go.dev/go.uber.org/zap