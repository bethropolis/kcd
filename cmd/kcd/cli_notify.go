package main

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"
)

var notifyCmd = &cli.Command{
	Name:  "notify",
	Usage: "Interact with notifications on a remote device",
	Commands: []*cli.Command{
		{
			Name:      "action",
			Usage:     "Trigger a notification action on a remote device",
			ArgsUsage: "<device-id> <notification-id> <action>",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if cmd.NArg() < 3 {
					return fmt.Errorf("missing device ID, notification ID, or action")
				}
				cl, err := getClient(cmd)
				if err != nil {
					return err
				}
				if err := cl.NotifyAction(cmd.Args().Get(0), cmd.Args().Get(1), cmd.Args().Get(2)); err != nil {
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
