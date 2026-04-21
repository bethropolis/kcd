"""
nautilus-kcd.py — Send files to KDE Connect devices from Nautilus / Files.

Installation:
    mkdir -p ~/.local/share/nautilus-python/extensions
    cp nautilus-kcd.py ~/.local/share/nautilus-python/extensions/
    nautilus -q   # restart Nautilus to load the extension

Requirements:
    - kcd daemon running  (systemctl --user status kcd)
    - python3-nautilus    (Nautilus Python bindings)
"""

import json
import os
import subprocess
import threading
import urllib.parse

import gi

# Support Nautilus 3.x, 4.0, and 4.0+ API variants.
for version in ("4.0", "3.0"):
    try:
        gi.require_version("Nautilus", version)
        break
    except ValueError:
        continue

gi.require_version("GLib", "2.0")
from gi.repository import GLib, GObject, Nautilus  # noqa: E402


# ── helpers ───────────────────────────────────────────────────────────────────

def _notify(summary: str, body: str = "", icon: str = "kdeconnect") -> None:
    """Fire a desktop notification without blocking."""
    try:
        subprocess.Popen(
            ["notify-send", "--app-name=kcd", f"--icon={icon}", summary, body],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
    except FileNotFoundError:
        pass  # notify-send not installed — silently skip


def _kcd(*args, timeout: int = 5) -> subprocess.CompletedProcess | None:
    """Run a kcd sub-command and return the CompletedProcess, or None on error."""
    # Try searching in PATH first, then fall back to ~/.local/bin/kcd
    cmd = "kcd"
    if not any(os.access(os.path.join(p, cmd), os.X_OK) for p in os.environ.get("PATH", "").split(os.pathsep)):
        local_bin = os.path.expanduser("~/.local/bin/kcd")
        if os.path.exists(local_bin):
            cmd = local_bin

    try:
        return subprocess.run(
            [cmd, *args],
            capture_output=True,
            text=True,
            timeout=timeout,
        )
    except (FileNotFoundError, subprocess.TimeoutExpired):
        return None


def _connected_devices() -> list[dict]:
    """Return the list of currently connected+paired devices from `kcd devices --json`."""
    result = _kcd("devices", "--json")
    if result is None or result.returncode != 0:
        return []
    try:
        devices = json.loads(result.stdout)
        return [d for d in devices if d.get("connected") and d.get("state") == "PAIRED"]
    except (json.JSONDecodeError, AttributeError):
        return []


def _uri_to_path(uri: str) -> str | None:
    """Convert a file:// URI to a local absolute path, or return None."""
    if not uri.startswith("file://"):
        return None
    return urllib.parse.unquote(uri[len("file://"):])


# ── Nautilus extension ────────────────────────────────────────────────────────

class KcdSendExtension(GObject.GObject, Nautilus.MenuProvider):
    """Adds a 'Send via KDE Connect' right-click menu to files in Nautilus."""
    # Cache devices for a short window so rapid right-clicks don't hammer kcd.
    _cache: list[dict] = []
    _cache_lock = threading.Lock()
    _cache_ttl: float = 0.0
    _CACHE_SECONDS: float = 3.0

    def _get_devices(self) -> list[dict]:
        import time
        now = time.monotonic()
        with self._cache_lock:
            if now < self._cache_ttl and self._cache:
                return list(self._cache)
        devices = _connected_devices()
        with self._cache_lock:
            self._cache = devices
            self._cache_ttl = now + self._CACHE_SECONDS
        return devices

    # Nautilus 3.x passes (files,); 4.x passes (window, files).
    def get_file_items(self, *args):
        files = args[-1]
        if not files:
            return []

        # Only show the menu for local files.
        paths = [_uri_to_path(f.get_uri()) for f in files]
        paths = [p for p in paths if p and os.path.exists(p)]
        if not paths:
            return []

        devices = self._get_devices()
        if not devices:
            return []

        top = Nautilus.MenuItem(
            name="Kcd::SendTop",
            label="Send via KDE Connect",
            tip="Send selected file(s) to a paired KDE Connect device",
            icon="kdeconnect",
        )

        if len(devices) == 1:
            # Single device — no submenu, activate directly.
            dev = devices[0]
            top.set_property("label", f"Send to {dev['name']}")
            top.connect("activate", self._on_send, paths, dev)
        else:
            submenu = Nautilus.Menu()
            top.set_submenu(submenu)
            for dev in devices:
                item = Nautilus.MenuItem(
                    name=f"Kcd::Device_{dev['id']}",
                    label=dev["name"],
                    icon="phone",
                )
                item.connect("activate", self._on_send, paths, dev)
                submenu.append_item(item)

        return [top]

    def _on_send(self, _menu_item, paths: list[str], dev: dict) -> None:
        """Kick off transfers in a background thread so the UI stays responsive."""
        threading.Thread(
            target=self._send_files,
            args=(paths, dev),
            daemon=True,
        ).start()

    def _send_files(self, paths: list[str], dev: dict) -> None:
        device_name = dev.get("name", dev["id"])
        device_id   = dev["id"]
        total        = len(paths)
        failed       = []

        for path in paths:
            filename = os.path.basename(path)
            result = _kcd("share", device_id, path, timeout=300)
            if result is None or result.returncode != 0:
                failed.append(filename)

        # Report outcome via desktop notification.
        GLib.idle_add(self._report, total, failed, device_name)

    @staticmethod
    def _report(total: int, failed: list[str], device_name: str) -> bool:
        ok = total - len(failed)
        if not failed:
            summary = f"Sent to {device_name}"
            body    = (
                f"{ok} file sent successfully."
                if ok == 1
                else f"{ok} files sent successfully."
            )
            _notify(summary, body, icon="emblem-ok-symbolic")
        else:
            summary = f"Transfer to {device_name} incomplete"
            body    = "\n".join([
                f"{ok}/{total} file(s) sent.",
                "Failed: " + ", ".join(failed),
            ])
            _notify(summary, body, icon="dialog-error-symbolic")
        return False  # Remove from GLib idle queue
