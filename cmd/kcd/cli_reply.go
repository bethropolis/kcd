package main

import (
	"fmt"

	"github.com/urfave/cli/v2"
)

var replyCmd = &cli.Command{
	Name:      "reply",
	Usage:     "Send a text reply to a smartphone notification",
	ArgsUsage: "<device-id> <reply-id> <message>",
	Action: func(c *cli.Context) error {
		if c.NArg() < 3 {
			return fmt.Errorf("missing device ID, reply ID, or message")
		}
		cl, err := getClient(c)
		if err != nil {
			return err
		}
		if err := cl.NotifyReply(c.Args().Get(0), c.Args().Get(1), c.Args().Get(2)); err != nil {
			return err
		}
		fmt.Println("Notification reply sent")
		return nil
	},
}
