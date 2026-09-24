#!/bin/sh
set -e

# Package post-install hook for kcd

echo "================================================================="
echo "kcd installed successfully."
echo "================================================================="
echo ""
echo "To enable on-demand startup (recommended), run:"
echo "  systemctl --user enable --now kcd.socket"
echo ""
echo "The daemon then starts automatically on first use — run any kcd"
echo "command once after login and a paired phone reconnects by itself."
echo ""
echo "To enable the system-level service (per-user), run:"
echo "  sudo systemctl enable --now kcd@\$USER"
echo ""
echo "If you use a firewall, allow KDE Connect. Discovery alone is not"
echo "enough — without TCP the pair never connects, and without the"
echo "side-channel range file and clipboard transfers fail:"
echo "  UFW:       sudo ufw allow kcd"
echo "             (1716/udp discovery, 1716/tcp control, 1739:1764/tcp transfers)"
echo "  Firewalld: sudo firewall-cmd --permanent --add-service=kcd"
echo "             sudo firewall-cmd --reload"
echo "================================================================="
