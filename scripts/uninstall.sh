#!/usr/bin/env bash
set -e

# kcd manual uninstallation script

echo "================================================================="
echo " Uninstalling kcd (Headless KDE Connect Daemon)"
echo "================================================================="

BIN_DIR="$HOME/.local/bin"
SYSTEMD_DIR="$HOME/.config/systemd/user"
CONFIG_DIR="$HOME/.config/kcd"
STATE_DIR="$HOME/.local/state/kcd"

# Stop service if running
if systemctl --user is-active --quiet kcd.service 2>/dev/null; then
    echo "Stopping and disabling kcd.service..."
    systemctl --user stop kcd.service
    systemctl --user disable kcd.service
fi

# Remove systemd unit
if [ -f "$SYSTEMD_DIR/kcd.service" ]; then
    echo "Removing systemd service..."
    rm "$SYSTEMD_DIR/kcd.service"
    systemctl --user daemon-reload
fi

# Remove binary
if [ -f "$BIN_DIR/kcd" ]; then
    echo "Removing binary..."
    rm "$BIN_DIR/kcd"
fi
if [ -f "$BIN_DIR/kcd.backup" ]; then
    echo "Removing binary backup..."
    rm "$BIN_DIR/kcd.backup"
fi

# Ask about config/state
echo "================================================================="
read -p "Do you want to remove configuration and paired device state? [y/N] " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    echo "Removing configuration and state directories..."
    rm -rf "$CONFIG_DIR"
    rm -rf "$STATE_DIR"
    echo "Done."
else
    echo "Configuration and state directories preserved."
fi

echo "================================================================="
echo "✓ Uninstallation complete!"
echo "================================================================="
