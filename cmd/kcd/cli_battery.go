package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bethropolis/kcd/pkg/client"
	"github.com/urfave/cli/v3"
)

var batteryCmd = &cli.Command{
	Name:      "battery",
	Usage:     "Fetch battery level and charging status",
	ArgsUsage: "[device-id]",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "json",
			Usage: "Output raw JSON",
		},
	},
	Action: func(ctx context.Context, cmd *cli.Command) error {
		cl, err := getClient(cmd)
		if err != nil {
			return err
		}
		return runBattery(cmd, cl)
	},
}

func runBattery(cmd *cli.Command, cl *client.Client) error {
	deviceID, err := resolveDeviceID(cmd, cl)
	if err != nil {
		return err
	}
	charge, charging, err := cl.Battery(deviceID)
	if err != nil {
		return err
	}
	if cmd.Bool("json") {
		// The resolved ID, not the raw positional, which is empty when
		// the device was auto-selected.
		out, _ := json.Marshal(map[string]interface{}{
			"deviceId": deviceID,
			"charge":   charge,
			"charging": charging,
		})
		fmt.Println(string(out))
		return nil
	}
	state := "discharging"
	if charging {
		state = "charging"
	}
	fmt.Printf("Battery: %d%% (%s)\n", charge, state)
	return nil
}
