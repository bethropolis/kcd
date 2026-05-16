#!/usr/bin/env python3
"""
kcd-waybar.py — Waybar custom module for the kcd daemon.

Connects directly to the kcd daemon's Unix socket (no subprocess),
maintains per-device state in memory, and re-renders Waybar JSON
on every event. Reconnects automatically if the daemon restarts.
"""

import json
import os
import socket
import time

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------

os.environ["PATH"] += (
    os.pathsep
    + os.path.expanduser("~/.local/bin")
    + os.pathsep
    + os.path.expanduser("~/go/bin")
)

RECONNECT_DELAY = 5

_runtime_dir = os.environ.get("XDG_RUNTIME_DIR")
if not _runtime_dir:
    _runtime_dir = f"/run/user/{os.getuid()}"
SOCKET_PATH = os.environ.get("KCD_SOCKET") or os.path.join(
    _runtime_dir, "kcd", "kcd.sock"
)

# Nerd Font icons
ICON_PHONE_OFF = "󰄕"
ICON_CHARGING = "󰂄"
# discharge tiers: ≥80, ≥60, ≥40, ≥20, else (pre-sorted descending)
ICONS_BAT = ((80, "󰁹"), (60, "󰁾"), (40, "󰁼"), (20, "󰁺"), (0, "󰁻"))


# ---------------------------------------------------------------------------
# Device state (__slots__ avoids per-instance dict overhead)
# ---------------------------------------------------------------------------

class Device:
    __slots__ = ("name", "type", "charge", "charging", "connected")
    def __init__(self, name="", type="phone", charge=0, charging=False, connected=True):
        self.name = name
        self.type = type
        self.charge = charge
        self.charging = charging
        self.connected = connected


devices: dict[str, Device] = {}


# ---------------------------------------------------------------------------
# Rendering
# ---------------------------------------------------------------------------


def battery_icon(charge: int, charging: bool) -> str:
    if charging:
        return ICON_CHARGING
    for threshold, icon in ICONS_BAT:
        if charge >= threshold:
            return icon
    return ICONS_BAT[-1][1]


def render() -> None:
    # Find first connected device
    primary = None
    for d in devices.values():
        if d.connected:
            primary = d
            break

    if primary is None:
        print(
            json.dumps(
                {
                    "text": ICON_PHONE_OFF,
                    "tooltip": "kcd: no devices connected",
                    "class": "kcd-disconnected",
                    "percentage": 0,
                },
                ensure_ascii=False,
            ),
            flush=True,
        )
        return

    charge = primary.charge
    charging = primary.charging
    icon = battery_icon(charge, charging)
    text = f"{icon} {charge}%"
    css = "kcd-charging" if charging else ("kcd-low" if charge < 20 else "kcd-connected")

    lines = ["<b>KDE Connect</b>"]
    for d in devices.values():
        if not d.connected:
            continue
        state = f"charging {d.charge}%" if d.charging else f"battery {d.charge}%"
        lines.append(f"{d.name}  {state}  ({d.type})")

    print(
        json.dumps(
            {
                "text": text,
                "tooltip": "\n".join(lines),
                "class": css,
                "percentage": charge,
            },
            ensure_ascii=False,
        ),
        flush=True,
    )


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
# Socket connection
# ---------------------------------------------------------------------------


def connect_watch():
    """Connect to kcd daemon's Unix socket and subscribe to events."""
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    sock.settimeout(10)
    sock.connect(SOCKET_PATH)

    req = json.dumps({
        "cmd": "watch",
        "payload": {
            "events": [
                "device.connected",
                "device.disconnected",
                "battery.update",
                "pair.request",
                "pair.accepted",
            ]
        },
    })
    sock.sendall((req + "\n").encode())

    # Buffered reader around the socket for efficient readline()
    rfile = sock.makefile("r")

    resp_line = rfile.readline()
    if not resp_line:
        sock.close()
        raise ConnectionError("daemon closed connection")
    resp = json.loads(resp_line)
    if not resp.get("ok"):
        sock.close()
        raise ConnectionError(f"watch rejected: {resp.get('error')}")

    sock.settimeout(None)
    return sock, rfile


# ---------------------------------------------------------------------------
# Event handling
# ---------------------------------------------------------------------------


def handle_event(ev: dict) -> None:
    etype = ev.get("type", "")
    did = ev.get("deviceId", "")
    payload = ev.get("payload") or {}

    if etype == "device.connected":
        if did not in devices:
            devices[did] = Device(
                name=payload.get("name", did),
                type=payload.get("type", "phone"),
            )
        else:
            devices[did].connected = True
            if payload.get("name"):
                devices[did].name = payload["name"]
        render()

    elif etype == "device.disconnected":
        if did in devices:
            devices[did].connected = False
        render()

    elif etype == "battery.update":
        if did not in devices:
            devices[did] = Device(name=did)
        devices[did].charge = int(payload.get("charge", 0))
        devices[did].charging = bool(payload.get("charging", False))
        render()

    elif etype == "pair.request":
        name = payload.get("name", "Unknown device")
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


# ---------------------------------------------------------------------------
# Main loop
# ---------------------------------------------------------------------------


def main() -> None:
    global devices

    sock = None
    rfile = None

    while True:
        try:
            render()

            sock, rfile = connect_watch()

            while True:
                line = rfile.readline()
                if not line:
                    break
                line = line.strip()
                if not line:
                    continue

                try:
                    ev = json.loads(line)
                except json.JSONDecodeError:
                    continue
                handle_event(ev)

        except FileNotFoundError:
            render_error(f"kcd socket not found ({SOCKET_PATH})")
            time.sleep(RECONNECT_DELAY * 2)
            continue

        except (ConnectionError, OSError) as exc:
            render_error(f"kcd: {exc}")

        except Exception as exc:
            render_error(f"kcd-waybar error: {exc}")

        # Connection lost — mark devices disconnected
        for d in devices.values():
            d.connected = False
        render()

        if rfile is not None:
            try:
                rfile.close()
            except Exception:
                pass
            rfile = None
        if sock is not None:
            try:
                sock.close()
            except Exception:
                pass
            sock = None

        time.sleep(RECONNECT_DELAY)


if __name__ == "__main__":
    main()
