package main

import (
	"fmt"

	"github.com/urfave/cli/v2"
)

var runCmd = &cli.Command{
	Name:  "run",
	Usage: "Execute and manage remote commands on the device",
	Subcommands: []*cli.Command{
		{
			Name:      "list",
			Usage:     "List available remote commands",
			ArgsUsage: "<device-id>",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("missing device ID")
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				if err := cl.RunList(c.Args().First()); err != nil {
					return err
				}
				fmt.Println("Command list requested. Run 'kcd watch' to see results.")
				return nil
			},
		},
		{
			Name:      "exec",
			Usage:     "Execute a remote command by key",
			ArgsUsage: "<device-id> <command-key>",
			Action: func(c *cli.Context) error {
				if c.NArg() < 2 {
					return fmt.Errorf("missing device ID or command key")
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				if err := cl.RunExec(c.Args().Get(0), c.Args().Get(1)); err != nil {
					return err
				}
				fmt.Println("Command execution requested")
				return nil
			},
		},
	},
}
