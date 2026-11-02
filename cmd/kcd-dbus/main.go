// Command kcd-dbus exposes a small read-mostly KDE Connect D-Bus facade.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/pkg/client"
	"github.com/godbus/dbus/v5"
)

type adapter struct {
	client *client.Client
}

// IsDaemonRunning reports whether kcd answered the local status request.
func (a *adapter) IsDaemonRunning() (bool, *dbus.Error) {
	_, err := a.client.Status()
	return err == nil, nil
}

// Devices returns known device IDs for read-mostly integrations.
func (a *adapter) Devices() ([]string, *dbus.Error) {
	response, err := a.client.Call(ipc.CmdDevices, nil)
	if err != nil {
		return nil, dbus.MakeFailedError(err)
	}
	var devices []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Data, &devices); err != nil {
		return nil, dbus.MakeFailedError(err)
	}
	ids := make([]string, 0, len(devices))
	for _, device := range devices {
		ids = append(ids, device.ID)
	}
	return ids, nil
}

func main() {
	cfg, err := config.Load(config.DefaultConfigPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	conn, err := dbus.SessionBus()
	if err != nil {
		fmt.Fprintln(os.Stderr, "kcd-dbus: session bus unavailable:", err)
		os.Exit(1)
	}
	defer conn.Close()
	reply, err := conn.RequestName("org.kde.kdeconnect", dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		fmt.Fprintln(os.Stderr, "kcd-dbus: KDE Connect bus name unavailable")
		os.Exit(1)
	}
	cl := &client.Client{SocketPath: cfg.SocketPath}
	if err := conn.Export(&adapter{client: cl}, dbus.ObjectPath("/modules/kdeconnect"), "org.kde.kdeconnect"); err != nil {
		fmt.Fprintln(os.Stderr, "kcd-dbus: export failed:", err)
		os.Exit(1)
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
}
