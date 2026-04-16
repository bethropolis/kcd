# kcd — Roadmap

## Phases Overview

| Phase | Name | Goal | Est. Effort |
|-------|------|------|-------------|
| 1 | Protocol Core | Packet framing, TLS, identity | Small |
| 2 | Discovery + Device Manager | UDP discovery, connection lifecycle | Medium |
| 3 | Pairing + IPC | Pair state machine, Unix socket, kcdctl | Medium |
| 4 | Core Plugins | Battery, Notification, Clipboard, RunCommand | Medium |
| 5 | Share Plugin | File transfer, side-channel TCP, streaming | Medium |
| 6 | MPRIS Plugin | D-Bus media control | Small |
| 7 | Distribution | GoReleaser, systemd, OCI, AUR, .deb | Small |

---

## Not In Scope (Ever)

| Item | Reason |
|------|--------|
| GUI / Tray icon | Daemon is headless by design; GUI is someone else's job using the IPC socket |
| SMS / MMS | Requires deep Android integration, legally and technically complex |
| Remote filesystem (SFTP) | Heavy dependency (ssh server), out of scope for a minimal daemon |
| Windows / macOS support | Linux-only; the protocol transport is fine but clipboard/notification/MPRIS are Linux-specific |
| Bluetooth transport | WiFi LAN transport covers 99% of use cases; Bluetooth adds complexity with minimal gain |
| GNOME/KDE Shell integration | IPC socket is the integration point; shell plugins are a separate project |
| Auto-update mechanism | Handled by package managers (pacman, apt, Podman pull) |

---

## Phase 1 — Protocol Core

**Goal:** Parse and emit KDE Connect packets over TLS. No networking yet —
just the data structures, framing, cert generation, and transport types.

**In scope:**
- `Packet` struct with `json.RawMessage` body and `sync.Pool`
- `ReadPacket` / `WritePacket` with `bufio.Reader`
- Self-signed TLS cert generation and load
- `Conn` wrapping `*tls.Conn` + `*bufio.Reader`
- `Listener` wrapping `tls.Listen`
- Identity packet constructor
- Pair packet constants

**Out of scope:** UDP, device state, plugins, IPC, config file.

**Success criteria:**
- `go test ./internal/protocol/... ./internal/cert/... ./internal/transport/...` passes
- `WritePacket` → `ReadPacket` round-trip produces identical struct
- TLS pair over `net.Pipe()` exchanges identity packets successfully

**Key implementation notes:**
- `Body` stays as `json.RawMessage` — do not unmarshal in the router
- `bufio.Reader` allocated once per `Conn`, not per packet
- `packetPool` must zero out the struct before returning from pool

---

## Phase 2 — Discovery + Device Manager

**Goal:** Discover devices on the LAN, establish connections, manage lifecycle.

**In scope:**
- UDP broadcaster sending identity every 30s
- UDP listener calling `OnDeviceFound(ip, port, identity)`
- `Device` struct with pairing state, send channel, writer goroutine, read loop
- `Registry` with `sync.Map` keyed by deviceId
- Device state persistence (JSON file)
- Reconnect loop with exponential backoff (2s → 60s)
- Cert fingerprint mismatch detection

**Out of scope:** Pairing user flow, plugins, IPC.

**Success criteria:**
- `kcd` logs identity exchange with real Android KDE Connect device
- Reconnect happens automatically after phone goes out of range and returns
- `Device.Send` drops and logs when channel full — confirmed by unit test

**Key implementation notes:**
- The UDP broadcast must include `tcpPort` in the identity body
- On receiving UDP identity, check if deviceId already connected before dialing
- Writer goroutine exits cleanly when send channel is closed (defer close on disconnect)

---

## Phase 3 — Pairing + IPC

**Goal:** Pair devices, expose state and control via Unix socket to kcdctl.

**In scope:**
- Pair state machine: UNKNOWN → PAIR_REQUESTED → PAIRED
- Pair packet send/receive handlers
- Cert fingerprint stored on pair, verified on reconnect
- Unix socket IPC server with JSON request/response protocol
- `kcdctl devices`, `pair`, `unpair`, `ping` subcommands
- `pkg/client` IPC client library

**Out of scope:** Plugin commands over IPC (those come in Phase 4+).

**Success criteria:**
- `kcdctl devices` lists connected phone with PAIRED/UNKNOWN state
- `kcdctl pair <id>` → user accepts on phone → both show PAIRED
- `kcdctl ping <id>` → phone shows ping notification
- Re-pairing after cert mismatch works without restarting daemon

**Key implementation notes:**
- IPC protocol is newline-delimited JSON (same framing as KDE Connect packets)
- Socket path defaults to `/run/user/<uid>/kcd.sock`; configurable
- `kcdctl` connects, sends one request, reads one response, exits
- For streaming events (future), use a subscribe command with persistent connection

---

## Phase 4 — Core Plugins

**Goal:** Battery, notification mirroring, clipboard sync, remote command execution.

**In scope:**
- Battery plugin: recv charge + isCharging, update device state, queryable via IPC
- Notification plugin: recv notification, exec `notify-send`, truncate at 512 bytes
- Clipboard plugin: recv clipboard, exec `xclip` or `wl-copy` based on session type
- Clipboard plugin: outgoing push via `kcdctl clipboard push`
- RunCommand plugin: recv key, exec configured command, send command list on request
- `kcdctl battery <id>`, `clipboard push`, `runcommand list/exec`

**Out of scope:** File transfer (Phase 5), MPRIS (Phase 6).

**Success criteria:**
- Phone battery % shown via `kcdctl battery <id>`
- Phone notification appears on desktop within 2 seconds
- Copy on phone → paste on desktop works
- Configured command triggered from phone via RunCommand
- All plugins unit-tested with `fakeDevice`

**Key implementation notes:**
- All plugin `Handle()` functions must return immediately; use goroutine for exec
- Detect clipboard session type by checking `WAYLAND_DISPLAY` env var
- Sanitize notification app name before passing to exec (strip shell metacharacters)
- RunCommand uses `exec.CommandContext` with 10s timeout

---

## Phase 5 — Share Plugin

**Goal:** Receive and send files over side-channel TCP without memory buffering.

**In scope:**
- Recv: parse `payloadTransferInfo.port`, dial side-channel TCP in goroutine
- Recv: `io.Copy(file, io.LimitReader(sideConn, payloadSize))`
- Recv: filename sanitizer (no `..`, no `/`)
- Recv: duplicate filename handling (`file_1.pdf`, `file_2.pdf`)
- Send: listen on random port, send share packet, serve file over side-channel
- `kcdctl send file <deviceId> <path>`

**Out of scope:** Sending URLs (can be added later as addendum), progress reporting.

**Success criteria:**
- 50MB file received from phone appears in `DownloadDir` with correct SHA256
- `kcdctl send file <id> /path/file.pdf` delivers to phone
- Sending `../etc/passwd` as filename is rejected (sanitizer test)
- No measurable RSS increase during large file transfer (stream confirmed)

**Key implementation notes:**
- `io.LimitReader` is mandatory — do not trust `payloadSize` from remote
- Side-channel TCP connection must be established promptly (within 5s) or sender times out
- Open `DownloadDir` with `os.MkdirAll(path, 0755)` at startup, not on first transfer

---

## Phase 6 — MPRIS Plugin

**Goal:** Let phone control desktop media players via D-Bus MPRIS.

**In scope:**
- List `org.mpris.MediaPlayer2.*` D-Bus names on request
- Handle play/pause/next/prev/stop/seek/setVolume from phone
- Push now-playing metadata to phone on `PropertiesChanged` signal
- No-op gracefully when no player is running

**Out of scope:** Album art transfer, playlist management.

**Success criteria:**
- Phone media controls control mpv or VLC playing on desktop
- Now-playing track name appears on phone
- No error logs when no media player is running

**Key implementation notes:**
- Use `godbus/dbus/v5` — pure Go, no cgo
- Subscribe to `org.freedesktop.DBus.Properties.PropertiesChanged` on player object
- MPRIS plugin is the only plugin that imports `godbus/dbus/v5`
- Document in README: MPRIS requires D-Bus session; does not work in `scratch` OCI
  container without host D-Bus socket mount

---

## Phase 7 — Distribution

**Goal:** Ship via GoReleaser to all four targets.

**In scope:**
- `.goreleaser.yaml` building both binaries for linux/amd64 + linux/arm64
- nfpms producing `.deb` with systemd user unit
- AUR `kcd-bin` PKGBUILD via GoReleaser aurs block
- OCI image (`scratch` base, static binary only)
- `dist/kcd.service` systemd user unit
- `dist/kcd.example.toml` annotated example config
- `Makefile` with `build`, `test`, `lint`, `release-dry-run`, `install`
- `README.md` with install instructions per distribution method

**Out of scope:** Homebrew, Nix, Flatpak, Windows.

**Success criteria:**
- `goreleaser release --snapshot --clean` succeeds without errors
- `ldd kcd` → "not a dynamic executable" on both architectures
- OCI image size < 15MB
- `apt install ./kcd_*_amd64.deb` installs binary + systemd unit
- `yay -S kcd-bin` installs on Arch
- `podman run ghcr.io/yourname/kcd:latest --help` prints usage

**Key implementation notes:**
- GoReleaser `ldflags: ["-s -w -X main.version={{.Version}}"]` strips debug info
- AUR requires SSH key configured in CI; use `GORELEASER_KEY` env var
- OCI image uses `buildx` with `--platform=linux/amd64,linux/arm64` for multi-arch
- The `.deb` `postinst` script should NOT auto-enable the service; document manual `systemctl --user enable kcd`
