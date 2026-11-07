# KDE Connect Protocol v8 Compatibility Matrix & Deviations

This document tracks `kcd`'s support for the KDE Connect protocol v8 specification
(defined in `kdeconnect-meta`), compatibility with official Android and desktop clients,
and documented intentional protocol deviations.

---

## 1. Packet Types Support Matrix

| Packet Type | Inbound | Outbound | Plugin | Notes |
|---|:---:|:---:|---|---|
| `kdeconnect.identity` | ✓ | ✓ | Core | Pre-TLS discovery and post-TLS full capability exchange. |
| `kdeconnect.pair` | ✓ | ✓ | `pair` | SHA-256 certificate fingerprint verification and pairing requests. |
| `kdeconnect.ping` | ✓ | ✓ | `ping` | Ping connectivity check and notification display. |
| `kdeconnect.battery` | ✓ | ✗ | `battery` | Battery charge and charging status updates from phone. |
| `kdeconnect.battery.request` | ✗ | ✓ | `battery` | Requests current battery status from remote peer. |
| `kdeconnect.clipboard` | ✓ | ✓ | `clipboard` | Bi-directional clipboard synchronisation. |
| `kdeconnect.clipboard.connect` | ✓ | ✓ | `clipboard` | Requests remote clipboard state immediately upon connection. |
| `kdeconnect.clipboard.file` | ✓ | ✓ | `clipboard` | File-backed clipboard transfers over TLS side-channel. |
| `kdeconnect.connectivity_report` | ✓ | ✗ | `connectivity` | Cellular signal strength and network type reporting. |
| `kdeconnect.connectivity_report.request` | ✗ | ✓ | `connectivity` | Requests remote network connectivity status. |
| `kdeconnect.contacts.request_uids_timestamps` | ✗ | ✓ | `contacts` | Requests list of contact IDs and modification timestamps. |
| `kdeconnect.contacts.request_all_uids_timestamps` | ✗ | ✓ | `contacts` | Legacy full UID timestamp request format. |
| `kdeconnect.contacts.response_uids_timestamps` | ✓ | ✗ | `contacts` | Receives contact IDs and timestamps. |
| `kdeconnect.contacts.request_vcards_by_uid` | ✗ | ✓ | `contacts` | Requests vCards for specified contact IDs. |
| `kdeconnect.contacts.response_vcards` | ✓ | ✗ | `contacts` | Receives requested vCard contact details. |
| `kdeconnect.digitizer` | ✓ | ✗ | `digitizer` | Receives drawing tablet point and pressure events. |
| `kdeconnect.digitizer.session` | ✓ | ✗ | `digitizer` | Receives drawing tablet session start/end signals. |
| `kdeconnect.findmyphone.request` | ✗ | ✓ | `findmyphone` | Requests phone to ring loudly to locate it. |
| `kdeconnect.findthisdevice.request` | ✓ | ✗ | `findthisdevice` | Triggers local desktop audio alert when requested by phone. |
| `kdeconnect.lock` | ✓ | ✗ | `lockdevice` | Reports desktop lock status (`isLocked`). |
| `kdeconnect.lock.request` | ✓ | ✓ | `lockdevice` | Inbound and outbound session lock/unlock commands. |
| `kdeconnect.mousepad.request` | ✓ | ✗ | `mousepad` | Mouse movement, clicks, scrolling, and keyboard input emulation. |
| `kdeconnect.mousepad.keyboardstate` | ✗ | ✓ | `mousepad` | Reports current keyboard modifier and lock state. |
| `kdeconnect.mpris` | ✓ | ✓ | `mpris` | Media player metadata, playback state, and volume updates. |
| `kdeconnect.mpris.request` | ✓ | ✓ | `mpris` | Media control actions (play, pause, next, prev, seek, volume). |
| `kdeconnect.notification` | ✓ | ✓ | `notification` | Inbound phone notifications; outbound desktop notifications. |
| `kdeconnect.notification.request` | ✗ | ✓ | `notification` | Requests active notification list from phone. |
| `kdeconnect.notification.reply` | ✗ | ✓ | `notification` | Sends replies to remote notification action threads. |
| `kdeconnect.notification.action` | ✗ | ✓ | `notification` | Triggers predefined action on remote notification. |
| `kdeconnect.presenter` | ✓ | ✗ | `presenter` | Pointer movement and slide navigation for presentations. |
| `kdeconnect.runcommand` | ✓ | ✓ | `runcommand` | Advertises configured commands to phone; receives remote commands. |
| `kdeconnect.runcommand.request` | ✓ | ✓ | `runcommand` | Requests command list or execution of a remote command. |
| `kdeconnect.runcommand.output` | ✓ | ✓ | `runcommand` | Streams command stdout/stderr output batches and exit code. |
| `kdeconnect.sftp` | ✓ | ✗ | `sftp` | Receives SFTP mount credentials and multi-path storage roots. |
| `kdeconnect.sftp.request` | ✗ | ✓ | `sftp` | Requests phone to start SFTP server. |
| `kdeconnect.share.request` | ✓ | ✓ | `share` | Streams file transfers and shares plain text or URLs. |
| `kdeconnect.share.request.update` | ✗ | ✓ | `share` | Optional file transfer progress updates. |
| `kdeconnect.sms.messages` | ✓ | ✗ | `sms` | Receives batches of SMS/MMS messages and conversation threads. |
| `kdeconnect.sms.request` | ✗ | ✓ | `sms` | Sends an outbound SMS message via the phone. |
| `kdeconnect.sms.request_conversations` | ✗ | ✓ | `sms` | Requests list of message conversation threads. |
| `kdeconnect.sms.request_conversation` | ✗ | ✓ | `sms` | Requests messages within a single conversation thread. |
| `kdeconnect.sms.request_attachment` | ✗ | ✓ | `sms` | Requests MMS attachment binary stream. |
| `kdeconnect.sms.attachment_file` | ✓ | ✗ | `sms` | Receives MMS attachment file over side-channel. |
| `kdeconnect.systemvolume` | ✓ | ✓ | `systemvolume` | Controls and observes system audio sink volumes and mute state. |
| `kdeconnect.systemvolume.request` | ✓ | ✓ | `systemvolume` | Adjusts volume or mute state on remote peer. |
| `kdeconnect.telephony` | ✓ | ✗ | `telephony` | Receives incoming call ringing, missed, or canceled events. |
| `kdeconnect.telephony.request_mute` | ✗ | ✓ | `telephony` | Requests phone to mute an incoming call ringtone. |

---

## 2. Intentional Deviations & Rationale

1. **Target Device ID Mismatch Handled Gracefully**
   - *Protocol Spec:* Section "Device Connection" dictates aborting if `targetDeviceId` doesn't match the local device ID.
   - *Deviation:* `kcd` logs a warning and drops mismatched pre-auth packets without tearing down valid connection attempts from peers caching old IDs or broadcast relays.

2. **RunCommand `command` Field Decoupled from Shell Execution**
   - *Protocol Spec / Reference:* Wire schemas declare `command` as the command string to run.
   - *Deviation:* For security against remote arbitrary code execution, `kcd` treats the `command` field strictly as an opaque label matching keys configured locally in `[commands]` in `kcd.toml`. Only allowlisted commands configured by the local user are ever executed.

3. **Share `open` Flag Ignored**
   - *Deviation:* When receiving files or URLs via `kdeconnect.share.request`, the `open: true` flag is ignored. Received files are saved to the designated download directory without auto-executing or opening default handlers.

4. **Contacts Empty UID Toleration**
   - *Deviation:* If an Android contact card lacks a unique ID (`uids`), `kcd` synthesizes a deterministic identifier rather than failing the entire batch, ensuring partial contact imports succeed.

5. **Device ID Format Flexibility**
   - *Deviation:* Standard KDE Connect uses UUIDs or hex hashes. `kcd` generates RFC 4122 v4 UUIDs prefixed with `_` or alphanumeric IDs, and accepts any peer ID matching `^[A-Za-z0-9_-]{32,38}$` to maintain strict path safety while supporting both Android (32 hex characters) and desktop UUID (36–38 characters) representations.

---

## 3. Known Protocol & Platform Gaps

- **Un-arming Phone SMS Streams:** Android's `haveMessagesBeenRequested` flag in `SMSHelper` is set upon receiving any conversation request and is never cleared until connection teardown. `kcd` mitigates this by requiring explicit opt-in subscriber filtering for SMS events (`events.OptInOnly()`).
- **Headless Desktop Dependencies:** Features requiring audio alerts (`paplay`/`aplay`), display notifications (`notify-send`), lock screen (`loginctl`), or input simulation (`uinput`, `ydotool`, `xdotool`) degrade gracefully with logged warnings when the corresponding desktop subsystem is unavailable.
