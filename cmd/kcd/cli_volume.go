package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bethropolis/kcd/internal/plugins/remotesystemvolume"
	"github.com/urfave/cli/v3"
)

var volumeCmd = &cli.Command{
	Name:  "volume",
	Usage: "Control remote device volume",
	Commands: []*cli.Command{
		{
			Name:      "list",
			Usage:     "List audio sinks on a remote device",
			ArgsUsage: "[device-id]",
			Flags: []cli.Flag{
				&cli.BoolFlag{
					Name:  "json",
					Usage: "Output raw JSON",
				},
			},
			Action: func(ctx context.Context, cmd *cli.Command) error {
				cl, err := getClient(cmd)
				if err != nil {
					return err
				}
				deviceID, err := resolveDeviceID(cmd, cl)
				if err != nil {
					return err
				}
				data, err := cl.RemoteVolumeList(deviceID)
				if err != nil {
					return err
				}
				if cmd.Bool("json") {
					fmt.Println(string(data))
					return nil
				}
				var sinks []remotesystemvolume.SinkInfo
				if err := json.Unmarshal(data, &sinks); err != nil {
					return fmt.Errorf("decode sinks: %w", err)
				}
				if len(sinks) == 0 {
					fmt.Println("No sinks known — connect to the device first.")
					return nil
				}
				for _, s := range sinks {
					muted := ""
					if s.Muted {
						muted = " (MUTED)"
					}
					fmt.Printf("%s  %d%%%s\n", s.Name, s.Volume, muted)
				}
				return nil
			},
		},
		{
			Name:      "set",
			Usage:     "Set volume on a remote device sink",
			ArgsUsage: "<device-id> <sink-name> <0-100>",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if cmd.NArg() < 3 {
					return fmt.Errorf("usage: kcd volume set <device-id> <sink-name> <0-100>")
				}
				cl, err := getClient(cmd)
				if err != nil {
					return err
				}
				volume := cmd.Args().Get(2)
				var vol int
				if _, err := fmt.Sscanf(volume, "%d", &vol); err != nil || vol < 0 || vol > 100 {
					return fmt.Errorf("volume must be 0-100")
				}
				if err := cl.RemoteVolumeSet(cmd.Args().First(), cmd.Args().Get(1), vol); err != nil {
					return err
				}
				fmt.Println("Volume set.")
				return nil
			},
		},
		{
			Name:      "mute",
			Usage:     "Mute or unmute a remote device sink",
			ArgsUsage: "<device-id> <sink-name> <true|false>",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if cmd.NArg() < 3 {
					return fmt.Errorf("usage: kcd volume mute <device-id> <sink-name> <true|false>")
				}
				cl, err := getClient(cmd)
				if err != nil {
					return err
				}
				muted := cmd.Args().Get(2) == "true"
				if err := cl.RemoteVolumeMute(cmd.Args().First(), cmd.Args().Get(1), muted); err != nil {
					return err
				}
				state := "muted"
				if !muted {
					state = "unmuted"
				}
				fmt.Printf("Sink %s.\n", state)
				return nil
			},
		},
	},
}
