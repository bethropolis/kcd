# D-Bus Adapter

`kcd dbus` is an optional compatibility process launched from the same static
`kcd` executable. It connects to the daemon through `pkg/client` and exports a small read-mostly facade at
`org.kde.kdeconnect` and `/modules/kdeconnect`.

It exposes `IsDaemonRunning` and `Devices`, allowing desktop panels and widgets to inspect connection status. It cannot run alongside a real
`kdeconnectd`, because both processes claim the same session-bus name. A
missing session bus is reported and does not affect the kcd daemon.
