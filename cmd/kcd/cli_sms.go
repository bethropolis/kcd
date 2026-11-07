package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/bethropolis/kcd/pkg/client"
	"github.com/urfave/cli/v3"
)

var smsCmd = &cli.Command{
	Name:  "sms",
	Usage: "Send and receive SMS via a device",
	Commands: []*cli.Command{
		{
			Name:      "send",
			Usage:     "Send an SMS via a device",
			ArgsUsage: "<device-id> <phone-number> <message>",
			Flags: []cli.Flag{
				&cli.Int64Flag{
					Name:  "sub-id",
					Usage: "SIM subscription ID to send from",
				},
			},
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if cmd.NArg() < 3 {
					return fmt.Errorf("missing device ID, phone number, or message")
				}
				cl, err := getClient(cmd)
				if err != nil {
					return err
				}
				var subID []int64
				if cmd.IsSet("sub-id") {
					subID = append(subID, cmd.Int64("sub-id"))
				}
				if err := cl.SendSMS(cmd.Args().Get(0), cmd.Args().Get(1), cmd.Args().Get(2), subID...); err != nil {
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
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if cmd.NArg() < 1 {
					return fmt.Errorf("missing device ID")
				}
				cl, err := getClient(cmd)
				if err != nil {
					return err
				}
				if err := cl.SmsRequestConversations(cmd.Args().Get(0)); err != nil {
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
			Flags: []cli.Flag{
				&cli.Int64Flag{
					Name:  "before",
					Usage: "Only messages at or before this ms epoch (rangeStartTimestamp)",
				},
				&cli.Int64Flag{
					Name:  "limit",
					Usage: "Cap how many messages, newest first (numberToRequest)",
				},
			},
			Action: func(ctx context.Context, cmd *cli.Command) error {
				cl, err := getClient(cmd)
				if err != nil {
					return err
				}
				return runSmsConversation(cmd, cl)
			},
		},
		{
			Name:      "attachment",
			Usage:     "Request an MMS attachment file from a device",
			ArgsUsage: "<device-id> <part-id> <unique-identifier>",
			Flags: []cli.Flag{
				&cli.Int64Flag{
					Name:  "thread-id",
					Usage: "Conversation thread ID this attachment belongs to",
				},
			},
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if cmd.NArg() < 3 {
					return fmt.Errorf("missing device ID, part ID, or unique identifier")
				}
				partID, err := strconv.ParseInt(cmd.Args().Get(1), 10, 64)
				if err != nil {
					return fmt.Errorf("invalid part ID: %w", err)
				}
				cl, err := getClient(cmd)
				if err != nil {
					return err
				}
				var threadID []int64
				if cmd.IsSet("thread-id") {
					threadID = append(threadID, cmd.Int64("thread-id"))
				}
				if err := cl.SmsRequestAttachment(cmd.Args().Get(0), partID, cmd.Args().Get(2), threadID...); err != nil {
					return err
				}
				fmt.Println("Attachment requested. File will arrive via `kcd watch --events sms.attachment`.")
				return nil
			},
		},
	},
}

// runSmsConversation requests one thread with an optional window. IsSet
// (not a zero default) decides: the phone answers an explicit 0 with nothing.
func runSmsConversation(cmd *cli.Command, cl *client.Client) error {
	if cmd.NArg() < 2 {
		return fmt.Errorf("missing device ID or thread ID")
	}
	threadID, err := strconv.ParseInt(cmd.Args().Get(1), 10, 64)
	if err != nil {
		return fmt.Errorf("invalid thread ID: %w", err)
	}
	var before, limit *int64
	if cmd.IsSet("before") {
		v := cmd.Int64("before")
		if v < 0 {
			return fmt.Errorf("invalid --before %d: ms epoch, must not be negative", v)
		}
		before = &v
	}
	if cmd.IsSet("limit") {
		v := cmd.Int64("limit")
		if v < 0 {
			return fmt.Errorf("invalid --limit %d: must not be negative", v)
		}
		limit = &v
	}
	if err := cl.SmsRequestConversationRange(cmd.Args().Get(0), threadID, before, limit); err != nil {
		return err
	}
	fmt.Println("Conversation requested. Use `kcd watch --events sms.incoming` to see results.")
	return nil
}
