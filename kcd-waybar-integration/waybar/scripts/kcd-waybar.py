#!/usr/bin/env python3
"""
kcd-waybar.py — Waybar custom module for the kcd daemon.

Connects to `kcd watch --json`, maintains per-device state in memory,
and re-renders Waybar JSON on every event. Reconnects automatically
if the daemon restarts.

Install: chmod +x ~/.config/waybar/scripts/kcd-waybar.py
"""

import json
import os
import signal
import subprocess
import sys
import time

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------

# Add common user bin paths since Waybar environment might not have them
os.environ["PATH"] += (
    os.pathsep
    + os.path.expanduser("~/.local/bin")
    + os.pathsep
    + os.path.expanduser("~/go/bin")
)

KCD_BIN = os.environ.get("KCD_BIN", "kcd")
RECONNECT_DELAY = 5  # seconds before reconnecting after daemon exit

# Nerd Font icons (requires a Nerd Font in your Waybar font config)
ICON_PHONE = "󰏲"  # phone connected
ICON_PHONE_OFF = "󰄕"  # no device
ICON_CHARGING = "󰂄"  # charging
ICONS_BAT = {  # discharge tiers: ≥80, ≥60, ≥40, ≥20, else
    80: "󰁹",
    60: "󰁾",
    40: "󰁼",
    20: "󰁺",
    0: "󰁻",
}


# ---------------------------------------------------------------------------
# State
# ---------------------------------------------------------------------------

# devices[device_id] = {
#   "name": str, "type": str,
#   "charge": int, "charging": bool,
#   "connected": bool
# }
devices: dict = {}


# ---------------------------------------------------------------------------
# Rendering
# ---------------------------------------------------------------------------


def battery_icon(charge: int, charging: bool) -> str:
    if charging:
        return ICON_CHARGING
    for threshold, icon in sorted(ICONS_BAT.items(), reverse=True):
        if charge >= threshold:
            return icon
    return ICONS_BAT[0]


def render() -> None:
    connected = {did: d for did, d in devices.items() if d["connected"]}

    if not connected:
        out = {
            "text": ICON_PHONE_OFF,
            "tooltip": "kcd: no devices connected",
            "class": "kcd-disconnected",
            "percentage": 0,
        }
        print(json.dumps(out, ensure_ascii=False), flush=True)
        return

    # Primary device: first in connected dict
    primary = next(iter(connected.values()))
    charge = primary["charge"]
    charging = primary["charging"]

    bat_icon = battery_icon(charge, charging)
    text = f"{ICON_PHONE} {bat_icon} {charge}%"

    # CSS class
    if charging:
        css = "kcd-charging"
    elif charge < 20:
        css = "kcd-low"
    else:
        css = "kcd-connected"

    # Tooltip: one line per device
    lines = ["<b>KDE Connect</b>"]
    for d in connected.values():
        state_str = (
            f"charging {d['charge']}%" if d["charging"] else f"battery {d['charge']}%"
        )
        lines.append(f"{d['name']}  {state_str}  ({d['type']})")

    out = {
        "text": text,
        "tooltip": "\n".join(lines),
        "class": css,
        "percentage": charge,
    }
    print(json.dumps(out, ensure_ascii=False), flush=True)


def render_error(msg: str) -> None:
    print(
        json.dumps(
            {
                "text": ICON_PHONE_OFF,
                "tooltip": msg,
                "class": "kcd-error",
                "percentage": 0,
            },
            ensure_ascii=False,
        ),
        flush=True,
    )


# ---------------------------------------------------------------------------
# Event handling
# ---------------------------------------------------------------------------


def handle_event(ev: dict) -> None:
    etype = ev.get("type", "")
    did = ev.get("deviceId", "")
    payload = ev.get("payload") or {}

    if etype == "device.connected":
        if did not in devices:
            devices[did] = {
                "name": payload.get("name", did),
                "type": payload.get("type", "phone"),
                "charge": 0,
                "charging": False,
                "connected": True,
            }
        else:
            devices[did]["connected"] = True
            # refresh name if changed
            if payload.get("name"):
                devices[did]["name"] = payload["name"]
        render()

    elif etype == "device.disconnected":
        if did in devices:
            devices[did]["connected"] = False
        render()

    elif etype == "battery.update":
        if did not in devices:
            devices[did] = {
                "name": did,
                "type": "phone",
                "charge": 0,
                "charging": False,
                "connected": True,
            }
        devices[did]["charge"] = int(payload.get("charge", 0))
        devices[did]["charging"] = bool(payload.get("charging", False))
        render()

    elif etype == "pair.request":
        name = payload.get("name", "Unknown device")
        # Fire-and-forget: show a system notification asking to accept
        os.system(
            f'notify-send -a "KDE Connect" -u normal -t 15000 '
            f'"Pair request" "From: {name}\\nRun: kcd pair <device-id>"'
        )

    elif etype == "pair.accepted":
        name = payload.get("name", "Unknown device")
        os.system(
            f'notify-send -a "KDE Connect" -u low -t 5000 '
            f'"Paired" "Now paired with {name}"'
        )

    # device.added / device.removed: re-render but no state change needed here


# ---------------------------------------------------------------------------
# Main loop
# ---------------------------------------------------------------------------

proc = None


def cleanup(*args):
    global proc
    if proc is not None:
        try:
            proc.terminate()
            proc.wait(timeout=2)
        except Exception:
            try:
                proc.kill()
            except Exception:
                pass
    sys.exit(0)


def main() -> None:
    global devices, proc

    while True:
        try:
            render()  # Ensure we draw the disconnected state initially or immediately

            proc = subprocess.Popen(
                [KCD_BIN, "watch", "--json"],
                stdout=subprocess.PIPE,
                stderr=subprocess.DEVNULL,
                text=True,
                bufsize=1,  # line-buffered
            )

            while True:
                raw_line = proc.stdout.readline()
                if not raw_line:  # EOF (Daemon disconnected)
                    break

                line = raw_line.strip()
                if not line:
                    continue

                try:
                    ev = json.loads(line)
                except json.JSONDecodeError:
                    continue
                handle_event(ev)

            proc.wait()

        except FileNotFoundError:
            render_error(f"kcd not found in PATH ({KCD_BIN})")
            time.sleep(RECONNECT_DELAY * 2)
            continue

        except Exception as exc:
            render_error(f"kcd-waybar error: {exc}")

        # Daemon exited: mark everything disconnected, wait, retry
        for d in devices.values():
            d["connected"] = False
        render()
        time.sleep(RECONNECT_DELAY)


if __name__ == "__main__":
    # Clean exit on SIGTERM (sent by Waybar on reload)
    signal.signal(signal.SIGTERM, cleanup)
    signal.signal(signal.SIGINT, cleanup)
    main()
