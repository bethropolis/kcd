package main

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"
)

var dismissCmd = &cli.Command{
	Name:      "dismiss",
	Usage:     "Clear a notification on the smartphone",
	ArgsUsage: "<device-id> <notification-id>",
	Action: func(ctx context.Context, cmd *cli.Command) error {
		if cmd.NArg() < 2 {
			return fmt.Errorf("missing device ID or notification ID")
		}
		cl, err := getClient(cmd)
		if err != nil {
			return err
		}
		if err := cl.NotifyDismiss(cmd.Args().Get(0), cmd.Args().Get(1)); err != nil {
			return err
		}
		fmt.Println("Notification dismissed")
		return nil
	},
}
