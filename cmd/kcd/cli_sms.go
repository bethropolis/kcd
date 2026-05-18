package main

import (
	"fmt"
	"strconv"

	"github.com/urfave/cli/v2"
)

var smsCmd = &cli.Command{
	Name:  "sms",
	Usage: "Send and receive SMS via a device",
	Subcommands: []*cli.Command{
		{
			Name:      "send",
			Usage:     "Send an SMS via a device",
			ArgsUsage: "<device-id> <phone-number> <message>",
			Action: func(c *cli.Context) error {
				if c.NArg() < 3 {
					return fmt.Errorf("missing device ID, phone number, or message")
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				if err := cl.SendSMS(c.Args().Get(0), c.Args().Get(1), c.Args().Get(2)); err != nil {
					return err
				}
				fmt.Println("SMS request sent")
				return nil
			},
		},
		{
			Name:      "conversations",
			Usage:     "Request a list of all SMS conversations from a device",
			ArgsUsage: "<device-id>",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("missing device ID")
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				if err := cl.SmsRequestConversations(c.Args().Get(0)); err != nil {
					return err
				}
				fmt.Println("Conversation list requested. Use `kcd watch --events sms.incoming` to see results.")
				return nil
			},
		},
		{
			Name:      "conversation",
			Usage:     "Request messages from a specific SMS conversation thread",
			ArgsUsage: "<device-id> <thread-id>",
			Action: func(c *cli.Context) error {
				if c.NArg() < 2 {
					return fmt.Errorf("missing device ID or thread ID")
				}
				threadID, err := strconv.ParseInt(c.Args().Get(1), 10, 64)
				if err != nil {
					return fmt.Errorf("invalid thread ID: %w", err)
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				if err := cl.SmsRequestConversation(c.Args().Get(0), threadID); err != nil {
					return err
				}
				fmt.Println("Conversation requested. Use `kcd watch --events sms.incoming` to see results.")
				return nil
			},
		},
		{
			Name:      "attachment",
			Usage:     "Request an MMS attachment file from a device",
			ArgsUsage: "<device-id> <part-id> <unique-identifier>",
			Action: func(c *cli.Context) error {
				if c.NArg() < 3 {
					return fmt.Errorf("missing device ID, part ID, or unique identifier")
				}
				partID, err := strconv.ParseInt(c.Args().Get(1), 10, 64)
				if err != nil {
					return fmt.Errorf("invalid part ID: %w", err)
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				if err := cl.SmsRequestAttachment(c.Args().Get(0), partID, c.Args().Get(2)); err != nil {
					return err
				}
				fmt.Println("Attachment requested. File will arrive via `kcd watch --events sms.attachment`.")
				return nil
			},
		},
	},
}
