# kcd — Agent Instructions

Instructions for AI coding agents, and a fast orientation for human
contributors. Human contribution process: [CONTRIBUTING.md](CONTRIBUTING.md).
Threat model and reporting: [SECURITY.md](SECURITY.md).

---

## Project Identity

**`kcd`** is a headless Go daemon implementing the KDE Connect protocol v8.
One static binary is both the daemon (`kcd daemon`) and the CLI client (every
other subcommand), built on `urfave/cli/v2`.

- The daemon serves a Unix socket (newline-delimited JSON). The CLI, `pkg/client` and third-party tools are all clients of it.
- There's no GUI. The daemon links D-Bus (`godbus`) only in the MPRIS plugin. Other desktop integration goes through external tools (`notify-send`, `gdbus`, `pactl`/`wpctl`, `loginctl`…). Every desktop dependency must degrade gracefully (log and continue) when it's missing.
- **Language:** Go 1.25 (pinned in `mise.toml`, with golangci-lint and hk).
- **Module:** `github.com/bethropolis/kcd`. **License:** MIT.
- **Core constraints:** a static binary (`CGO_ENABLED=0`), memory efficiency (stream, never buffer payloads), and zero idle cost (no timers or polling when nothing is happening).

---

## Absolute Rules

Never violate these. If a change would break one, stop and reconsider the approach.

1. **`CGO_ENABLED=0` for every shipped binary.** `file bin/kcd` must report a statically linked executable. (Race-detector *tests* need `CGO_ENABLED=1`; that's for tests only.)
2. **Never buffer file payloads.** Stream with `io.Copy(dst, io.LimitReader(conn, size))`.
3. **Cap every size a peer declares.** `payloadSize` and packet lengths are untrusted. Enforce a hard maximum, and treat a short read as failure.
4. **Never write to a `net.Conn` of a device directly.** Send with `dev.Send(pkt)`. The device's writer goroutine is the only write path.
5. **One `bufio.Reader` per connection**, allocated at accept or dial time, never inside the read loop.
6. **Use the packet pool.** `protocol.AcquirePacket()` / `protocol.ReleasePacket(pkt)`. A packet is released after the plugin's `Handle` returns, so goroutines must copy `pkt.Body` before using it.
7. **Protocol version is 8** (`protocol.ProtocolVersion`). Never send another value.
8. **The device ID is permanent.** It's generated once and bound to the certificate's CN. Never regenerate or rewrite it, or every pairing breaks.
9. **TLS uses self-signed certs with `InsecureSkipVerify: true`.** Authentication is the SHA-256 fingerprint pinned in `devices.json` at pairing, not a CA chain. Never accept a paired device whose fingerprint doesn't match.
10. **`Plugin.Handle()` returns immediately.** D-Bus calls, subprocesses, disk and network I/O run in a goroutine. **That goroutine must not use `Handle`'s `ctx`**: the dispatcher cancels it when `Handle` returns. Use the plugin's lifetime context, or `context.WithoutCancel(ctx)` plus its own timeout.
11. **No deprecated or forbidden packages.** No `io/ioutil`, no stdlib `log`, no direct `go.uber.org/zap` (use `internal/log`; depguard enforces the zap rule), no `cobra` (use `urfave/cli/v2`).
12. **The router never decodes bodies.** `Packet.Body` is `json.RawMessage`; each plugin decodes its own types.
13. **One reader goroutine per connection; dispatch is sequential per device.** That's intentional and removes the need for per-plugin locks on the receive path. Don't parallelise dispatch.
14. **TLS handshake order:**
    1. exchange the plaintext identity;
    2. upgrade to TLS with **inverted roles** (the TCP dialer is the TLS *server*, the acceptor is the TLS *client*);
    3. send the full identity again over TLS.
15. **Only pairing packets go to and from unpaired devices.** Android unpairs a device that sends it anything else.
16. **Compatibility is a feature.** Existing `kcd.toml` files, `devices.json`, IPC clients and CLI flags must keep working:
    - New config keys are optional and default to current behaviour; old keys become aliases, never removed.
    - IPC changes are additive.
    - The daemon never rewrites the user's config file.

---

## Architecture Invariants

| Invariant | Why |
|---|---|
| `internal/protocol` imports the stdlib only | Wire types are used everywhere; dependencies there create cycles |
| `internal/config` imports only `github.com/BurntSushi/toml` and `internal/protocol` | Config must stay lean and cycle-free |
| `internal/plugin` (interface and registry) imports only `internal/protocol` and `internal/device` | Plugins depend on the interface, not on each other |
| **A plugin never imports another plugin.** Shared code goes in a neutral package (`internal/transport`, or a new `internal/<name>`) | Keeps plugins independently removable |
| Plugins are registered in `internal/daemon/plugins.go` (`setupPlugins`), pair first, never in `init()` | Explicit, ordered, conditional on config |
| `pkg/client` imports from `internal/` only `device`, `events`, `ipc` and `plugins/contacts` | The public API must not drag in daemon internals. Shrink this list over time; never grow it |
| All shipped binaries are built with `CGO_ENABLED=0` | Static distribution |

---

## Repository Map

| Path | What lives there |
|---|---|
| `cmd/kcd/` | CLI entry point: `main.go` (app, global flags, `daemon`, `doctor`, `status`) and `cli_<area>.go` per command group; `format_event.go` renders `watch` output |
| `internal/daemon/` | `daemon.Run` wiring, `plugins.go` (registration), `ipc_routes_<area>.go` (IPC handlers), `transport*.go` (listen, dial, handshake, reconnect) |
| `internal/protocol/` | Packet framing, pool, identity, `FlexBool`, constants |
| `internal/transport/` | TLS connection wrapper, side-channel dial, idle-timeout conn, plaintext identity |
| `internal/discovery/` | UDP broadcast and listener, mDNS advertise and browse, broadcaster ownership controller |
| `internal/device/` | `Device` (state, send channel, read and write loops), `Registry`, `devices.json` load and save |
| `internal/cert/` | Cert generation and loading, TLS config, fingerprints, pairing verification key |
| `internal/plugin/` | `Plugin` interface, registry, and dispatch (timeouts, panic recovery) |
| `internal/sysexec/` | Injectable subprocess runner and cached executable lookup |
| `internal/desktop/` | Shared clipboard and audio backends used by independent plugins |
| `internal/plugins/<name>/` | One package per KDE Connect plugin |
| `internal/ipc/` | IPC wire types (`proto.go`), server, watch streaming, built-in handlers |
| `internal/events/` | Event bus and event type constants |
| `internal/config/` | Config struct, defaults, load, validation, XDG paths |
| `internal/log/` | Logging facade over zap (the only package that may import zap) |
| `internal/doctor/` | `kcd doctor` environment checks |
| `internal/testutil/` | `MockPeer` (fake KDE Connect peer), `StartTestDaemon` |
| `internal/integration/` | End-to-end tests (build tag `integration`) |
| `pkg/client/` | Public Go client for the IPC socket |
| `docs/` | `ARCHITECTURE.md`, `IPC_PROTOCOL.md`, `CLIENT_GUIDE.md`, `CLI.md`, `CONTAINER.md` |
| `packaging/` | systemd units (user, system, socket-activated), completions, `kcd.example.toml`, firewall rules, Nautilus extension |
| `desktop-integration/` | Waybar and other desktop integrations |

---

## Daemon Startup Order

`daemon.Run()` (`internal/daemon/daemon.go`) wires things in this order. Keep
the order when modifying startup:

1. Build the logger: `log.New(cfg.LogLevel)`.
2. Load or generate the TLS certificate (`cert.LoadOrGenerate`), and build `cert.TLSConfig`.
3. Create the event bus (`events.NewBus`).
4. Create the device registry (`device.NewRegistry`), load `devices.json` (with migrations), and prune stale unpaired devices.
5. Create the plugin registry (`plugin.NewRegistry`).
6. Register plugins (`setupPlugins`): **pair first**, then each one enabled in `cfg.Plugins`.
7. Build the identity packet from `plugins.Capabilities()`.
8. Create the broadcaster controller. Start the mDNS advertisement (always on) and the reconnect-broadcast sync on connect/disconnect events.
9. Create the IPC handler (`ipc.NewHandler`), register IPC routes (`registerIPCRoutes`), and set the pair-dial hook.
10. Start the IPC server.
11. Start the transport (`runTransport`): TCP listener, UDP listener, discovery.
12. Send `READY=1` to `$NOTIFY_SOCKET` if set (systemd `Type=notify`).
13. Install the SIGHUP reload handler. Today it reloads commands, notification filters and the log level only.
14. Block on `<-ctx.Done()`.
15. On shutdown, call `SftpPlugin.UnmountAll`, bounded by `shutdownUnmountBudget`. `OnDisconnect` only fires on a dropped connection, so without this every graceful stop would leave mounts live.

---

## Plugin System

### Interface (`internal/plugin/plugin.go`)

```go
type Plugin interface {
    Name() string
    IncomingTypes() []string   // packet types handled inbound
    OutgoingTypes() []string   // packet types sent
    Timeout() time.Duration    // dispatch budget for Handle
    Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error
    OnConnect(dev device.Sender)
    OnDisconnect(dev device.Sender)
}
```

`plugins.Capabilities()` returns the union of all registered plugins'
incoming and outgoing types. Those go in the identity packet, so the peer knows
what kcd supports.

### Constructors

Every plugin has a `New<Name>Plugin(...)` constructor. **Always use it**, never
a struct literal. Signatures differ (most take some of `cfg`, `bus *events.Bus`,
`tlsConfig *tls.Config`, `logger log.Logger`, plus variadic options). The
source of truth is the code:

```bash
grep -rn "^func New[A-Za-z]*Plugin(" internal/plugins/
```

### Writing a plugin handler

- `switch pkt.Type` first. A status packet must never be treated as a command.
- Decode into plugin-owned types. **Inbound optional fields are pointers**, so absent and zero stay distinct.
- **Outbound: no `omitempty` on a zero value that means something** (`false`, `0`, `""` that the receiver acts on).
- Validate every peer field (type, range, length). Peer-controlled strings never become paths, commands or event names without sanitising or allowlisting.
- Android sends some numbers and bools as strings. Use `protocol.FlexBool` (and a flexible int alongside it) to accept both. Always *send* the type the spec says.
- Check units and field names against the spec schemas for every field you touch.
- Publish an event (`bus.Publish(events.TypeX, dev.ID(), payload)`) for anything clients should be able to observe.

### Adding a new plugin: checklist

1. `internal/plugins/<name>/` with `<name>.go` (constructor, Name, types, Timeout, OnConnect/OnDisconnect), `handle.go`, `types.go`.
2. Config toggle in `PluginConfig` (`internal/config/plugins.go`), defaulting to `true` in `Defaults()`, plus a settings struct if needed.
3. Register it in `internal/daemon/plugins.go`.
4. IPC: a constant in `internal/ipc/proto.go`, and a handler in `internal/daemon/ipc_routes_<area>.go` using the `deviceRoute` / `pluginRoute` helpers (`ipc_route_helpers.go`).
5. A client method in `pkg/client/`, and a CLI command in `cmd/kcd/cli_<area>.go` with shell completions in `packaging/`.
6. Event types in `internal/events/bus.go` (add them to `All()`; opt-in types to `OptInOnly()`).
7. Docs: `packaging/kcd.example.toml`, `docs/IPC_PROTOCOL.md` (commands §3, events §5, packets §6/§7), `docs/CLIENT_GUIDE.md`, `docs/CLI.md`. Cover the error cases: device disconnected, plugin disabled, malformed payload.
8. Tests (see Testing).

---

## IPC Protocol (summary; full spec in `docs/IPC_PROTOCOL.md`)

- Socket: `$XDG_RUNTIME_DIR/kcd/kcd.sock`, mode 0600. NDJSON. One request per connection.
- Request: `{"cmd":"pair","payload":{"deviceId":"..."}}`
- Response: `{"ok":true}` · `{"ok":true,"data":...}` · `{"ok":false,"error":"..."}`
- `watch` keeps the connection open and streams `events.Event` as NDJSON. Filters go in the payload: `{"cmd":"watch","payload":{"events":["battery.update"]}}`. The first event is always `state.snapshot`. The `kcd watch` CLI reconnects with backoff (1 s → 30 s).
- **IPC changes are additive only:** new commands, new optional fields, new event types. Never change the meaning of an existing command or field. Every command and event must be documented in `docs/IPC_PROTOCOL.md`.

## Event Bus

- `events.Bus` is a non-blocking fan-out. Plugins publish; IPC watchers and internal subscribers receive.
- `bus.Subscribe(capacity, filters...)`: capacity comes **first** (`0` means the default of 64; `events.WatchSubscriberCap` (256) is for watch streams). Always `defer sub.Close()`.
- A full subscriber channel **drops** events (with a warning). Publishing never blocks.
- An empty filter means every event *except* the opt-in types (`events.OptInOnly()`, currently SMS). Those must be named explicitly, because subscribing to them makes the phone push every message. That's a consent decision.
- `bus.HasSubscribers(type)` and `OnSubscriberChange` let plugins run work only while someone is listening. Use them to keep idle cost at zero.

## Discovery & Connection

| Path | Behaviour |
|---|---|
| mDNS advertise (`_kdeconnect._udp`) | Always on; responder only, no timers |
| UDP listener (port 1716) | Always on; feeds sightings to the dial logic |
| UDP broadcast and mDNS browse | **On demand**, reference-counted by owner: `pairing` (`kcd pair` listen mode) and `reconnect` (a paired device is offline) |
| Reconnect | Per-device loop with exponential backoff. Sightings wake it immediately; it gives up after `reconnect.stale_after` until the next sighting |

Dial targets (`LastIP`/`LastPort`) persist in `devices.json`. Paired sightings
dial the sighted address, authenticated after connecting by CN and pinned
fingerprint.

## Device State Machine

```
Unpaired ── local requests ──► PairRequested ── peer accepts ──► Paired
   │                               └─ peer rejects ─────────────► Unpaired
   └──── peer requests ──► PairRequestedByPeer ── user accepts ──► Paired
                                   └─ user rejects ─────────────► Unpaired
Paired ── peer requests pairing again ──► PairRequestedByPeer
```

Pending states expire after `pairing.timeout_secs`, capped at Android's
30-second requester and 25-second receiver windows. A restart restores
pending states as `Unpaired`. Use `dev.State()`, `dev.SetState()`, and
`dev.TransitionState()` for mutex-guarded state access. State changes are
persisted to `devices.json` (`ID`, `Name`, `Type`, `State`, `CertFP`,
`LastSeen`, `LastIP`, `LastPort`).

---

## Comment Policy

**Explain why, never what.** A comment whose deletion loses no information should be deleted.

**Required:**
- Security rationale: what an attacker gains without a validation, and why an input is rejected.
- Protocol facts the code can't show: a wire quirk, an Android-only behaviour, units, a literal error string worth matching on.
- Invariants a plausible future edit would break.
- The reason behind every timeout, retry count or magic number.

**Not wanted:**
- Restating the next line of code.
- Change narration (`used to`, `is now`, `no longer`, `fixed`). Git history owns that.
- Dev-process references: phases, planning documents, issue or spec numbers, review IDs. Those go in commit messages.
- `TODO` / `FIXME` / `XXX` (the `godox` linter rejects them). Track open work outside the code.
- Prose that duplicates this file or `docs/`.

**Godoc stays** on exported identifiers: one line, starting with the identifier (`// Name does X.`). Comments end with a period (`godot` linter).

**A comment block over 8 lines is usually two comments**, a contract and a history. Keep the contract and drop the history. When trimming a block that touches validation, TLS, key handling, timeouts or the wire format, keep its *reason*.

---

## Code Conventions

- **Errors:** `fmt.Errorf("<pkg>: <verb> <object>: %w", err)`. Lowercase, wrapped with `%w`, checked with `errors.Is/As`. Never discard persistence errors. Never panic on peer input, files or config.
- **Logging** (`internal/log`): lowercase messages prefixed with the subsystem (`"mpris: seek failed"`), snake_case keys that reuse existing names (`device_id`, `plugin`, `path`, `port`, `remote_addr`). Never log secrets or private content (SFTP passwords, clipboard text, SMS and notification bodies). Rate-limit anything that can loop.
- **Concurrency:** every goroutine has an owner and an exit. Hold locks only around memory, never across `Send`, I/O or callbacks. No exported mutexes.
- **Timers:** no ticker that runs at idle. Arm on demand and stop when done. Every retry loop backs off and exits on `ctx.Done()`.
- **Processes:** pass an argv slice to `exec.Command`. Never `sh -c` with peer data (only user-configured commands go through a shell). No subprocess per high-frequency event.
- **Files:** write important state to a temp file, then rename. Create new files with `O_EXCL`. Sanitise and confine any path derived from peer data.
- **Config:** new keys go in `internal/config` with defaults in `Defaults()` and validation in `Validate()`, and are documented in `packaging/kcd.example.toml`.
- **Dependencies:** must build with `CGO_ENABLED=0`. Prefer the stdlib for anything under ~50 lines.

## Testing

- Unit tests sit next to the code. Integration tests live in `internal/integration` (`//go:build integration`).
- Helpers:
  - `log.NewTest(t)` gives a test logger; `log.Observe()` captures logs for assertions.
  - `testutil.NewMockPeer` / `DialPipe` / `SendPacket` / `ReadPacket` give a fake KDE Connect peer.
  - `testutil.FakeSender` records plugin packets; `testutil.ExpectEvent` waits for a typed event.
  - `testutil/daemonstart.StartTestDaemon(t, cfg)` gives a daemon plus an IPC client.
- Tests are deterministic: no `time.Sleep` to wait for goroutines; use channels or bounded `select` waits. Helpers call `t.Helper()`. Prefer table tests.
- Code that runs external commands takes an injectable runner, so tests never spawn real `loginctl`, `xdotool` and so on.
- A bug fix comes with a test that fails without the fix.

## Verification (what CI runs)

```bash
gofmt -l .                                              # prints nothing
go vet ./...
golangci-lint run                                       # govet, staticcheck, unused, depguard, godot, godox, misspell...
CGO_ENABLED=1 go test -p 1 -race -count=1 ./...
CGO_ENABLED=1 go test -tags integration -race -count=1 ./internal/integration/...
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/kcd ./cmd/kcd && file bin/kcd
go mod tidy && git diff --exit-code go.mod go.sum
```

`just build`, `just test` and `just integration` wrap the common cases.
`hk check` runs the pre-commit set. If `go` is missing, `mise install`
provides the pinned toolchain. Report failures with their output; never claim
a check passed without running it.

---

## Key File Locations (runtime)

| Purpose | Path |
|---|---|
| Config | `$XDG_CONFIG_HOME/kcd/kcd.toml` (`~/.config/kcd/kcd.toml`), overridable with `--config` / `KCD_CONFIG` |
| TLS cert / key | `~/.config/kcd/cert.pem`, `~/.config/kcd/key.pem` |
| Paired devices | `$XDG_STATE_HOME/kcd/devices.json` (`~/.local/state/kcd/devices.json`) |
| IPC socket | `$XDG_RUNTIME_DIR/kcd/kcd.sock`, in a `kcd/` subdirectory, not directly in the runtime dir |
| Downloads | `~/Downloads/kcd/` (`download_dir`) |
| Album art cache | `$XDG_CACHE_HOME/kcd/art/` |
| SFTP mount points | `$XDG_RUNTIME_DIR/kcd/mnt/` (`$XDG_STATE_HOME/kcd/mnt` without a user session). Never under a bulk-deletable user folder, because `rm -rf` descends into a live mount |
| systemd user unit | `~/.config/systemd/user/kcd.service` (sources in `packaging/`) |

## Known Gaps (don't report these as "done")

| Area | Status |
|---|---|
| **Un-arming the phone** | Not possible. Android's `haveMessagesBeenRequested` is set by any `sms.request*` packet and nothing clears it, so the phone pushes every new message for the life of the connection. kcd's only lever is consent: unfiltered subscribers never receive the opt-in SMS types, so an incidental `kcd watch` doesn't arm the phone. Once armed, ending it requires disabling the plugin on the phone. |

## Common Issues

| Symptom | Cause | Fix |
|---|---|---|
| Device not discovered | Firewall blocks UDP 1716 | `ufw allow 1716/udp`; check with `nc -ulvp 1716` |
| Discovered but can't connect | Firewall blocks TCP 1716 | `ufw allow 1716/tcp` |
| File transfer stalls | Firewall blocks side-channel ports | `ufw allow 1739:1764/tcp` |
| Plugin receives no packets | Plugin not registered or disabled | Check `kcd status` and the capabilities logged at `--log-level=debug` |
| `kcd devices` returns nothing | Daemon not running, or socket path mismatch | `systemctl --user status kcd`; compare `socket_path` |
| Binary dynamically linked | cgo leaked in | Build with `CGO_ENABLED=0`; check with `file` |
| Fingerprint mismatch loop | Phone reinstalled or cert regenerated | Unpair (`kcd unpair <id>`) and pair again |
| MPRIS silently disabled | No D-Bus session bus | Expected: MPRIS logs a warning and continues |
| Desktop notifications without icons | libnotify < 0.8 | Upgrade libnotify |
| Paired device never auto-reconnects | No cached address and no sighting | `kcd connect <ip>` once to prime `LastIP` |


## References

- Protocol spec (official): https://invent.kde.org/network/kdeconnect-meta/-/blob/master/protocol.md (schemas in `schemas/`)
- Android app (reference behaviour): https://invent.kde.org/network/kdeconnect-android
- KDE desktop implementation: https://invent.kde.org/network/kdeconnect-kde
- `godbus`: https://pkg.go.dev/github.com/godbus/dbus/v5 · MPRIS2: https://specifications.freedesktop.org/mpris-spec/latest/
- `libp2p/zeroconf/v2`: https://github.com/libp2p/zeroconf · `urfave/cli/v2`: https://cli.urfave.org/v2/
