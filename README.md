# kcd — Headless KDE Connect Daemon

[![Go Version](https://img.shields.io/badge/Go-1.25%2B-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://golang.org)
[![Protocol](https://img.shields.io/badge/KDE%20Connect-v8-4CAF50?style=for-the-badge&logoColor=white)](https://valent.andyholmes.ca/documentation/protocol.html)
[![License](https://img.shields.io/badge/License-MIT-F7DF1E?style=for-the-badge&logoColor=black)](LICENSE)
[![Release](https://img.shields.io/github/v/release/bethropolis/kcd?style=for-the-badge&logo=github&color=181717&logoColor=white)](https://github.com/bethropolis/kcd/releases/latest)
[![Build](https://img.shields.io/github/actions/workflow/status/bethropolis/kcd/ci.yml?style=for-the-badge&logo=githubactions&logoColor=white&label=build)](https://github.com/bethropolis/kcd/actions/workflows/ci.yml)
[![GHCR](https://img.shields.io/badge/container-ghcr.io-2496ED?style=for-the-badge&logo=docker&logoColor=white)](https://github.com/bethropolis/kcd/pkgs/container/kcd)
[![Platforms](https://img.shields.io/badge/Platforms-Linux%20%7C%20macOS-6e40c9?style=for-the-badge&logoColor=white)](https://github.com/bethropolis/kcd)

`kcd` is a lightweight, headless implementation of the [KDE Connect protocol v8](https://kdeconnect.kde.org/) written in Go. It lets Linux servers, containers, and minimal desktop environments participate in the KDE Connect ecosystem without a GUI, a full KDE installation, or heavy D-Bus dependencies.

## Features

| Plugin / Feature | What it does |
|---|---|
| **Battery** | Monitor remote device charge and charging state |
| **Clipboard** | Bi-directional sync (Wayland via `wl-copy`, X11 via `xclip`) |
| **Notifications** | Forward phone notifications to the desktop via `notify-send`<br>> Desktop notification icons require `libnotify ≥ 0.8.0` (Ubuntu 22.04+, Fedora 36+). Older versions receive text-only notifications. |
| **Share** | Receive files and URLs from the phone; send local files to it |
| **RunCommand** | Execute pre-configured local shell commands triggered from your phone |
| **MPRIS** | Control desktop media players (VLC, Spotify, etc.) via D-Bus |
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
> Paired devices reconnect automatically using the last known IP when broadcast is disabled (`enable_broadcast = false`), so the daemon reaches 0.0% idle CPU without losing reconnection ability.

---

## Installation


### From source (Recommended)
```bash
git clone https://github.com/bethropolis/kcd.git
cd kcd
./scripts/install.sh
```


### Binary releases

Download the latest pre-built binary from [GitHub Releases](https://github.com/bethropolis/kcd/releases).


---

## Firewall

KDE Connect requires three port ranges to be open on the local machine:

| Port | Protocol | Direction | Purpose |
|---|---|---|---|
| 1716 | UDP | bidirectional | Device discovery broadcast |
| 1716 | TCP | bidirectional | Encrypted control channel |
| 1739–1764 | TCP | inbound | File transfer side-channels |

### UFW (Ubuntu / Debian)
```bash
sudo cp packaging/ufw-kcd /etc/ufw/applications.d/kcd
sudo ufw allow kcd
```

Or manually:
```bash
sudo ufw allow 1716/udp
sudo ufw allow 1716/tcp
sudo ufw allow 1739:1764/tcp
```

### firewalld (Fedora / RHEL)
```bash
sudo cp packaging/firewalld-kcd.xml /etc/firewalld/services/kcd.xml
sudo firewall-cmd --permanent --add-service=kcd
sudo firewall-cmd --reload
```

---

## Quick Start

### 1. Start the daemon

```bash
# Run directly in the foreground
kcd daemon

# Or install and enable the systemd user unit
cp packaging/kcd-user.service ~/.config/systemd/user/kcd.service
systemctl --user daemon-reload
systemctl --user enable --now kcd
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

```bash
kcd pair a1b2c3d4_e5f6_7890_abcd_ef1234567890
```

Accept the pairing request on your phone. You can watch for the confirmation:

```bash
kcd watch --events=pair.accepted
```

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

# Unmount a previously mounted phone filesystem
kcd sftp unmount <device-id>
```

---

## Configuration

The daemon reads `$XDG_CONFIG_HOME/kcd/kcd.toml` (typically `~/.config/kcd/kcd.toml`). All settings are optional — sensible defaults are applied automatically.

```toml
device_name = "my-desktop"
device_type = "desktop"        # desktop | laptop | phone | tablet | tv
tcp_port    = 1716
log_level   = "info"           # debug | info | warn | error | quiet

# Set to false once all devices are paired — reaches 0.0% idle CPU.
enable_broadcast = true

# Auto-accept pairing requests without prompting (headless/server use).
# ⚠  Only enable on trusted networks.
auto_accept_pairing = false

# Directory where received files are saved.
download_dir = "~/Downloads/kcd"

# Reload [commands] and log_level without restarting: kill -HUP $(pidof kcd)

[plugins]
battery      = true
clipboard    = true
notification = true
share        = true
runcommand   = true
mpris        = true
ping         = true
telephony    = true
mousepad     = true
sftp         = true
findmyphone  = true
lockdevice   = true
sms          = true

# Shell commands that the RunCommand plugin exposes to your phone.
[commands]
uptime   = "uptime"
lock     = "loginctl lock-session"
suspend  = "systemctl suspend"

# Per-app notification filters.
# Keys: Android package names. Values: "show" or "silent".
# "silent" suppresses the desktop popup but still emits a watch event.
# "*" sets the default for all unmatched apps.
[notifications]
# "com.whatsapp"          = "show"
# "com.google.android.gm" = "silent"
# "*"                     = "show"
```

See [`packaging/kcd.example.toml`](packaging/kcd.example.toml) for the full annotated reference.

---

## Desktop Integrations

### Nautilus / GNOME Files

Right-click any file in Nautilus to send it directly to a paired device:

```bash
mkdir -p ~/.local/share/nautilus-python/extensions
cp packaging/nautilus-kcd.py ~/.local/share/nautilus-python/extensions/
nautilus -q   # Restart Nautilus to load the extension
```

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

A ready-made config snippet and stylesheet are in [`kcd-waybar-integration/`](kcd-waybar-integration/).

### Tiling WM shortcuts (Sway / Hyprland)

```bash
# Push clipboard to the first connected phone
bindsym Super+c exec kcd clipboard

# Ring the phone
bindsym Super+Shift+f exec kcd findmyphone $(kcd devices --json | jq -r '.[0].ID')
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

`kcd` is optimised for extremely low resource usage. After your devices are paired:

1. Open `~/.config/kcd/kcd.toml`
2. Set `enable_broadcast = false`
3. Restart the daemon

This reaches **0.0% idle CPU**. Your phone will still reconnect automatically (it remembers the IP from pairing); you only lose the ability to discover brand-new devices without entering an IP manually.

---

## Manual Connection

On networks that block broadcast packets (corporate Wi-Fi, Docker bridges, university networks):

```bash
# Enter the PC's IP in the KDE Connect app: ⋮ → Add device by IP
# Then on the PC:
kcd connect 192.168.1.100
```

---

## License

MIT
