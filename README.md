# kcd - Headless KDE Connect Daemon

[![Go Version](https://img.shields.io/badge/Go-1.25%2B-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://golang.org)
[![Protocol](https://img.shields.io/badge/KDE%20Connect-v8-4CAF50?style=for-the-badge&logoColor=white)](https://valent.andyholmes.ca/documentation/protocol.html)
[![License](https://img.shields.io/badge/License-MIT-F7DF1E?style=for-the-badge&logoColor=black)](LICENSE)
[![Release](https://img.shields.io/github/v/release/bethropolis/kcd?style=for-the-badge&logo=github&color=181717&logoColor=white)](https://github.com/bethropolis/kcd/releases/latest)
[![Build](https://img.shields.io/github/actions/workflow/status/bethropolis/kcd/ci.yml?style=for-the-badge&logo=githubactions&logoColor=white&label=build)](https://github.com/bethropolis/kcd/actions/workflows/ci.yml)
[![GHCR](https://img.shields.io/badge/container-ghcr.io-2496ED?style=for-the-badge&logo=docker&logoColor=white)](https://github.com/bethropolis/kcd/pkgs/container/kcd)
[![Platforms](https://img.shields.io/badge/Platform-Linux-6e40c9?style=for-the-badge&logoColor=white)](https://github.com/bethropolis/kcd)

`kcd` is a lightweight, headless implementation of the [KDE Connect protocol v8](https://kdeconnect.kde.org/) written in Go. It lets Linux servers, containers, and minimal desktop environments participate in the KDE Connect ecosystem without a GUI, a full KDE installation, or heavy D-Bus dependencies.

## Features

| Plugin / Feature | What it does |
|---|---|
| **Battery** | Monitor remote device charge and charging state |
| **Clipboard** | Bi-directional sync (Wayland via `wl-copy`, X11 via `xclip`) |
| **Notifications** | Forward phone notifications to the desktop via `notify-send`<br>> Desktop notification icons require `libnotify ≥ 0.8.0` (Ubuntu 22.04+, Fedora 36+). Older versions receive text-only notifications. |
| **Share** | Receive files and URLs from the phone; send local files to it |
| **RunCommand** | Execute pre-configured local shell commands triggered from your phone |
| **MPRIS** | Control desktop media players via D-Bus **and** control phone music (play/pause/next/prev/volume/seek) from the desktop CLI |
| **Mousepad** | Use the phone as a wireless trackpad and keyboard |
| **Find My Phone** | Ring the phone to locate it |
| **Telephony** | Get call and SMS notifications on the desktop |
| **SMS** | Send SMS messages via the phone |
| **SFTP** | Browse the phone's filesystem |
| **Lock / Unlock** | Lock and unlock the desktop session |
| **Ping** | Simple connectivity check |
| **Connectivity** | Phone signal strength and network type reporting |
| **System Volume** | Control desktop audio volume from the phone |
| **Send Notification** | Push a notification from the PC to the phone |
| **Auto-reconnect** | Paired devices reconnect automatically after dropping |

Discovery is dual-mode: **UDP broadcast** (port 1716) and **mDNS/Zeroconf** (`_kdeconnect._udp`), so `kcd` works on both simple home networks and restricted environments (corporate Wi-Fi, Docker, university networks) where broadcast packets are dropped.

> [!NOTE]
> Broadcast is off by default. It only runs during `kcd pair` (listen mode) and stops immediately after. The UDP/mDNS **listener** stays always-on, so paired devices reconnect automatically via remembered IPs at 0.0% idle CPU.

---

## Installation


### From source (Recommended)
```bash
git clone https://github.com/bethropolis/kcd.git
cd kcd
./scripts/install.sh
```


### Arch Linux

Install from the AUR using your preferred helper:

```bash
yay -S kcd-bin
```

### Container

Multi-arch images on GHCR: [`docs/CONTAINER.md`](docs/CONTAINER.md)

### Binary releases

Download the latest pre-built binary from [GitHub Releases](https://github.com/bethropolis/kcd/releases).


---

## Firewall

KDE Connect needs three port ranges open:

| Port | Protocol | Direction | Purpose |
|---|---|---|---|
| 1716 | UDP | bidirectional | Device discovery broadcast |
| 1716 | TCP | bidirectional | Encrypted control channel |
| 1739–1764 | TCP | inbound | File transfer side-channels |

> **Installed via .deb, .rpm, or AUR?** The firewall rules are already in place
> — nothing to do on your end. These steps are only needed for manual installs.

### UFW (Ubuntu / Debian)
```bash
sudo cp packaging/ufw-kcd /etc/ufw/applications.d/kcd
sudo ufw allow kcd
```

### firewalld (Fedora / RHEL)
```bash
sudo cp packaging/firewalld-kcd.xml /etc/firewalld/services/kcd.xml
sudo firewall-cmd --permanent --add-service=kcd
sudo firewall-cmd --reload
```

### Manual
```bash
sudo ufw allow 1716/udp
sudo ufw allow 1716/tcp
sudo ufw allow 1739:1764/tcp
```

---

## Quick Start

### 1. Start the daemon

If you installed via the script, `.deb`, `.rpm`, or AUR, the systemd user service
is already set up — enable and start it:

```bash
systemctl --user enable --now kcd
```

Check that it's running:

```bash
systemctl --user status kcd
```

To run in the foreground instead (for testing):

```bash
kcd daemon
```


### 2. Discover your phone

Open the KDE Connect app on your Android device and make sure it is on the same network. Then:

```bash
kcd devices
```

```
DEVICE ID                            NAME              TYPE       STATE      CONNECTED
---------------------------------------------------------------------------------------------------
a1b2c3d4_e5f6_7890_abcd_ef1234567890 Pixel 8 Pro       phone      Unpaired   true
```

### 3. Pair

Two ways to pair:

**A. Send pair request to a discovered device:**
```bash
kcd pair a1b2c3d4_e5f6_7890_abcd_ef1234567890
```

you can accept the pair request on your phone


**B. Listen mode — accept any incoming request (headless/server):**
```bash
kcd pair
```

Broadcast starts automatically so the phone can find the PC. Accept on the phone, the CLI confirms and exits. Broadcast stops immediately. Press Ctrl+C to cancel.


### 4. Use it

```bash
# Check daemon health and dependencies
kcd doctor

# Show daemon uptime, version, connected devices, and plugins
kcd status

# Push your clipboard to the first connected phone
kcd clipboard

# Send a file
kcd share <device-id> ~/Pictures/photo.jpg

# Ring the phone
kcd findmyphone <device-id>

# Watch live events
kcd watch

# Watch device connect/disconnect live
kcd devices --watch

# List MPRIS players on the phone and control playback
kcd mpris list
kcd mpris play
kcd mpris pause
kcd mpris next
kcd mpris previous
```

---

## Configuration

The daemon reads `$XDG_CONFIG_HOME/kcd/kcd.toml` (typically `~/.config/kcd/kcd.toml`). All settings are optional — sensible defaults are applied automatically.

```toml
device_name = "my-desktop"
device_type = "desktop"        # desktop | laptop | phone | tablet | tv
tcp_port    = 1716
log_level   = "info"           # debug | info | warn | error | quiet

# Directory where received files are saved.
download_dir = "~/Downloads/kcd"

# Reload [commands] and log_level without restarting: kill -HUP $(pidof kcd)

[plugins]
# battery      = true
# clipboard    = true
# ... (see example config for full list)

# ─── Detailed Plugin Configuration ────────────────────────────────────────────
# Each plugin has its own section for fine-tuning behavior.

[battery]
# notify_low   = true
# notify_full  = true
# low_urgency  = "critical"

[notification_plugin]
# fetch_icons  = true
# expire_ms    = -1

[share]
# auto_open    = false
# open_command = "xdg-open"

[sftp]
# auto_open    = true
# mount_dir    = "/home/user/mnt"

[commands]
uptime   = "uptime"
lock     = "loginctl lock-session"

[notifications]
# "*" = "show"

# See packaging/kcd.example.toml for the full annotated reference of all settings.
```

See [`packaging/kcd.example.toml`](packaging/kcd.example.toml) for the full annotated reference.

---

## Desktop Integrations

### Nautilus / GNOME Files

Most of the installation methods, add the [kcd nautilus plugin](packaging/nautilus-kcd.py) which allows you to right-click any file in Nautilus to send it directly to a paired device.

### Waybar (phone battery widget)

Add to `~/.config/waybar/config`:
```json
"custom/phone-battery": {
    "exec": "kcd watch --events=battery.update | jq -r 'select(.type==\"battery.update\") | \"\\(.payload.charge)%\" + (if .payload.charging then \" \" else \"\" end)'",
    "restart-interval": 0,
    "format": "󰏚 {}",
    "return-type": ""
}
```

A ready-made config snippet, bash script and stylesheet are in [`kcd-waybar-integration/`](kcd-waybar-integration/).

### Tiling WM shortcuts (Sway / Hyprland)

```bash
# Push clipboard to the first connected phone
bindsym Super+c exec kcd clipboard

# Ring the phone
bindsym Super+Shift+f exec kcd findmyphone

# Control phone music playback
bindsym Super+Shift+period exec kcd mpris next
bindsym Super+Shift+comma exec kcd mpris previous
bindsym Super+Shift+m exec kcd mpris toggle
```

### Custom event scripts

```bash
kcd watch --json | while read -r event; do
    type=$(echo "$event" | jq -r '.type')
    case "$type" in
        battery.update)
            charge=$(echo "$event" | jq -r '.payload.charge')
            notify-send "Phone battery" "${charge}%"
            ;;
        telephony.ringing)
            echo "$event" | jq -r '.payload | "Call from \(.contactName // .phoneNumber)"'
            ;;
    esac
done
```

---

## Events

`kcd watch --json` streams NDJSON events. All events include `type`, `timestamp` (RFC3339), and `deviceId` fields.

| Event type | Payload fields | Description |
|---|---|---|
| `device.added` | `name`, `type` | New device seen for the first time |
| `device.removed` | — | Device unpaired and removed |
| `device.connected` | `name`, `type` | TCP connection established |
| `device.disconnected` | — | Connection dropped |
| `pair.requested` | — | Phone sent a pairing request |
| `pair.accepted` | — | Pairing completed on both sides |
| `pair.rejected` | — | Pairing denied or cancelled |
| `battery.update` | `charge`, `charging` | Battery reading received |
| `battery.threshold` | `charge`, `charging`, `event` | Low (event=1) or full (event=2) |
| `notification` | `appName`, `title`, `text`, `requestReplyId` | Phone notification forwarded |
| `notification.canceled` | `id` | Phone dismissed a notification |
| `share.progress` | `file`, `current`, `total` | File transfer in progress (~2/sec) |
| `share.complete` | `file`, `path` | File transfer finished |
| `share.text` | `text` | Plain text received |
| `share.url` | `url` | URL received |
| `ping.received` | — | Ping arrived |
| `telephony.ringing` | `contactName`, `phoneNumber` | Incoming call |
| `telephony.missed` | `contactName`, `phoneNumber` | Missed call |
| `telephony.canceled` | — | Call ended |
| `connectivity.update` | `signal`, `networkType` | Signal strength report |
| `mpris.update` | `player`, `title`, `artist`, `album`, `isPlaying`, `pos`, `length`, `volume` | Phone now playing state changed |
| `volume.update` | `name`, `volume`, `muted` | Desktop volume changed from phone |
| `sftp.mount` | `uri`, `ip`, `port`, `user`, `password`, `path` | SFTP credentials received |

---

## Security

- **TLS everywhere** — all traffic is encrypted. Plaintext is only used for the initial identity exchange before the TLS upgrade.
- **Certificate fingerprinting** — trust is established by comparing the SHA-256 fingerprint of each peer's self-signed certificate during pairing. No CA required.
- **Path sanitisation** — filenames received via the Share plugin are strictly sanitised to prevent path traversal attacks.
- **Memory-safe transfers** — large file transfers stream directly to disk to prevent OOM conditions.

---

## Performance

`kcd` is optimised for extremely low resource usage. Broadcast is off by default (only enabled during `kcd pair` listen mode for discovery), so the daemon sits at **0.0% idle CPU** in normal operation. Paired phones reconnect automatically via the last known IP without any broadcast chatter.

---

## Manual Connection

On networks that block broadcast packets (corporate Wi-Fi, Docker bridges, university networks):

```bash
# Enter the PC's IP in the KDE Connect app: ⋮ → Add device by IP
# Then on the PC:
kcd connect 192.168.1.100
```

---

## Further Reading

| Document | Description |
|---|---|
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | System architecture, plugin system, event bus, IPC protocol |
| [`docs/CLI.md`](docs/CLI.md) | Full CLI reference and sub-commands |
| [`docs/CONTAINER.md`](docs/CONTAINER.md) | Running kcd in Docker / Podman |
| [`packaging/kcd.example.toml`](packaging/kcd.example.toml) | Annotated configuration reference |

---

## License

[MIT](./LICENSE)
