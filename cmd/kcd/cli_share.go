package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/urfave/cli/v3"
)

var shareCmd = &cli.Command{
	Name:      "share",
	Usage:     "Send a local file to a device",
	ArgsUsage: "<device-id> <file-path>",
	Action: func(ctx context.Context, cmd *cli.Command) error {
		if cmd.NArg() < 2 {
			return fmt.Errorf("missing device ID or file path")
		}
		cl, err := getClient(cmd)
		if err != nil {
			return err
		}
		absPath, err := filepath.Abs(cmd.Args().Get(1))
		if err != nil {
			return fmt.Errorf("invalid file path: %w", err)
		}
		if stat, err := os.Stat(absPath); err == nil && stat.IsDir() {
			return fmt.Errorf("share: directory transfer is not supported")
		}
		if err := cl.ShareFile(cmd.Args().Get(0), absPath); err != nil {
			return err
		}
		fmt.Println("File share requested")
		return nil
	},
}
