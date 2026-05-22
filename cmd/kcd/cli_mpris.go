package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/urfave/cli/v2"
)

var actionFlags = []cli.Flag{
	&cli.StringFlag{Name: "device", Usage: "Target device ID"},
	&cli.StringFlag{Name: "player", Aliases: []string{"p"}, Usage: "Player name"},
}

func actionCmd(action string) cli.ActionFunc {
	return func(c *cli.Context) error {
		cl, err := getClient(c)
		if err != nil {
			return err
		}
		return cl.MprisAction(c.String("device"), c.String("player"), action)
	}
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
			Name:   "play",
			Usage:  "Start playback on a remote device",
			Flags:  actionFlags,
			Action: actionCmd("Play"),
		},
		{
			Name:   "pause",
			Usage:  "Pause playback on a remote device",
			Flags:  actionFlags,
			Action: actionCmd("Pause"),
		},
		{
			Name:   "toggle",
			Usage:  "Toggle play/pause on a remote device",
			Flags:  actionFlags,
			Action: actionCmd("PlayPause"),
		},
		{
			Name:  "next",
			Usage: "Skip to next track on a remote device (also starts playback)",
			Flags: actionFlags,
			Action: func(c *cli.Context) error {
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				if err := cl.MprisAction(c.String("device"), c.String("player"), "Next"); err != nil {
					return err
				}
				// Some phone MPRIS implementations stop after Next.
				// Send Play to ensure the new track starts.
				return cl.MprisAction(c.String("device"), c.String("player"), "Play")
			},
		},
		{
			Name:    "previous",
			Aliases: []string{"prev"},
			Usage:   "Go to previous track on a remote device (also starts playback)",
			Flags:   actionFlags,
			Action: func(c *cli.Context) error {
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				if err := cl.MprisAction(c.String("device"), c.String("player"), "Previous"); err != nil {
					return err
				}
				return cl.MprisAction(c.String("device"), c.String("player"), "Play")
			},
		},
		{
			Name:   "stop",
			Usage:  "Stop playback on a remote device",
			Flags:  actionFlags,
			Action: actionCmd("Stop"),
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
			Name:      "seek",
			Usage:     "Seek to position or by offset. Examples: +30s, -10s, 1m30s, 45 (seconds)",
			ArgsUsage: "<offset>",
			Flags: []cli.Flag{
				&cli.StringFlag{Name: "device", Usage: "Target device ID"},
				&cli.StringFlag{Name: "player", Aliases: []string{"p"}, Usage: "Player name"},
			},
			Action: func(c *cli.Context) error {
				if c.Args().Len() < 1 {
					return fmt.Errorf("seek: missing offset argument")
				}
				offsetStr := c.Args().First()
				seek, err := parseSeek(offsetStr)
				if err != nil {
					return fmt.Errorf("seek: %w", err)
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				return cl.MprisSeek(c.String("device"), c.String("player"), seek)
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

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12] + "…"
	}
	return id
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

// parseSeek parses a seek offset string into milliseconds.
// Supported formats: +30s, -10s, 1m30s, 45 (bare seconds).
func parseSeek(s string) (int64, error) {
	if strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") {
		isNeg := strings.HasPrefix(s, "-")
		rest := s[1:]
		d, err := parseDuration(rest)
		if err != nil {
			return 0, fmt.Errorf("invalid offset %q", s)
		}
		if isNeg {
			return -d, nil
		}
		return d, nil
	}
	d, err := parseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid offset %q", s)
	}
	return d, nil
}

func parseDuration(s string) (int64, error) {
	d, err := time.ParseDuration(s)
	if err == nil {
		return int64(d.Milliseconds()), nil
	}
	// Try bare seconds
	if secs, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(secs * 1000), nil
	}
	return 0, fmt.Errorf("cannot parse %q as duration", s)
}
