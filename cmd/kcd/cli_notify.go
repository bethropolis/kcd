package main

import (
	"fmt"

	"github.com/urfave/cli/v2"
)

var notifyCmd = &cli.Command{
	Name:  "notify",
	Usage: "Interact with notifications on a remote device",
	Subcommands: []*cli.Command{
		{
			Name:      "action",
			Usage:     "Trigger a notification action on a remote device",
			ArgsUsage: "<device-id> <notification-id> <action>",
			Action: func(c *cli.Context) error {
				if c.NArg() < 3 {
					return fmt.Errorf("missing device ID, notification ID, or action")
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				if err := cl.NotifyAction(c.Args().Get(0), c.Args().Get(1), c.Args().Get(2)); err != nil {
					return err
				}
				fmt.Println("Notification action sent")
				return nil
			},
		},
		{
			Name:      "reply",
			Usage:     "Send a text reply to a smartphone notification",
			ArgsUsage: "<device-id> <reply-id> <message>",
			Action:    replyCmd.Action,
		},
		{
			Name:      "dismiss",
			Usage:     "Clear a notification on the smartphone",
			ArgsUsage: "<device-id> <notification-id>",
			Action:    dismissCmd.Action,
		},
	},
}
