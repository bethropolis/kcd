package main

import (
	"encoding/json"
	"fmt"

	"github.com/urfave/cli/v2"
)

var batteryCmd = &cli.Command{
	Name:      "battery",
	Usage:     "Fetch battery level and charging status",
	ArgsUsage: "<device-id>",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "json",
			Usage: "Output raw JSON",
		},
	},
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
		if c.Bool("json") {
			out, _ := json.Marshal(map[string]interface{}{
				"deviceId": c.Args().First(),
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
	},
}
