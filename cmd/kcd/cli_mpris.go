package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/urfave/cli/v2"
)

var mprisCmd = &cli.Command{
	Name:  "mpris",
	Usage: "MPRIS media player debug tools",
	Subcommands: []*cli.Command{
		{
			Name:  "status",
			Usage: "Show MPRIS plugin debug state",
			Action: func(c *cli.Context) error {
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				status, err := cl.MprisStatus()
				if err != nil {
					return err
				}

				fmt.Println("MPRIS Debug Status")
				fmt.Println("==================")
				fmt.Printf("Watcher running: %v\n", status.WatcherRunning)
				fmt.Printf("Connected devices: %d\n", status.DeviceCount)
				fmt.Println()

				if len(status.Players) == 0 {
					fmt.Println("No MPRIS players found on D-Bus")
				} else {
					fmt.Println("Players:")
					for _, p := range status.Players {
						fmt.Printf("  [%s]\n", p.DisplayName)
						fmt.Printf("    Bus:     %s\n", p.BusName)
						fmt.Printf("    Short:   %s\n", p.ShortName)
						if p.Error != "" {
							fmt.Printf("    Error:   %s\n", p.Error)
						} else {
							fmt.Printf("    Title:   %q\n", p.Title)
							fmt.Printf("    Artist:  %q\n", p.Artist)
							fmt.Printf("    Album:   %q\n", p.Album)
							fmt.Printf("    Status:  %s", p.PlaybackStatus)
							if p.IsPlaying {
								fmt.Print(" (playing)")
							}
							fmt.Println()
							fmt.Printf("    Pos:     %s / %s\n", formatMs(p.Pos), formatMs(p.Length))
							fmt.Printf("    Volume:  %d%%\n", p.Volume)
							fmt.Printf("    Art:     %s\n", p.AlbumArtUrl)
						}
						fmt.Println()
					}
				}

				if len(status.PlayerMappings) > 0 {
					fmt.Println("Player Mappings:")
					for name, bus := range status.PlayerMappings {
						fmt.Printf("  %-25s → %s\n", name, bus)
					}
					fmt.Println()
				}

				return nil
			},
		},
		{
			Name:  "list",
			Usage: "List active MPRIS players",
			Action: func(c *cli.Context) error {
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				status, err := cl.MprisStatus()
				if err != nil {
					return err
				}
				if len(status.Players) == 0 {
					fmt.Println("No MPRIS players found")
					return nil
				}
				for _, p := range status.Players {
					state := "stopped"
					if p.IsPlaying {
						state = "playing"
					} else if p.PlaybackStatus == "Paused" {
						state = "paused"
					}
					fmt.Printf("%s (%s) - %s\n", p.DisplayName, state, p.Title)
				}
				return nil
			},
		},
		{
			Name:      "raw",
			Usage:     "Dump raw MPRIS plugin state as JSON",
			ArgsUsage: "[device-id]",
			Action: func(c *cli.Context) error {
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				status, err := cl.MprisStatus()
				if err != nil {
					return err
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(status)
			},
		},
	},
}

func formatMs(ms int64) string {
	if ms < 0 {
		return "??:??"
	}
	totalSec := ms / 1000
	min := totalSec / 60
	sec := totalSec % 60
	return fmt.Sprintf("%d:%02d", min, sec)
}
