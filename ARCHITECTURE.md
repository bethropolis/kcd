# kcd Architecture

`kcd` is a headless, concurrent, event-driven implementation of the KDE Connect v8 protocol written in Go.

## 1. Network & Transport (`internal/discovery`, `internal/transport`)
- **Discovery**: Devices are discovered via UDP broadcasts on port `1716` and mDNS (Zeroconf) on `_kdeconnect._udp`.
- **TLS Handshake**: KDE Connect uses inverted TLS roles. The device that initiates the TCP connection acts as the TLS Server, and the receiving device acts as the TLS Client.
- **Authentication**: `kcd` generates a self-signed certificate on first run. Trust is established by comparing the SHA-256 fingerprint of the peer's certificate during the Pairing phase.

## 2. Device Registry & Event Bus (`internal/device`, `internal/events`)
- Active connections are wrapped in a thread-safe `Device` struct.
- The `Registry` manages all known devices and persists their keys to disk.
- Internal state changes (e.g., Battery updates, connection drops) are broadcast internally using a non-blocking `events.Bus`.

## 3. Plugin System (`internal/plugin`)
- All features implement the `Plugin` interface.
- Incoming `protocol.Packet` payloads are dynamically routed to the correct plugin based on the `packet.Type` field (e.g., `kdeconnect.telephony`).
- **Concurrency Rule**: Plugins are executed sequentially per device. Any heavy operation (disk I/O, subprocess execution like `notify-send`) MUST be spawned in a background goroutine so it does not block the TCP read loop.

## 4. IPC & CLI (`internal/ipc`, `cmd/kcd`)
- The `kcd` daemon opens a Unix socket (`/run/user/$UID/kcd/kcd.sock`).
- The `kcdctl` (or `kcd <cmd>`) CLI acts as a client to this socket.
- Commands are sent as simple JSON requests. Event streams (like `kcd watch`) use NDJSON (Newline Delimited JSON) to stream real-time data to terminal tools like `jq` or `Waybar`.