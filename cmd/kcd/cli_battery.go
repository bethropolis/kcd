package main

import (
	"fmt"

	"github.com/urfave/cli/v2"
)

var batteryCmd = &cli.Command{
	Name:      "battery",
	Usage:     "Fetch battery level and charging status",
	ArgsUsage: "<device-id>",
	Action: func(c *cli.Context) error {
		if c.NArg() < 1 {
			return fmt.Errorf("missing device ID")
		}
		cl, err := getClient(c)
		if err != nil {
			return err
		}
		charge, charging, err := cl.Battery(c.Args().First())
		if err != nil {
			return err
		}
		state := "discharging"
		if charging {
			state = "charging"
		}
		fmt.Printf("Battery: %d%% (%s)\n", charge, state)
		return nil
	},
}
