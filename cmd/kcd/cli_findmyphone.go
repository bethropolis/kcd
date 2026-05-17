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

		targetID := c.Args().First()
		if targetID == "" {
			devs, err := cl.Devices()
			if err != nil {
				return err
			}
			for _, d := range devs {
				if d.Connected {
					targetID = d.ID
					break
				}
			}
			if targetID == "" {
				return fmt.Errorf("no connected devices found")
			}
		}

		if err := cl.FindMyPhone(targetID); err != nil {
			return err
		}
		fmt.Println("Ring request sent")
		return nil
	},
}
