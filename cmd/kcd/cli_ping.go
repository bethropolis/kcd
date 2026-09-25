package main

import (
	"fmt"

	"github.com/urfave/cli/v2"
)

var pingCmd = &cli.Command{
	Name:      "ping",
	Usage:     "Send a ping notification to a device",
	ArgsUsage: "[device-id]",
	Action: func(c *cli.Context) error {
		cl, err := getClient(c)
		if err != nil {
			return err
		}
		deviceID, err := resolveDeviceID(c, cl)
		if err != nil {
			return err
		}
		if err := cl.Ping(deviceID); err != nil {
			return err
		}
		fmt.Println("Ping sent")
		return nil
	},
}
