package main

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"
)

var runCmd = &cli.Command{
	Name:  "run",
	Usage: "Execute and manage remote commands on the device",
	Commands: []*cli.Command{
		{
			Name:      "list",
			Usage:     "List available remote commands",
			ArgsUsage: "<device-id>",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if cmd.NArg() < 1 {
					return fmt.Errorf("missing device ID")
				}
				cl, err := getClient(cmd)
				if err != nil {
					return err
				}
				commands, err := cl.RunList(cmd.Args().First())
				if err != nil {
					return err
				}
				if len(commands) == 0 {
					fmt.Println("No commands available on this device.")
					return nil
				}
				for _, c := range commands {
					fmt.Printf("%s\t%s\n", c.Name, c.Command)
				}
				return nil
			},
		},
		{
			Name:      "exec",
			Usage:     "Execute a remote command by key",
			ArgsUsage: "<device-id> <command-key>",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if cmd.NArg() < 2 {
					return fmt.Errorf("missing device ID or command key")
				}
				cl, err := getClient(cmd)
				if err != nil {
					return err
				}
				if err := cl.RunExec(cmd.Args().Get(0), cmd.Args().Get(1)); err != nil {
					return err
				}
				fmt.Println("Command execution requested")
				return nil
			},
		},
	},
}
