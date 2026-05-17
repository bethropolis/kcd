package main

import (
	"fmt"

	"github.com/urfave/cli/v2"
)

var smsCmd = &cli.Command{
	Name:      "sms",
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
}
