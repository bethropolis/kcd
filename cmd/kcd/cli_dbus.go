package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/pkg/client"
	"github.com/godbus/dbus/v5"
	"github.com/urfave/cli/v3"
)

type dbusAdapter struct {
	client *client.Client
}

// IsDaemonRunning reports whether kcd answered the local status request.
//
//nolint:unparam // Required signature convention for D-Bus method export.
func (a *dbusAdapter) IsDaemonRunning() (bool, *dbus.Error) {
	_, err := a.client.Status()
	return err == nil, nil
}

// Devices returns known device IDs for read-mostly integrations.
func (a *dbusAdapter) Devices() ([]string, *dbus.Error) {
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

var dbusCmd = &cli.Command{
	Name:   "dbus",
	Usage:  "Run the optional KDE Connect D-Bus compatibility adapter",
	Action: runDBusAdapter,
}

func runDBusAdapter(ctx context.Context, cmd *cli.Command) error {
	cfg, err := config.Load(configPathFor(cmd))
	if err != nil {
		return err
	}
	conn, err := dbus.SessionBus()
	if err != nil {
		return fmt.Errorf("kcd dbus: session bus unavailable: %w", err)
	}
	defer conn.Close()
	reply, err := conn.RequestName("org.kde.kdeconnect", dbus.NameFlagDoNotQueue)
	if err != nil {
		return fmt.Errorf("kcd dbus: request bus name: %w", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return fmt.Errorf("kcd dbus: KDE Connect bus name unavailable")
	}
	cl := &client.Client{SocketPath: cfg.SocketPath}
	if err := conn.Export(&dbusAdapter{client: cl}, dbus.ObjectPath("/modules/kdeconnect"), "org.kde.kdeconnect"); err != nil {
		return fmt.Errorf("kcd dbus: export adapter: %w", err)
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	select {
	case <-stop:
	case <-ctx.Done():
	}
	return nil
}
