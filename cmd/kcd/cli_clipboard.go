package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/urfave/cli/v3"
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
	Action: func(ctx context.Context, cmd *cli.Command) error {
		if cmd.Bool("watch") {
			return clipboardWatch(ctx)
		}

		cl, err := getClient(cmd)
		if err != nil {
			return err
		}

		targetID, err := resolveDeviceID(cmd, cl)
		if err != nil {
			return err
		}
		if err := cl.ClipboardPush(targetID); err != nil {
			return err
		}
		fmt.Printf("Clipboard pushed to %s\n", targetID)
		return nil
	},
}

func clipboardWatch(ctx context.Context) error {
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		return fmt.Errorf("clipboard --watch requires a Wayland session (WAYLAND_DISPLAY not set)")
	}

	self, err := os.Executable()
	if err != nil {
		self = "kcd"
	}

	fmt.Println("Watching for clipboard changes (Ctrl+C to stop)…")

	for {
		if ctx.Err() != nil {
			return nil
		}
		cmd := exec.CommandContext(ctx, "wl-paste", "--watch", self, "clipboard")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			fmt.Fprintf(os.Stderr, "clipboard watch error: %v\n", err)
			// A failing wl-paste returns instantly; without this the loop
			// pegs a core and floods stderr.
			time.Sleep(2 * time.Second)
		}
	}
}
