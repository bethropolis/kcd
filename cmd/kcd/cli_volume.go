package main

import (
	"encoding/json"
	"fmt"

	"github.com/bethropolis/kcd/internal/plugins/remotesystemvolume"
	"github.com/urfave/cli/v2"
)

var volumeCmd = &cli.Command{
	Name:  "volume",
	Usage: "Control remote device volume",
	Subcommands: []*cli.Command{
		{
			Name:      "list",
			Usage:     "List audio sinks on a remote device",
			ArgsUsage: "<device-id>",
			Flags: []cli.Flag{
				&cli.BoolFlag{
					Name:  "json",
					Usage: "Output raw JSON",
				},
			},
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("missing device ID")
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				data, err := cl.RemoteVolumeList(c.Args().First())
				if err != nil {
					return err
				}
				if c.Bool("json") {
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
			Action: func(c *cli.Context) error {
				if c.NArg() < 3 {
					return fmt.Errorf("usage: kcd volume set <device-id> <sink-name> <0-100>")
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				volume := c.Args().Get(2)
				var vol int
				if _, err := fmt.Sscanf(volume, "%d", &vol); err != nil || vol < 0 || vol > 100 {
					return fmt.Errorf("volume must be 0-100")
				}
				if err := cl.RemoteVolumeSet(c.Args().First(), c.Args().Get(1), vol); err != nil {
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
			Action: func(c *cli.Context) error {
				if c.NArg() < 3 {
					return fmt.Errorf("usage: kcd volume mute <device-id> <sink-name> <true|false>")
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				muted := c.Args().Get(2) == "true"
				if err := cl.RemoteVolumeMute(c.Args().First(), c.Args().Get(1), muted); err != nil {
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
