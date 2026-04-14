#!/bin/sh
set -e

# Package pre-remove hook for kcd

# Try to stop and disable all instances of the kcd template service
# that might be running. 
if command -v systemctl >/dev/null 2>&1; then
    echo "Stopping any running kcd services..."
    # Find active instances
    systemctl list-units "kcd@*.service" --no-legend --plain | awk '{print $1}' | while read -r unit; do
        if [ -n "$unit" ]; then
            echo "Stopping and disabling $unit..."
            systemctl stop "$unit" || true
            systemctl disable "$unit" || true
        fi
    done
fi

echo "Note: User config (~/.config/kcd) and state (~/.local/state/kcd) directories were preserved."
