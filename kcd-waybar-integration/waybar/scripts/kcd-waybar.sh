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

render() {
    if [ "$CONNECTED" != "true" ]; then
        printf '{"text":"\uf114","tooltip":"Disconnected","class":"kcd-disconnected","percentage":0}\n'
        return
    fi

    local icon="\uf1b9"
    if [ "$CHARGING" = "true" ]; then
        icon="\uf0e7"
    elif [ "$CHARGE" -lt 20 ]; then
        icon="\uf071"
    elif [ "$CHARGE" -lt 40 ]; then
        icon="\uf0e9"
    elif [ "$CHARGE" -lt 60 ]; then
        icon="\uf0ed"
    fi

    local css="kcd-connected"
    if [ "$CHARGING" = "true" ]; then
        css="kcd-charging"
    elif [ "$CHARGE" -lt 20 ]; then
        css="kcd-low"
    fi

    local tooltip="Battery: ${CHARGE}%"
    if [ -n "$TITLE" ]; then
        tooltip="${tooltip}\n\u25b6 ${TITLE}"
        [ -n "$ARTIST" ] && tooltip="${tooltip} - ${ARTIST}"
    fi

    jq -n -c -M \
        --arg text "$icon $CHARGE%" \
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
