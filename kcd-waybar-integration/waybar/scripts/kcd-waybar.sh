#!/bin/bash
# kcd-waybar.sh — Lightweight Waybar custom module for the kcd daemon.
#
# Requires: jq, kcd CLI on PATH.
# Connects to the daemon via `kcd watch --json` (the client handles
# socket reconnection with exponential backoff).
#
# Install:
#   mkdir -p ~/.config/waybar/scripts
#   cp kcd-waybar.sh  ~/.config/waybar/scripts/
#   chmod +x           ~/.config/waybar/scripts/kcd-waybar.sh
#
# Waybar config (~/.config/waybar/config.jsonc):
#   "custom/kcd": {
#       "exec": "~/.config/waybar/scripts/kcd-waybar.sh",
#       "return-type": "json",
#       "restart-interval": 0
#   }

CHARGE=0
CHARGING="false"
TITLE=""
ARTIST=""
CONNECTED="false"

# Nerd Font icons (Font Awesome codepoints) — $'...' required for \u
ICON_OFF=$'\uf127'          # unlink
ICON_CHARGING=$'\uf0e7'     # bolt
ICON_BAT100=$'\uf240'       # battery-full
ICON_BAT75=$'\uf241'        # battery-three-quarters
ICON_BAT50=$'\uf242'        # battery-half
ICON_BAT25=$'\uf243'        # battery-quarter
ICON_BAT0=$'\uf244'         # battery-empty
ICON_PLAY=$'\uf04b'         # play
NL=$'\n'

render() {
    if [ "$CONNECTED" != "true" ]; then
        jq -n -c -M \
            --arg text "$ICON_OFF" \
            --arg tooltip "Disconnected" \
            --arg class "kcd-disconnected" \
            --argjson percentage 0 \
            '{text: $text, tooltip: $tooltip, class: $class, percentage: $percentage}'
        return
    fi

    local icon="$ICON_BAT100"
    local css="kcd-connected"

    if [ "$CHARGING" = "true" ]; then
        icon="$ICON_CHARGING"
        css="kcd-charging"
    elif [ "$CHARGE" -lt 20 ]; then
        icon="$ICON_BAT0"
        css="kcd-low"
    elif [ "$CHARGE" -lt 40 ]; then
        icon="$ICON_BAT25"
    elif [ "$CHARGE" -lt 60 ]; then
        icon="$ICON_BAT50"
    elif [ "$CHARGE" -lt 80 ]; then
        icon="$ICON_BAT75"
    fi

    local tooltip="Battery: ${CHARGE}%"
    if [ -n "$TITLE" ]; then
        if [ -n "$ARTIST" ]; then
            tooltip="${tooltip}${NL}${ICON_PLAY} ${TITLE} - ${ARTIST}"
        else
            tooltip="${tooltip}${NL}${ICON_PLAY} ${TITLE}"
        fi
    fi

    jq -n -c -M \
        --arg text "${icon} ${CHARGE}%" \
        --arg tooltip "$tooltip" \
        --arg css "$css" \
        --argjson percentage "$CHARGE" \
        '{text: $text, tooltip: $tooltip, class: $css, percentage: $percentage}'
}

render

kcd watch --json \
    -e device.connected \
    -e device.disconnected \
    -e battery.update \
    -e mpris.update | while read -r event; do

    type=$(echo "$event" | jq -r '.type')

    case "$type" in
        "device.connected")
            CONNECTED="true"
            ;;
        "device.disconnected")
            CONNECTED="false"
            ;;
        "battery.update")
            CHARGE=$(echo "$event" | jq -r '.payload.charge // 0')
            CHARGING=$(echo "$event" | jq -r '.payload.charging // false')
            CONNECTED="true"
            ;;
        "mpris.update")
            TITLE=$(echo "$event" | jq -r '.payload.title // empty')
            ARTIST=$(echo "$event" | jq -r '.payload.artist // empty')
            ;;
    esac

    render
done
