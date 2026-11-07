package main

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"
)

var callCmd = &cli.Command{
	Name:  "call",
	Usage: "Manage phone calls",
	Commands: []*cli.Command{
		{
			Name:      "mute",
			Usage:     "Silence an incoming call",
			ArgsUsage: "<device-id>",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if cmd.NArg() < 1 {
					return fmt.Errorf("missing device ID")
				}
				cl, err := getClient(cmd)
				if err != nil {
					return err
				}
				if err := cl.CallMute(cmd.Args().First()); err != nil {
					return err
				}
				fmt.Println("Call mute requested")
				return nil
			},
		},
	},
}
