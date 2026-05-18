package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/urfave/cli/v2"
)

var clipboardCmd = &cli.Command{
	Name:      "clipboard",
	Usage:     "Sync local clipboard content to a device",
	ArgsUsage: "[device-id]",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:    "watch",
			Aliases: []string{"w"},
			Usage:   "Watch for local clipboard changes and auto-sync to connected devices",
		},
	},
	Action: func(c *cli.Context) error {
		if c.Bool("watch") {
			return clipboardWatch()
		}

		cl, err := getClient(c)
		if err != nil {
			return err
		}

		var targetID string
		if c.NArg() >= 1 {
			targetID = c.Args().First()
		} else {
			devs, err := cl.Devices()
			if err != nil {
				return err
			}
			for _, d := range devs {
				if d.Connected {
					targetID = d.ID
					break
				}
			}
			if targetID == "" {
				return fmt.Errorf("no connected devices found")
			}
		}

		if err := cl.ClipboardPush(targetID); err != nil {
			return err
		}
		fmt.Printf("Clipboard pushed to %s\n", targetID)
		return nil
	},
}

func clipboardWatch() error {
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		return fmt.Errorf("clipboard --watch requires a Wayland session (WAYLAND_DISPLAY not set)")
	}

	self, err := os.Executable()
	if err != nil {
		self = "kcd"
	}

	fmt.Println("Watching for clipboard changes (Ctrl+C to stop)…")

	for {
		cmd := exec.CommandContext(context.Background(), "wl-paste", "--watch", self, "clipboard")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "clipboard watch error: %v\n", err)
		}
	}
}
