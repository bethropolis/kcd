package main

import (
	"fmt"

	"github.com/urfave/cli/v2"
)

var lockCmd = &cli.Command{
	Name:      "lock",
	Usage:     "Lock the screen of a remote device",
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
		if err := cl.Lock(deviceID); err != nil {
			return err
		}
		fmt.Println("Lock requested")
		return nil
	},
}
