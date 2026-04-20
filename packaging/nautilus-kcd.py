import os
import json
import subprocess
import urllib.parse
import gi

try:
    gi.require_version("Nautilus", "4.0")
except ValueError:
    try:
        gi.require_version("Nautilus", "4.1")
    except ValueError:
        gi.require_version("Nautilus", "3.0")
from gi.repository import Nautilus, GObject


class KcdExtension(GObject.GObject, Nautilus.MenuProvider):
    def get_file_items(self, *args):
        files = args[-1]
        if not files:
            return

        # Query kcd for connected devices
        try:
            result = subprocess.run(
                ["kcd", "devices", "--json"], capture_output=True, text=True
            )
            if result.returncode != 0:
                return
            devices = json.loads(result.stdout)
            connected_devices = [d for d in devices if d.get("Connected")]
        except Exception:
            return

        if not connected_devices:
            return

        # Create the main "Send via KDE Connect" menu item
        menu_item = Nautilus.MenuItem(
            name="Kcd::Send", label="Send via KDE Connect", icon="kdeconnect"
        )
        submenu = Nautilus.Menu()
        menu_item.set_submenu(submenu)

        # Add a submenu item for each connected device
        for dev in connected_devices:
            item = Nautilus.MenuItem(
                name=f"Kcd::Device_{dev['ID']}", label=dev["Name"], icon="smartphone"
            )
            item.connect("activate", self.send_files, files, dev["ID"])
            submenu.append_item(item)

        return [menu_item]

    def send_files(self, menu, files, device_id):
        for f in files:
            # Extract local absolute path from the URI
            uri = f.get_uri()
            if uri.startswith("file://"):
                path = urllib.parse.unquote(uri[7:])
                # Send the file via kcd without blocking the file manager
                subprocess.Popen(["kcd", "share", device_id, path])
