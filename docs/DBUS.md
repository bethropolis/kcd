# D-Bus Adapter

`kcd-dbus` is a separate, optional compatibility process. It connects to kcd
through `pkg/client` and exports a small read-mostly facade at
`org.kde.kdeconnect` and `/modules/kdeconnect`.

It exposes `IsDaemonRunning` and `Devices`. It cannot run alongside a real
`kdeconnectd`, because both processes claim the same session-bus name. A
missing session bus is reported and does not affect the kcd daemon.