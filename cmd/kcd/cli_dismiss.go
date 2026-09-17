package main

import (
	"fmt"

	"github.com/urfave/cli/v2"
)

var dismissCmd = &cli.Command{
	Name:      "dismiss",
	Usage:     "Clear a notification on the smartphone",
	ArgsUsage: "<device-id> <notification-id>",
	Action: func(c *cli.Context) error {
		if c.NArg() < 2 {
			return fmt.Errorf("missing device ID or notification ID")
		}
		cl, err := getClient(c)
		if err != nil {
			return err
		}
		if err := cl.NotifyDismiss(c.Args().Get(0), c.Args().Get(1)); err != nil {
			return err
		}
		fmt.Println("Notification dismissed")
		return nil
	},
}
