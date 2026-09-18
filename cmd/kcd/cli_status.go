package main

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bethropolis/kcd/internal/ipc"
)

// formatStatus renders the human-readable `kcd status` output.
func formatStatus(st *ipc.StatusResponse) string {
	var b strings.Builder
	fmt.Fprintf(&b, "kcd %s (up %s)\n", st.Version, st.UptimeHuman)
	fmt.Fprintf(&b, "\nSocket:   %s\n", st.SocketPath)
	fmt.Fprintf(&b, "Config:   %s\n", st.ConfigPath)
	if st.TCPPort > 0 {
		fmt.Fprintf(&b, "Listen:   tcp :%d\n", st.TCPPort)
	}
	fmt.Fprintf(&b, "\nDevices:  %d known, %d connected\n", st.DeviceCount, st.ConnectedCount)
	if len(st.Devices) > 0 {
		w := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tID\tTYPE\tSTATE\tADDR\tBATTERY\tLAST SEEN")
		for _, d := range st.Devices {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				d.Name, shortDeviceID(d.ID), d.Type, d.State,
				orDash(d.Addr), formatStatusBattery(d.Battery), formatSeenAge(d.LastSeen))
		}
		w.Flush()
	}
	fmt.Fprintf(&b, "\nPlugins (%d): %s\n", len(st.Plugins), strings.Join(st.Plugins, ", "))
	return b.String()
}

// shortDeviceID shows the leading segment of a device ID.
func shortDeviceID(id string) string {
	if i := strings.Index(id, "_"); i > 0 {
		return id[:i]
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// formatStatusBattery renders a cached reading, or a dash when unknown.
func formatStatusBattery(b *ipc.StatusBattery) string {
	if b == nil {
		return "—"
	}
	if b.Charging {
		return fmt.Sprintf("%d%%+", b.Charge)
	}
	return fmt.Sprintf("%d%%", b.Charge)
}

// formatSeenAge renders an RFC3339 timestamp as a relative age.
func formatSeenAge(ts string) string {
	if ts == "" {
		return "never"
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	d := time.Since(t)
	switch {
	case d < 0:
		return "now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return t.Format("2006-01-02")
	}
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
