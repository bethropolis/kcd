package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/urfave/cli/v2"
)

var pingCmd = &cli.Command{

	Name:      "ping",
	Usage:     "Send a ping notification to a device",
	ArgsUsage: "<device-id>",
	Action: func(c *cli.Context) error {
		if c.NArg() < 1 {
			return fmt.Errorf("missing device ID")
		}
		cl, err := getClient(c)
		if err != nil {
			return err
		}
		if err := cl.Ping(c.Args().First()); err != nil {
			return err
		}
		fmt.Println("Ping sent")
		return nil
	},
}

var batteryCmd = &cli.Command{

	Name:      "battery",
	Usage:     "Fetch battery level and charging status",
	ArgsUsage: "<device-id>",
	Action: func(c *cli.Context) error {
		if c.NArg() < 1 {
			return fmt.Errorf("missing device ID")
		}
		cl, err := getClient(c)
		if err != nil {
			return err
		}
		charge, charging, err := cl.Battery(c.Args().First())
		if err != nil {
			return err
		}
		state := "discharging"
		if charging {
			state = "charging"
		}
		fmt.Printf("Battery: %d%% (%s)\n", charge, state)
		return nil
	},
}

var sftpCmd = &cli.Command{

	Name:  "sftp",
	Usage: "Manage SFTP connections to a device",
	Subcommands: []*cli.Command{
		{
			Name:      "request",
			Usage:     "Request SFTP connection details from the device",
			ArgsUsage: "<device-id>",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("missing device ID")
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				if err := cl.SftpMount(c.Args().First()); err != nil {
					return err
				}
				fmt.Println("SFTP mount requested. Run 'kcd watch' to see details.")
				return nil
			},
		},
		{
			Name:      "mount",
			Usage:     "Request SFTP credentials from the phone, mount via sshfs, and open in file manager",
			ArgsUsage: "<device-id>",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("missing device ID")
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				fmt.Println("Requesting SFTP credentials from phone (waiting up to 20s)…")
				path, err := cl.SftpMountLocal(c.Args().First())
				if err != nil {
					return err
				}
				fmt.Printf("Mounted at: %s\n", path)
				return nil
			},
		},
		{
			Name:      "unmount",
			Usage:     "Unmount a previously mounted phone filesystem",
			ArgsUsage: "<device-id>",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("missing device ID")
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				if err := cl.SftpUnmount(c.Args().First()); err != nil {
					return err
				}
				fmt.Println("Unmounted successfully.")
				return nil
			},
		},
	},
}

var replyCmd = &cli.Command{

	Name:      "reply",
	Usage:     "Send a text reply to a smartphone notification",
	ArgsUsage: "<device-id> <reply-id> <message>",
	Action: func(c *cli.Context) error {
		if c.NArg() < 3 {
			return fmt.Errorf("missing device ID, reply ID, or message")
		}
		cl, err := getClient(c)
		if err != nil {
			return err
		}
		if err := cl.NotifyReply(c.Args().Get(0), c.Args().Get(1), c.Args().Get(2)); err != nil {
			return err
		}
		fmt.Println("Notification reply sent")
		return nil
	},
}

var callCmd = &cli.Command{

	Name:  "call",
	Usage: "Manage phone calls",
	Subcommands: []*cli.Command{
		{
			Name:      "mute",
			Usage:     "Silence an incoming call",
			ArgsUsage: "<device-id>",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("missing device ID")
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				if err := cl.CallMute(c.Args().First()); err != nil {
					return err
				}
				fmt.Println("Call mute requested")
				return nil
			},
		},
	},
}

var findmyphoneCmd = &cli.Command{

	Name:      "findmyphone",
	Usage:     "Make the phone play a loud ringtone",
	ArgsUsage: "[device-id]",
	Action: func(c *cli.Context) error {
		cl, err := getClient(c)
		if err != nil {
			return err
		}

		targetID := c.Args().First()
		if targetID == "" {
			// Auto-select the first connected device
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

		if err := cl.FindMyPhone(targetID); err != nil {
			return err
		}
		fmt.Println("Ring request sent")
		return nil
	},
}

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

var unlockCmd = &cli.Command{

	Name:      "unlock",
	Usage:     "Unlock the current desktop session",
	ArgsUsage: "<device-id>",
	Action: func(c *cli.Context) error {
		if c.NArg() < 1 {
			return fmt.Errorf("missing device ID")
		}
		cl, err := getClient(c)
		if err != nil {
			return err
		}
		if err := cl.Unlock(c.Args().First()); err != nil {
			return err
		}
		fmt.Println("Unlock requested")
		return nil
	},
}

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
		// Resolve to absolute path before sending to the daemon — the daemon
		// runs from a different working directory so relative paths break.
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
			// Auto-find the first connected device
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

// clipboardWatch runs wl-paste --watch in the user's session and triggers
// kcd clipboard on every change. This must run in a session with Wayland access.
func clipboardWatch() error {
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		return fmt.Errorf("clipboard --watch requires a Wayland session (WAYLAND_DISPLAY not set)")
	}

	// Find our own binary path to re-invoke
	self, err := os.Executable()
	if err != nil {
		self = "kcd"
	}

	fmt.Println("Watching for clipboard changes (Ctrl+C to stop)…")

	for {
		cmd := exec.Command("wl-paste", "--watch", self, "clipboard")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "clipboard watch error: %v\n", err)
		}
	}
}

var runCmd = &cli.Command{

	Name:  "run",
	Usage: "Execute and manage remote commands on the device",
	Subcommands: []*cli.Command{
		{
			Name:      "list",
			Usage:     "List available remote commands",
			ArgsUsage: "<device-id>",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("missing device ID")
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				if err := cl.RunList(c.Args().First()); err != nil {
					return err
				}
				fmt.Println("Command list requested. Run 'kcd watch' to see results.")
				return nil
			},
		},
		{
			Name:      "exec",
			Usage:     "Execute a remote command by key",
			ArgsUsage: "<device-id> <command-key>",
			Action: func(c *cli.Context) error {
				if c.NArg() < 2 {
					return fmt.Errorf("missing device ID or command key")
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				if err := cl.RunExec(c.Args().Get(0), c.Args().Get(1)); err != nil {
					return err
				}
				fmt.Println("Command execution requested")
				return nil
			},
		},
	},
}

var smsCmd = &cli.Command{

	Name:      "sms",
	Usage:     "Send an SMS via a device",
	ArgsUsage: "<device-id> <phone-number> <message>",
	Action: func(c *cli.Context) error {
		if c.NArg() < 3 {
			return fmt.Errorf("missing device ID, phone number, or message")
		}
		cl, err := getClient(c)
		if err != nil {
			return err
		}
		if err := cl.SendSMS(c.Args().Get(0), c.Args().Get(1), c.Args().Get(2)); err != nil {
			return err
		}
		fmt.Println("SMS request sent")
		return nil
	},
}

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
