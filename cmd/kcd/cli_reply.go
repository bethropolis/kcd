package main

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"
)

var replyCmd = &cli.Command{
	Name:      "reply",
	Usage:     "Send a text reply to a smartphone notification",
	ArgsUsage: "<device-id> <reply-id> <message>",
	Action: func(ctx context.Context, cmd *cli.Command) error {
		if cmd.NArg() < 3 {
			return fmt.Errorf("missing device ID, reply ID, or message")
		}
		cl, err := getClient(cmd)
		if err != nil {
			return err
		}
		if err := cl.NotifyReply(cmd.Args().Get(0), cmd.Args().Get(1), cmd.Args().Get(2)); err != nil {
			return err
		}
		fmt.Println("Notification reply sent")
		return nil
	},
}
