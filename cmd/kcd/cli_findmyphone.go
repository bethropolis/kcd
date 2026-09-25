package main

import (
	"fmt"

	"github.com/urfave/cli/v2"
)

var findmyphoneCmd = &cli.Command{
	Name:      "findmyphone",
	Usage:     "Make the phone play a loud ringtone",
	ArgsUsage: "[device-id]",
	Action: func(c *cli.Context) error {
		cl, err := getClient(c)
		if err != nil {
			return err
		}

		targetID, err := resolveDeviceID(c, cl)
		if err != nil {
			return err
		}

		if err := cl.FindMyPhone(targetID); err != nil {
			return err
		}
		fmt.Println("Ring request sent")
		return nil
	},
}
