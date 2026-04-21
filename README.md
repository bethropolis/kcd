# kcd — Headless KDE Connect Daemon

`kcd` is a lightweight, headless implementation of the [KDE Connect protocol v8](https://kdeconnect.kde.org/) written in Go. It lets Linux servers, containers, and minimal desktop environments participate in the KDE Connect ecosystem without a GUI, a full KDE installation, or heavy D-Bus dependencies.

## Features

| Plugin | What it does |
|---|---|
| **Battery** | Monitor remote device charge and charging state |
| **Clipboard** | Bi-directional sync (Wayland via `wl-copy`, X11 via `xclip`) |
| **Notifications** | Forward phone notifications to the desktop via `notify-send` |
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
| **Connectivity** | Phone signal strength reporting |
| **System Volume** | Remote volume control |
| **Send Notification** | Push a notification from PC to phone |

Discovery is dual-mode: **UDP broadcast** (port 1716) and **mDNS/Zeroconf** (`_kdeconnect._udp`), so `kcd` works on both simple home networks and restricted environments (corporate Wi-Fi, Docker, university networks) where broadcast packets are dropped.

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
# Push your clipboard to the first connected phone
kcd clipboard

# Send a file
kcd share <device-id> ~/Pictures/photo.jpg

# Ring the phone
kcd findmyphone <device-id>

# Watch live events
kcd watch
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

# Auto-accept pairing requests without manual confirmation (headless / server use).
auto_accept_pairing = false

# Directory where received files are saved.
download_dir = "~/Downloads/kcd"

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
    "exec": "kcd watch --events=battery.update | jq -r 'select(.type==\"battery.update\") | \"\(.payload.charge)%\" + (if .payload.charging then \" \" else \"\" end)'",
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