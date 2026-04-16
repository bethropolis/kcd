# kcd — Agent Instructions

## Project Identity

**`kcd`** is a headless Go daemon implementing KDE Connect protocol v8. Single binary (daemon + CLI via subcommands). Unix socket IPC. No GUI. D-Bus only for MPRIS plugin. Static binary distributed via GoReleaser.

- **Language:** Go 1.22+ with `CGO_ENABLED=0` (no cgo anywhere)
- **Module:** `github.com/bethropolis/kcd`
- **Key constraint:** Memory efficiency — never buffer what can be streamed. Reuse allocations (packet pool, single `bufio.Reader` per connection).

---

## Absolute Rules

1. **CGO_ENABLED=0 always.** `ldd kcd` must print "not a dynamic executable".
2. **Never buffer file payloads.** Use `io.Copy(dst, io.LimitReader(conn, payloadSize))`. Buffering causes OOM.
3. **Never write to conn directly.** Always use `device.Send(packet)` — the send channel is the only goroutine-safe write path.
4. **One `bufio.Reader` per connection, allocated once.** Never re-allocate in packet loop.
5. **Packet pool.** Use `sync.Pool` for `*Packet`. Return after plugin finishes.
6. **Protocol version = 8** in identity packets (hardcoded).
7. **deviceId persists.** Generate once, store in config/state. Never regenerate.
8. **Self-signed TLS.** `InsecureSkipVerify: true`. Auth via cert fingerprints stored after pairing.
9. **Plugin Handle() must not block.** Spawn goroutine for exec/D-Bus/disk ops, return immediately.
10. **No deprecated packages.** No `ioutil` (use `os`/`io`). No `log` (use `zap`). No `cobra` (use `urfave/cli/v2`).
11. **Keep `Body` as `json.RawMessage` in router.** Let plugins unmarshal their own types.
12. **Goroutine-per-connection**, not per-packet.
13. **Always cap `payloadSize` with `io.LimitReader`.** Don't trust remote values.
14. **Protocol v8 Handshake.** Exchange initial identity, then upgrade to TLS, then send full identity.

---

## Architecture Invariants

**These must hold. If you violate one, stop and reconsider:**

1. `internal/protocol/` has **zero external imports** (stdlib only)
2. `internal/config/` only imports `BurntSushi/toml` (no other externals)
3. `internal/plugin/plugin.go` imports only `internal/protocol` and `internal/device` — plugins never import each other
4. Plugins registered in `daemon.go`, not in their own `init()` functions
5. `pkg/client/` only imports `internal/ipc/proto.go` from `internal/`
6. Binary always built with `CGO_ENABLED=0`

---

## Key File Locations

| Purpose | Path |
|---------|------|
| Config | `~/.config/kcd/kcd.toml` |
| Device state | `~/.local/state/kcd/devices.json` |
| TLS cert/key | `~/.config/kcd/cert.pem`, `key.pem` |
| IPC socket | `/run/user/<uid>/kcd.sock` |
| Downloads | `~/Downloads/kcd/` (configurable) |

---

## Common Issues

| Symptom | Cause | Fix |
|---------|-------|-----|
| Device not discovered | Firewall | `ufw allow 1716/udp` |
| TLS fails | Missing `InsecureSkipVerify` | Set on both sides |
| Packet routing silent | Plugin not registered | Log `plugin.Registry().All()` at startup |
| File transfer OOM | Buffering | `io.LimitReader` + `io.Copy` |
| Binary not static | CGO enabled | `CGO_ENABLED=0 go build` |
| Cert mismatch loop | Device reinstalled | Delete fingerprint, re-pair |

---

## References

- Protocol spec: https://valent.andyholmes.ca/documentation/protocol.html
- KDE Connect meta: https://github.com/KDE/kdeconnect-meta
- D-Bus: https://pkg.go.dev/github.com/godbus/dbus/v5
- MPRIS: https://specifications.freedesktop.org/mpris-spec/latest/
