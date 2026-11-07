package daemon

import (
	"net"
	"os"
	"strings"
)

// notifySystemdReady signals systemd that the daemon has finished initialization.
func notifySystemdReady() {
	notifySocket := os.Getenv("NOTIFY_SOCKET")
	if notifySocket == "" {
		return
	}
	addr := &net.UnixAddr{Name: notifySocket, Net: "unixgram"}
	if strings.HasPrefix(notifySocket, "@") {
		addr.Name = "\x00" + notifySocket[1:]
	}
	if conn, err := net.DialUnix("unixgram", nil, addr); err == nil {
		_, _ = conn.Write([]byte("READY=1\n"))
		conn.Close()
	}
}
