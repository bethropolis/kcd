package main

import (
	"fmt"
	"path/filepath"

	"github.com/urfave/cli/v2"
)

var shareCmd = &cli.Command{
	Name:      "share",
	Usage:     "Send a local file to a device",
	ArgsUsage: "<device-id> <file-path>",
	Action: func(c *cli.Context) error {
		if c.NArg() < 2 {
			return fmt.Errorf("missing device ID or file path")
		}
		cl, err := getClient(c)
		if err != nil {
			return err
		}
		absPath, err := filepath.Abs(c.Args().Get(1))
		if err != nil {
			return fmt.Errorf("invalid file path: %w", err)
		}
		if err := cl.ShareFile(c.Args().Get(0), absPath); err != nil {
			return err
		}
		fmt.Println("File share requested")
		return nil
	},
}
