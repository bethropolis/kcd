# kcd — Project Guide

Headless Go daemon implementing KDE Connect protocol v8. Single binary, minimal deps, Unix socket IPC, modular plugins.

---

## 1. KDE Connect Protocol

### Three Layers
```
[ UDP broadcast :1716 ]  ← device discovery
[ TLS over TCP :1716  ]  ← secure transport  
[ JSON packets        ]  ← plugin communication
```

### Packet Structure
One line of JSON + `\n`:
```json
{"id":1712345678123,"type":"kdeconnect.battery","body":{"currentCharge":82,"isCharging":false}}
```

| Field | Type | Description |
|-------|------|-------------|
| `id` | int64 | Unix ms timestamp (not used for routing) |
| `type` | string | Plugin identifier (e.g. `kdeconnect.battery`) |
| `body` | object | Plugin-defined payload |
| `payloadSize` | int64 | File transfers only: byte count |
| `payloadTransferInfo` | object | File transfers only: `{"port": 1739}` |

**Critical:** Read until `\n`, parse JSON. Never buffer multiple packets.

### Discovery (UDP)
1. Broadcast identity packet to `255.255.255.255:1716` every 30s
2. Listen on `:1716`, extract `tcpPort` from identity body
3. Dial `remoteIP:tcpPort` with TLS

### Identity Packet
```json
{
  "type": "kdeconnect.identity",
  "body": {
    "deviceId": "a1b2c3d4_e5f6_7890_abcd_ef1234567890",
    "protocolVersion": 8,
    "tcpPort": 1716
  }
}
```

**Protocol version must be 8.** Generate `deviceId` once, persist forever.

### TLS Handshake
Self-signed certs on both sides. Set `InsecureSkipVerify: true`. After pairing, store peer cert SHA256 fingerprint and verify on reconnect.

**Protocol v8 Handshake Flow:**
1. After discovery, devices exchange an initial `kdeconnect.identity` over unencrypted TCP. This packet contains `deviceId`, `protocolVersion`, `targetDeviceId`, and `targetProtocolVersion`.
2. Both sides verify the target values match.
3. The connection is upgraded to TLS.
4. Once secured, both devices send their full identity packet, which includes `deviceName`, `deviceType`, `incomingCapabilities`, and `outgoingCapabilities`.

### Pairing
Send `{"type":"kdeconnect.pair","body":{"pair":true}}`. State machine:
```
UNKNOWN → PAIR_REQUESTED → PAIRED
        ↑                    ↓
        └───── UNPAIRED ◄────┘
```

### File Transfer (Share Plugin)
Uses **side-channel TCP**, not main TLS stream:
```json
{
  "type": "kdeconnect.share.request",
  "body": {"filename": "photo.jpg"},
  "payloadSize": 4823942,
  "payloadTransferInfo": {"port": 1739}
}
```

On receipt: dial `remoteIP:port`, `io.Copy(file, io.LimitReader(conn, payloadSize))`. **Never buffer in memory.**

---

## 2. Architecture Patterns

### Memory Efficiency
- **Packet pool:** `sync.Pool` for `*Packet`, return after plugin handles
- **Single `bufio.Reader` per connection**, allocated once
- **Streaming file transfers:** `io.LimitReader` + `io.Copy`, never buffer

### Concurrency Model
- **Goroutine per connection**, not per packet
- **Device.Send channel (buffered 32):** only safe write path. Drop if full, never block.
- **Plugin.Handle() must not block:** spawn goroutine for exec/D-Bus/disk

### Plugin Interface
```go
type Plugin interface {
    Name() string
    IncomingTypes() []string    // packet types this handles
    OutgoingTypes() []string    // packet types this sends
    Handle(ctx, dev, pkt) error // must return immediately
    OnDisconnect(dev)           // cleanup on device disconnect
}
```

Registered in `daemon.go`, dispatched by `Packet.Type`.

---

## 3. Key Libraries

| Package | Purpose |
|---------|---------|
| `github.com/godbus/dbus/v5` | D-Bus for MPRIS only |
| `github.com/BurntSushi/toml` | Config parsing |
| `github.com/urfave/cli/v2` | CLI subcommands |
| `go.uber.org/zap` | Structured logging |

**Avoid:** cobra, X11/Wayland bindings, notification libs (use `exec` instead).

**Build:** `CGO_ENABLED=0` for static binary.

---

## 4. Debugging

| Symptom | Fix |
|---------|-----|
| Device not discovered | `ufw allow 1716/udp` |
| TLS fails | Set `InsecureSkipVerify: true` |
| Packet routing silent | Log `plugin.Registry().All()` at startup |
| File transfer OOM | Use `io.LimitReader` + `io.Copy` |
| Clipboard one-way | Add `kdeconnect.clipboard.connect` to both incoming/outgoing caps |
| Binary not static | `CGO_ENABLED=0 go build` |
| Cert mismatch loop | Delete stored fingerprint, re-pair |

**Wireshark:** Set `SSLKEYLOGFILE=/tmp/kcd-keys.log` to inspect TLS packets.

---

## 5. Testing

- **Unit:** table-driven tests for protocol, fake device for plugins
- **Component:** temp Unix socket for IPC, `net.Pipe()` for transport
- **Integration:** two in-process daemons pairing over loopback
- **Manual:** pair with real Android device, verify all plugins
