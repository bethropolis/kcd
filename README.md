# kcd — Headless KDE Connect Daemon

`kcd` is a lightweight, headless implementation of the KDE Connect protocol (v8) in Go. 

It allows Linux servers, containers, or minimal desktop environments to interact with the KDE Connect ecosystem without a GUI or heavy D-Bus dependencies (except for optional media control).

## Features

- **Discovery**: Automatic UDP broadcast and listener.
- **Pairing**: Secure TLS identity exchange and fingerprinting.
- **Battery**: Monitor remote device charge and status from your terminal.
- **Notifications**: Forward phone notifications to your desktop via `notify-send`.
- **Clipboard**: Bi-directional sync for Wayland (`wl-copy`) and X11 (`xclip`).
- **Share**: Blazing fast file transfer via memory-efficient TCP side-channels.
- **RunCommand**: Execute pre-configured local shell commands from your phone.
- **MPRIS**: Control desktop media players (VLC, Spotify, etc.) from your device.
- **CLI**: Simple and powerful command-line interface (`kcdctl`).

## Installation

### Binary Releases (Recommended)
Download the latest release from [GitHub Releases](https://github.com/bethropolis/kcd/releases).

### Package Managers

**Debian/Ubuntu (.deb):**
```bash
curl -LO https://github.com/bethropolis/kcd/releases/download/v1.0.0/kcd_1.0.0_amd64.deb
sudo dpkg -i kcd_1.0.0_amd64.deb
sudo ufw allow 1716/udp
systemctl enable --now kcd@$USER
```

**Fedora/RHEL (.rpm):**
```bash
curl -LO https://github.com/bethropolis/kcd/releases/download/v1.0.0/kcd_1.0.0_x86_64.rpm
sudo rpm -i kcd_1.0.0_x86_64.rpm
sudo firewall-cmd --permanent --add-service=kcd
systemctl enable --now kcd@$USER
```

**Docker:**
```bash
docker pull ghcr.io/bethropolis/kcd:latest
docker run -d --name kcd --net=host -v ~/.config/kcd:/root/.config/kcd ghcr.io/bethropolis/kcd:latest
```

### From Source
```bash
git clone https://github.com/bethropolis/kcd.git
cd kcd
./scripts/install.sh
```

## Firewall Setup

KDE Connect requires these ports open:

**UDP 1716**: Device discovery (bidirectional)  
**TCP 1716**: Control connections (bidirectional)  
**TCP 1739-1764**: File transfer side-channels (inbound only)

### UFW (Ubuntu/Debian)
```bash
sudo ufw allow 1716/udp
sudo ufw allow 1716/tcp
sudo ufw allow 1739:1764/tcp
```

Or use the provided profile:
```bash
sudo cp packaging/ufw-kcd /etc/ufw/applications.d/kcd
sudo ufw allow kcd
```

### firewalld (Fedora/RHEL)
```bash
sudo firewall-cmd --permanent --add-service=kdeconnect
sudo firewall-cmd --reload
```

Or use the provided service definition:
```bash
sudo cp packaging/firewalld-kcd.xml /etc/firewalld/services/kcd.xml
sudo firewall-cmd --permanent --add-service=kcd
sudo firewall-cmd --reload
```

## ⚙️ Configuration

The daemon looks for configuration in `$XDG_CONFIG_HOME/kcd/kcd.toml` (usually `~/.config/kcd/kcd.toml`).

See [packaging/kcd.example.toml](packaging/kcd.example.toml) for all available options including custom shell commands.

## Usage

### Starting the Daemon
Manual start:
```bash
kcd
```
Via systemd user unit (if installed manually from source):
```bash
cp packaging/kcd-user.service ~/.config/systemd/user/kcd.service
systemctl --user daemon-reload
systemctl --user enable --now kcd
```

### Using the CLI
List discovered devices:
```bash
kcd devices
```

Pair with a device:
```bash
kcd pair <device-id>
```

Send a file:
```bash
kcd send file <device-id> /path/to/cat.jpg
```

Sync clipboard to device:
```bash
kcd clipboard push <device-id>
```

Execute a remote command:
```bash
kcd run exec <device-id> uptime
```

## Desktop Integration

### Waybar Battery Widget
Monitor phone battery in your Waybar status bar:

**~/.config/waybar/config**
```json
"custom/phone-battery": {
    "exec": "kcd watch --events=battery | jq -r 'select(.type==\"battery.update\") | \"\(.payload.charge)%\" + (if .payload.charging then \" (charging)\" else \"\" end)'",
    "restart-interval": 0,
    "format": "{}",
    "return-type": ""
}
```

**~/.config/waybar/style.css**
```css
#custom-phone-battery {
    color: #a6e3a1;
    padding: 0 10px;
}
```

### Custom Scripts
Stream all events as JSON:
```bash
kcd watch --events=battery,notification,device | while read -r event; do
    echo "$event" | jq .
done
```

## Security
- **TLS Everywhere**: All network traffic is encrypted via TLS.
- **Fingerprinting**: Implicit authentication via certificate fingerprints (Self-signed).
- **Sanitization**: Filenames in the Share plugin are strictly sanitized to prevent path traversal.
- **Memory Safe**: Zero-allocation streaming for large files prevents OOM attacks.

## Performance & Idle CPU

`kcd` is optimized for extremely low resource usage. 

To achieve **0.0% idle CPU** after you have paired your devices:
1. Open your configuration file (`~/.config/kcd/kcd.toml`)
2. Add `enable_broadcast = false`
3. Restart the daemon.

*Note: With broadcasting disabled, your PC won't announce itself on the network. Your phone will still connect to the PC automatically since it remembers the IP address from pairing, but initial discovery of new devices will require you to enter the IP manually on your phone.*

## Roadmap
- **Phase 5**: mDNS (Zeroconf) support for modern discovery, bypassing UDP broadcast restrictions on restricted networks and newer Android versions.

## License
MIT
