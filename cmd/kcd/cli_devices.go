package main

import (
	"encoding/json"
	"fmt"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/urfave/cli/v2"
)

var devicesCmd = &cli.Command{

	Name:  "devices",
	Usage: "List known devices",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "json",
			Usage: "Output in JSON format",
		},
		&cli.BoolFlag{
			Name:    "watch",
			Aliases: []string{"w"},
			Usage:   "Stream device changes in real time",
		},
		&cli.BoolFlag{
			Name:  "connected",
			Usage: "Only show paired devices (including offline ones)",
		},
	},
	Action: func(c *cli.Context) error {
		cl, err := getClient(c)
		if err != nil {
			return err
		}
		if c.Bool("watch") {
			return watchDevices(c, cl)
		}
		devices, err := cl.Devices()
		if err != nil {
			return err
		}
		if c.Bool("connected") {
			filtered := make([]device.DeviceInfo, 0, len(devices))
			for _, d := range devices {
				// Paired devices show even when offline — the CONNECTED
				// column carries liveness. Unpaired strangers (which may
				// briefly hold a raw TCP connection) are hidden.
				if d.State == device.StatePaired {
					filtered = append(filtered, d)
				}
			}
			devices = filtered
		}
		if c.Bool("json") {
			data, _ := json.MarshalIndent(devices, "", "  ")
			fmt.Println(string(data))
			return nil
		}
		printDeviceTable(devices)
		return nil
	},
}

func printDeviceTable(devices []device.DeviceInfo) {
	if len(devices) == 0 {
		fmt.Println("No devices found.")
		return
	}
	fmt.Printf("%-36s %-20s %-10s %-10s %s\n", "DEVICE ID", "NAME", "TYPE", "STATE", "CONNECTED")
	fmt.Println("---------------------------------------------------------------------------------------------------")
	for _, d := range devices {
		fmt.Printf("%-36s %-20s %-10s %-10s %v\n", d.ID, protocol.DisplayName(d.Name), d.Type, d.State, d.Connected)
	}
}
