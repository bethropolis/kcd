package main

import (
	"fmt"

	"github.com/urfave/cli/v2"
)

var lockCmd = &cli.Command{
	Name:      "lock",
	Usage:     "Lock the current desktop session",
	ArgsUsage: "<device-id>",
	Action: func(c *cli.Context) error {
		if c.NArg() < 1 {
			return fmt.Errorf("missing device ID")
		}
		cl, err := getClient(c)
		if err != nil {
			return err
		}
		if err := cl.Lock(c.Args().First()); err != nil {
			return err
		}
		fmt.Println("Lock requested")
		return nil
	},
}
