#!/bin/sh
set -e

# Package post-install hook for kcd

echo "================================================================="
echo "kcd installed successfully."
echo "================================================================="
echo ""
echo "To enable the user service, run:"
echo "  systemctl --user enable --now kcd"
echo ""
echo "To enable the system-level service (per-user), run:"
echo "  sudo systemctl enable --now kcd@\$USER"
echo ""
echo "If you use a firewall, allow the KDE Connect port:"
echo "  UFW:       sudo ufw allow 1716/udp"
echo "  Firewalld: sudo firewall-cmd --permanent --add-service=kcd"
echo "             sudo firewall-cmd --reload"
echo "================================================================="
