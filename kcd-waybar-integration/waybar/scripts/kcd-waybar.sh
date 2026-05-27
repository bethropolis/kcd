#!/bin/bash
# kcd-waybar.sh — Lightweight Waybar custom module for the kcd daemon.
#
# Requires: jq, and one of: nc (preferred), socat, or kcd CLI on PATH.
# Connects directly to the daemon Unix socket via the lightest tool
# available, then feeds events into an in-process while-loop (no subshell).
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

# Kill child processes immediately when Waybar kills this script
trap "exit" INT TERM
trap "kill 0" EXIT

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

handle_event() {
    local event="$1"
    local type
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
            PLAYING=$(echo "$event" | jq -r '.payload.isPlaying // false')
            if [ "$PLAYING" = "true" ]; then
                TITLE=$(echo "$event" | jq -r '.payload.title // empty')
                ARTIST=$(echo "$event" | jq -r '.payload.artist // empty')
            else
                TITLE=""
                ARTIST=""
            fi
            ;;
    esac

    render
}

# ---- Initial render before any event ----
render

# ---- Event source with auto-reconnect ----
# Each connection delivers an ack line followed by newline-delimited JSON events.
# When the connection drops (daemon restart, network blip), we reconnect after
# a short delay and clear stale state.

SOCKET="${KCD_SOCKET:-${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/kcd/kcd.sock}"
PAYLOAD='{"cmd":"watch","payload":{"events":["device.connected","device.disconnected","battery.update","mpris.update"]}}'

reconnect_delay() {
    sleep 1
}

connect_and_stream() {
    [[ -S "$SOCKET" ]] || return 1

    if command -v nc >/dev/null 2>&1; then
        # OpenBSD netcat — ~0.5 MB
        nc -U "$SOCKET" <<< "$PAYLOAD" 2>/dev/null
        return $?
    elif command -v socat >/dev/null 2>&1; then
        # socat — ~1 MB
        socat -,ignoreeof "UNIX-CONNECT:$SOCKET" <<< "$PAYLOAD" 2>/dev/null
        return $?
    elif command -v kcd >/dev/null 2>&1; then
        # kcd watch — ~10 MB fallback
        kcd watch --json \
            -e device.connected \
            -e device.disconnected \
            -e battery.update \
            -e mpris.update 2>/dev/null
        return $?
    fi
    return 1
}

while true; do
    # Clear stale state before each connect attempt
    TITLE=""
    ARTIST=""
    CONNECTED="false"
    render

    {
        read -r _
        while read -r event; do
            handle_event "$event"
        done
    } < <(connect_and_stream) || true

    # Connection dropped — clear stale state immediately
    TITLE=""
    ARTIST=""
    CONNECTED="false"
    render

    reconnect_delay
done
