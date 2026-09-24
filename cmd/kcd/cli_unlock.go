package main

import (
	"fmt"

	"github.com/urfave/cli/v2"
)

var unlockCmd = &cli.Command{
	Name:      "unlock",
	Usage:     "Unlock the screen of a remote device",
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
		if err := cl.Unlock(deviceID); err != nil {
			return err
		}
		fmt.Println("Unlock requested")
		return nil
	},
}
