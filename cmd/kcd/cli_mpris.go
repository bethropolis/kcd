package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/bethropolis/kcd/pkg/client"
	"github.com/urfave/cli/v2"
)

var actionFlags = []cli.Flag{
	&cli.StringFlag{Name: "device", Usage: "Target device ID"},
	&cli.StringFlag{Name: "player", Aliases: []string{"p"}, Usage: "Player name"},
}

// actionDeviceID returns the device for an mpris action: positional wins,
// --device is the fallback. (volume and seek take values positionally.)
func actionDeviceID(c *cli.Context) string {
	if c.NArg() >= 1 {
		return c.Args().First()
	}
	return c.String("device")
}

func actionCmd(action string) cli.ActionFunc {
	return func(c *cli.Context) error {
		cl, err := getClient(c)
		if err != nil {
			return err
		}
		return cl.MprisAction(actionDeviceID(c), c.String("player"), action)
	}
}

// skipCmd builds next/previous, nudging with Play only when playing: some
// phones stop after a skip, but waking a paused player starts unasked audio.
func skipCmd(action string) cli.ActionFunc {
	return func(c *cli.Context) error {
		cl, err := getClient(c)
		if err != nil {
			return err
		}
		deviceID := actionDeviceID(c)
		player := c.String("player")
		wasPlaying := remotePlayerPlaying(cl, deviceID, player)

		if err := cl.MprisAction(deviceID, player, action); err != nil {
			return err
		}
		if !wasPlaying {
			return nil
		}
		return cl.MprisAction(deviceID, player, "Play")
	}
}

// remotePlayerPlaying reports whether the player is playing (empty: the
// only/first). Unanswerable queries return true to preserve the nudge.
func remotePlayerPlaying(cl *client.Client, deviceID, player string) bool {
	remote, err := cl.MprisRemote()
	if err != nil {
		return true
	}
	for _, p := range remote.Players {
		if deviceID != "" && p.DeviceID != deviceID {
			continue
		}
		if player != "" && p.Player != player {
			continue
		}
		return p.IsPlaying
	}
	return true
}

var mprisCmd = &cli.Command{
	Name:  "mpris",
	Usage: "Media player control (remote devices)",
	Subcommands: []*cli.Command{
		{
			Name:  "list",
			Usage: "List active media players on remote devices",
			Flags: []cli.Flag{
				&cli.BoolFlag{
					Name:  "json",
					Usage: "Output as JSON",
				},
			},
			Action: func(c *cli.Context) error {
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				remote, err := cl.MprisRemote()
				if err != nil {
					return fmt.Errorf("mpris: %w", err)
				}
				if c.Bool("json") {
					enc := json.NewEncoder(os.Stdout)
					enc.SetIndent("", "  ")
					return enc.Encode(remote.Players)
				}
				if len(remote.Players) == 0 {
					fmt.Println("No remote media players found")
					return nil
				}
				seen := make(map[string]struct{})
				for _, p := range remote.Players {
					if _, ok := seen[p.Player]; ok {
						continue
					}
					seen[p.Player] = struct{}{}
					state := "stopped"
					if p.IsPlaying {
						state = "playing"
					} else if p.PlaybackStatus == "Paused" {
						state = "paused"
					}
					fmt.Printf("%s (%s) - %s\n", p.Player, state, p.Title)
				}
				return nil
			},
		},
		{
			Name:  "status",
			Usage: "Show currently playing media on remote devices",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:  "device",
					Usage: "Target device ID",
				},
				&cli.BoolFlag{
					Name:  "json",
					Usage: "Output as JSON",
				},
			},
			Action: func(c *cli.Context) error {
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				remote, err := cl.MprisRemote()
				if err != nil {
					return fmt.Errorf("mpris: %w", err)
				}
				if len(remote.Players) == 0 {
					fmt.Println("No remote media players found")
					return nil
				}
				deviceFilter := c.String("device")
				isJSON := c.Bool("json")

				if isJSON {
					if deviceFilter != "" {
						var filtered []interface{}
						for _, p := range remote.Players {
							if p.DeviceID == deviceFilter {
								filtered = append(filtered, p)
							}
						}
						enc := json.NewEncoder(os.Stdout)
						enc.SetIndent("", "  ")
						return enc.Encode(filtered)
					}
					enc := json.NewEncoder(os.Stdout)
					enc.SetIndent("", "  ")
					return enc.Encode(remote.Players)
				}

				for _, p := range remote.Players {
					if deviceFilter != "" && p.DeviceID != deviceFilter {
						continue
					}
					icon := "⏹"
					if p.IsPlaying {
						icon = "▶"
					} else if p.PlaybackStatus == "Paused" {
						icon = "⏸"
					}
					fmt.Printf("[%s] %s %s - %s\n", shortID(p.DeviceID), icon, p.Player, p.Title)
					if p.Artist != "" || p.Album != "" {
						fmt.Printf("  %s", p.Artist)
						if p.Album != "" {
							fmt.Printf(" — %s", p.Album)
						}
						fmt.Println()
					}
					fmt.Printf("  %s / %s  vol:%d%%\n", formatMs(p.Pos), formatMs(p.Length), p.Volume)
					fmt.Println()
				}
				return nil
			},
		},
		{
			Name:      "play",
			ArgsUsage: "[device-id]",
			Usage:     "Start playback on a remote device",
			Flags:     actionFlags,
			Action:    actionCmd("Play"),
		},
		{
			Name:      "pause",
			ArgsUsage: "[device-id]",
			Usage:     "Pause playback on a remote device",
			Flags:     actionFlags,
			Action:    actionCmd("Pause"),
		},
		{
			Name:      "toggle",
			ArgsUsage: "[device-id]",
			Usage:     "Toggle play/pause on a remote device",
			Flags:     actionFlags,
			Action:    actionCmd("PlayPause"),
		},
		{
			Name:      "next",
			ArgsUsage: "[device-id]",
			Usage:     "Skip to next track on a remote device (resumes playback if it was playing)",
			Flags:     actionFlags,
			Action:    skipCmd("Next"),
		},
		{
			Name:      "previous",
			ArgsUsage: "[device-id]",
			Aliases:   []string{"prev"},
			Usage:     "Go to previous track on a remote device (resumes playback if it was playing)",
			Flags:     actionFlags,
			Action:    skipCmd("Previous"),
		},
		{
			Name:      "stop",
			ArgsUsage: "[device-id]",
			Usage:     "Stop playback on a remote device",
			Flags:     actionFlags,
			Action:    actionCmd("Stop"),
		},
		{
			Name:      "volume",
			Usage:     "Set player volume (0-100)",
			ArgsUsage: "<volume>",
			Flags: []cli.Flag{
				&cli.StringFlag{Name: "device", Usage: "Target device ID"},
				&cli.StringFlag{Name: "player", Aliases: []string{"p"}, Usage: "Player name"},
			},
			Action: func(c *cli.Context) error {
				if c.Args().Len() < 1 {
					return fmt.Errorf("volume: missing volume argument (0-100)")
				}
				vol, err := strconv.Atoi(c.Args().First())
				if err != nil || vol < 0 || vol > 100 {
					return fmt.Errorf("volume: must be 0-100")
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				return cl.MprisVolume(c.String("device"), c.String("player"), vol)
			},
		},
		{
			Name:  "seek",
			Usage: "Seek to position or by offset. Examples: +30s, -10s, 1m30s, 45 (seconds)",
			// So a backward seek reaches parseSeek intact rather than being
			// rejected as an unknown flag.
			SkipFlagParsing: true,
			ArgsUsage:       "<offset>",
			Flags: []cli.Flag{
				&cli.StringFlag{Name: "device", Usage: "Target device ID"},
				&cli.StringFlag{Name: "player", Aliases: []string{"p"}, Usage: "Player name"},
			},
			Action: func(c *cli.Context) error {
				opts, err := splitSeekArgs(c.Args().Slice())
				if errors.Is(err, errSeekHelp) {
					return cli.ShowSubcommandHelp(c)
				}
				if err != nil {
					return err
				}
				seek, err := parseSeek(opts.offset)
				if err != nil {
					return fmt.Errorf("seek: %w", err)
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				return cl.MprisSeek(opts.device, opts.player, seek)
			},
		},
		{
			Name:      "raw",
			Usage:     "Dump raw MPRIS debug state as JSON (local players)",
			ArgsUsage: "",
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
