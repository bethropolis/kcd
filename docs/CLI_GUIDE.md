# kcd CLI Guide

`kcd` is a single headless Go binary that implements both the KDE Connect daemon and the client CLI.

## Running the Daemon

The daemon processes incoming connections, manages paired devices, and acts as the central hub for plugins. It needs to run continuously in the background.

```bash
# Start the daemon in the foreground (useful for debugging)
kcd daemon
```

### Running as a systemd service

To run `kcd` as a background service using systemd on Linux:

1. Create a file `~/.config/systemd/user/kcd.service`:

```ini
[Unit]
Description=KDE Connect Daemon (kcd)
After=network.target

[Service]
Type=simple
ExecStart=%h/.local/bin/kcd daemon
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
```

2. Enable and start the service:

```bash
systemctl --user daemon-reload
systemctl --user enable --now kcd
```

## Client Commands

You can use the `kcd` binary as a CLI tool to communicate with the running daemon via Unix socket IPC.

### Device Management

#### Listing Connected Devices
To see all paired and visible devices along with their IDs and current states:
```bash
kcd devices
```
*   `--json`: Output in computer-readable JSON format.

#### Pairing and Unpairing
Establish or revoke trust with a remote device.
```bash
# Send a pair request to a device
kcd pair <deviceId>

# Unpair a previously paired device
kcd unpair <deviceId>
```

### System Integration

#### Locking and Unlocking
Control the PC's session lock status from the CLI or phone.
```bash
# Lock the session
kcd lock <deviceId>

# Unlock the session
kcd unlock <deviceId>
```

#### Find My Phone
Make the phone ring loudly to help locate it.
```bash
kcd findmyphone <deviceId>
```

#### Media & Calls
Silence an incoming call on the remote device.
```bash
kcd call mute <deviceId>
```

### Data Sync & Transfers

#### Sending a File (Share)
Send a file from your local filesystem to a connected device:
```bash
kcd share <deviceId> /path/to/file.txt
```

#### Pushing the Clipboard
Send your current local clipboard content to a connected device:
```bash
kcd clipboard <deviceId>
```

#### SMS (Texting)
Send short messages from the desktop via the phone's cellular connection.
```bash
kcd sms <deviceId> <phoneNumber> "Message content"
```

#### Remote Command Execution
List and execute predefined commands on the remote device.
```bash
# Get the list of available commands (output appears in 'kcd watch')
kcd run list <deviceId>

# Execute a command by its key
kcd run exec <deviceId> <commandKey>
```

### Advanced Features

#### Monitoring Events
Watch real-time events from the daemon (notifications, battery updates, transfer progress).
```bash
kcd watch
```
*   `-e`, `--events`: Filter by event type (e.g., `-e notification -e battery.update`).
*   `--json`: Output raw NDJSON for script consumption.

#### Notification Replies
Send a text reply to an active notification on the phone.
```bash
kcd reply <deviceId> <replyId> "Your message here"
```
*The `replyId` can be found by monitoring `kcd watch` when a notification arrives.*

#### SFTP Mounting
Request credentials or physically mount the phone's filesystem.

```bash
# Request credentials (URI with password)
kcd sftp request <deviceId>

# Physically mount to /tmp/kcd-sftp-<deviceId> via sshfs
kcd sftp mount <deviceId>
```
*The `request` command will publish the SFTP URI to the event bus. Watch `kcd watch` to receive it.*

### Diagnostic Commands

#### Battery Status
Fetch current battery level and charging state.
```bash
kcd battery <deviceId>
```

#### Ping
Send a ping notification to verify connection.
```bash
kcd ping <deviceId>
```
