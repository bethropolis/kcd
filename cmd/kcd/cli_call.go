package main

import (
	"fmt"

	"github.com/urfave/cli/v2"
)

var callCmd = &cli.Command{
	Name:  "call",
	Usage: "Manage phone calls",
	Subcommands: []*cli.Command{
		{
			Name:      "mute",
			Usage:     "Silence an incoming call",
			ArgsUsage: "<device-id>",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("missing device ID")
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				if err := cl.CallMute(c.Args().First()); err != nil {
					return err
				}
				fmt.Println("Call mute requested")
				return nil
			},
		},
	},
}
