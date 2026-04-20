#!/usr/bin/env bash
set -e

# kcd manual installation script
# Installs to ~/.local/bin/kcd and ~/.config/systemd/user/kcd.service

echo "================================================================="
echo " Installing kcd (Headless KDE Connect Daemon)"
echo "================================================================="

# Directories
BIN_DIR="$HOME/.local/bin"
SYSTEMD_DIR="$HOME/.config/systemd/user"
CONFIG_DIR="$HOME/.config/kcd"

# Ensure directories exist
mkdir -p "$BIN_DIR"
mkdir -p "$SYSTEMD_DIR"
mkdir -p "$CONFIG_DIR"

# Check dependencies
if ! command -v go >/dev/null 2>&1; then
    echo "Error: 'go' is not installed. Please install Go 1.22+ to build kcd."
    exit 1
fi

if systemctl --user is-active --quiet kcd.service 2>/dev/null; then
    echo "Stopping existing kcd.service..."
    systemctl --user stop kcd.service || true
fi

# Backup existing
if [ -f "$BIN_DIR/kcd" ]; then
    echo "Backing up existing binary to $BIN_DIR/kcd.backup"
    mv "$BIN_DIR/kcd" "$BIN_DIR/kcd.backup"
fi

# Build
echo "Building static binary..."
if ! CGO_ENABLED=0 go build -ldflags="-s -w" -o "bin/kcd" ./cmd/kcd; then
    echo "Error: Build failed."
    if [ -f "$BIN_DIR/kcd.backup" ]; then
        echo "Restoring backup..."
        mv "$BIN_DIR/kcd.backup" "$BIN_DIR/kcd"
    fi
    exit 1
fi

# Verify static build
if command -v ldd >/dev/null 2>&1; then
    if ldd "bin/kcd" >/dev/null 2>&1; then
        echo "Error: Binary is not statically linked."
        exit 1
    fi
fi

# Install binary
echo "Installing binary to $BIN_DIR/kcd"
install -m 755 "bin/kcd" "$BIN_DIR/kcd"

# Install service
echo "Installing systemd service to $SYSTEMD_DIR/kcd.service"
install -m 644 "packaging/kcd-user.service" "$SYSTEMD_DIR/kcd.service"
systemctl --user daemon-reload

# Example config
if [ ! -f "$CONFIG_DIR/kcd.toml" ]; then
    echo "Installing default config to $CONFIG_DIR/kcd.toml"
    install -m 644 "packaging/kcd.example.toml" "$CONFIG_DIR/kcd.toml"
else
    echo "Config already exists at $CONFIG_DIR/kcd.toml (skipping)"
fi

# Nautilus extension
NAUTILUS_EXT_DIR="$HOME/.local/share/nautilus-python/extensions"
if [ -f "packaging/nautilus-kcd.py" ]; then
    echo "Installing Nautilus extension to $NAUTILUS_EXT_DIR/"
    mkdir -p "$NAUTILUS_EXT_DIR"
    install -m 644 "packaging/nautilus-kcd.py" "$NAUTILUS_EXT_DIR/nautilus-kcd.py"
    if command -v nautilus >/dev/null 2>&1; then
        if [ -n "$DISPLAY" ] || [ -n "$WAYLAND_DISPLAY" ]; then
            echo "Restarting Nautilus to load extension..."
            nautilus -q >/dev/null 2>&1 || true
        fi
    fi
fi

# Enable and start service
echo "Enabling and starting kcd.service..."
systemctl --user enable --now kcd.service || true

echo "================================================================="
echo "✓ Installation complete!"
echo "================================================================="
echo "Next steps:"
echo "1. Ensure $BIN_DIR is in your PATH."
echo "2. Configure firewall if needed:"
echo "   sudo ufw allow 1716/udp"
echo "================================================================="
