package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/daemon"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/pkg/client"
	"github.com/urfave/cli/v2"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func getClient(c *cli.Context) (*client.Client, error) {
	cfgPath := c.String("config")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	return &client.Client{
		SocketPath: cfg.SocketPath,
		Timeout:    5 * time.Second,
	}, nil
}

func main() {
	app := &cli.App{
		Name:    "kcd",
		Usage:   "KDE Connect daemon and client",
		Version: fmt.Sprintf("%s (commit %s, built %s)", version, commit, date),
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "config",
				Value:   config.DefaultConfigPath(),
				Usage:   "Path to config file",
				EnvVars: []string{"KCD_CONFIG"},
			},
			&cli.StringFlag{
				Name:    "log-level",
				Usage:   "Log level (debug, info, warn, error, quiet)",
				EnvVars: []string{"KCD_LOG_LEVEL"},
			},
		},
		Commands: []*cli.Command{
			{
				Name:  "daemon",
				Usage: "Run the kcd daemon",
				Action: func(c *cli.Context) error {
					configPath := c.String("config")
					cfg, err := config.Load(configPath)
					if err != nil {
						return fmt.Errorf("failed to load config: %w", err)
					}

					if logLevel := c.String("log-level"); logLevel != "" {
						cfg.LogLevel = logLevel
					}

					if err := cfg.EnsureDeviceID(configPath); err != nil {
						return fmt.Errorf("failed to ensure device ID: %w", err)
					}

					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()

					stop := make(chan os.Signal, 1)
					signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
					go func() {
						<-stop
						cancel()
					}()

					return daemon.Run(ctx, cfg)
				},
			},
			{
				Name:  "devices",
				Usage: "List all known and reachable devices",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "json",
						Usage: "Output in JSON format",
					},
				},
				Action: func(c *cli.Context) error {
					cl, err := getClient(c)
					if err != nil {
						return err
					}
					devices, err := cl.Devices()
					if err != nil {
						return err
					}
					if c.Bool("json") {
						data, _ := json.MarshalIndent(devices, "", "  ")
						fmt.Println(string(data))
						return nil
					}
					if len(devices) == 0 {
						fmt.Println("No devices found.")
						return nil
					}
					fmt.Printf("%-36s %-20s %-10s %-10s %s\n", "DEVICE ID", "NAME", "TYPE", "STATE", "CONNECTED")
					fmt.Println("---------------------------------------------------------------------------------------------------")
					for _, d := range devices {
						fmt.Printf("%-36s %-20s %-10s %-10s %v\n", d.ID, d.Name, d.Type, d.State, d.Connected)
					}
					return nil
				},
			},
			{
				Name:      "pair",
				Usage:     "Initiate pairing with a remote device",
				ArgsUsage: "<device-id>",
				Action: func(c *cli.Context) error {
					if c.NArg() < 1 {
						return fmt.Errorf("missing device ID")
					}
					cl, err := getClient(c)
					if err != nil {
						return err
					}
					if err := cl.Pair(c.Args().First()); err != nil {
						return err
					}
					fmt.Println("Pairing request sent")
					return nil
				},
			},
			{
				Name:      "unpair",
				Usage:     "Revoke trust and unpair from a device",
				ArgsUsage: "<device-id>",
				Action: func(c *cli.Context) error {
					if c.NArg() < 1 {
						return fmt.Errorf("missing device ID")
					}
					cl, err := getClient(c)
					if err != nil {
						return err
					}
					if err := cl.Unpair(c.Args().First()); err != nil {
						return err
					}
					fmt.Println("Unpaired successfully")
					return nil
				},
			},
			{
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
			},
			{
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
			},
			{
				Name:  "watch",
				Usage: "Monitor real-time events from the daemon (notifications, battery, transfers)",
				Flags: []cli.Flag{
					&cli.StringSliceFlag{
						Name:    "events",
						Aliases: []string{"e"},
						Usage:   "Filter events by type (e.g. device.connected, battery.update)",
					},
					&cli.BoolFlag{
						Name:  "json",
						Usage: "Output raw NDJSON instead of human-readable text",
					},
				},
				Action: func(c *cli.Context) error {
					cl, err := getClient(c)
					if err != nil {
						return err
					}

					isJSON := c.Bool("json")

					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()

					stop := make(chan os.Signal, 1)
					signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
					go func() {
						<-stop
						cancel()
					}()

					ch := make(chan events.Event, 64)

					// Consumer goroutine
					go func() {
						for ev := range ch {
							if isJSON {
								b, _ := json.Marshal(ev)
								fmt.Println(string(b))
							} else {
								switch ev.Type {
								case events.TypeBatteryUpdate:
									payload, _ := ev.Payload.(map[string]interface{})
									fmt.Printf("[%s] battery: %v%% (charging: %v)\n", ev.DeviceID, payload["charge"], payload["charging"])
								case events.TypeNotification:
									payload, _ := ev.Payload.(map[string]interface{})
									fmt.Printf("[%s] notification: %s - %s\n", ev.DeviceID, payload["appName"], payload["title"])
								case events.TypeShareProgress:
									payload, _ := ev.Payload.(map[string]interface{})
									fmt.Printf("\r[%s] transfer: %s... %v/%v bytes", ev.DeviceID, payload["file"], payload["current"], payload["total"])
								case events.TypeShareComplete:
									payload, _ := ev.Payload.(map[string]interface{})
									fmt.Printf("\n[%s] transfer complete: %s\n", ev.DeviceID, payload["file"])
								case events.TypeShareText:
									payload, _ := ev.Payload.(map[string]interface{})
									fmt.Printf("[%s] share text: %s\n", ev.DeviceID, payload["text"])
								case events.TypeShareURL:
									payload, _ := ev.Payload.(map[string]interface{})
									fmt.Printf("[%s] share url: %s\n", ev.DeviceID, payload["url"])
								case events.TypeSftpMount:
									payload, _ := ev.Payload.(map[string]interface{})
									fmt.Printf("[%s] SFTP credentials received: %s\n", ev.DeviceID, payload["uri"])
								case events.TypePairRequested:
									payload, _ := ev.Payload.(map[string]interface{})
									fmt.Printf("[%s] pair request from %s (%s). code: %v\n", ev.DeviceID, payload["name"], payload["type"], payload["verificationKey"])
								case events.TypePairAccepted:
									payload, _ := ev.Payload.(map[string]interface{})
									fmt.Printf("[%s] paired with %s\n", ev.DeviceID, payload["name"])
								case events.TypePairRejected:
									payload, _ := ev.Payload.(map[string]interface{})
									fmt.Printf("[%s] pairing rejected or cancelled by %s\n", ev.DeviceID, payload["name"])
								case events.TypeNotificationCanceled:
									payload, _ := ev.Payload.(map[string]interface{})
									fmt.Printf("[%s] notification cancelled: %s\n", ev.DeviceID, payload["id"])
								default:
									fmt.Printf("[%s] %s\n", ev.DeviceID, ev.Type)
								}
							}
						}
					}()

					backoff := 1 * time.Second
					maxBackoff := 30 * time.Second

					for {
						if ctx.Err() != nil {
							return nil
						}

						start := time.Now()
						err := cl.Watch(ctx, c.StringSlice("events"), ch)

						// If err != nil, the connection failed or disconnected
						if !isJSON && err != nil {
							fmt.Fprintf(os.Stderr, "Daemon disconnected or not running: %v. Reconnecting in %v...\n", err, backoff)
						}

						if time.Since(start) > 5*time.Second {
							backoff = 1 * time.Second
						}

						select {
						case <-time.After(backoff):
							// Exponential backoff
							backoff *= 2
							if backoff > maxBackoff {
								backoff = maxBackoff
							}
						case <-ctx.Done():
							return nil
						}
					}
				},
			},
			{
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
						Usage:     "Physically mount the phone's filesystem via sshfs",
						ArgsUsage: "<device-id>",
						Action: func(c *cli.Context) error {
							if c.NArg() < 1 {
								return fmt.Errorf("missing device ID")
							}
							cl, err := getClient(c)
							if err != nil {
								return err
							}
							if err := cl.SftpMountLocal(c.Args().First()); err != nil {
								return err
							}
							return nil
						},
					},
				},
			},
			{
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
			},
			{
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
			},
			{
				Name:      "findmyphone",
				Usage:     "Make the phone play a loud ringtone",
				ArgsUsage: "<device-id>",
				Action: func(c *cli.Context) error {
					if c.NArg() < 1 {
						return fmt.Errorf("missing device ID")
					}
					cl, err := getClient(c)
					if err != nil {
						return err
					}
					if err := cl.FindMyPhone(c.Args().First()); err != nil {
						return err
					}
					fmt.Println("Ring request sent")
					return nil
				},
			},
			{
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
			},
			{
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
			},
			{
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
					if err := cl.ShareFile(c.Args().Get(0), c.Args().Get(1)); err != nil {
						return err
					}
					fmt.Println("File share requested")
					return nil
				},
			},
			{
				Name:      "clipboard",
				Usage:     "Sync local clipboard content to a device",
				ArgsUsage: "<device-id>",
				Action: func(c *cli.Context) error {
					if c.NArg() < 1 {
						return fmt.Errorf("missing device ID")
					}
					cl, err := getClient(c)
					if err != nil {
						return err
					}
					if err := cl.ClipboardPush(c.Args().First()); err != nil {
						return err
					}
					fmt.Println("Clipboard push requested")
					return nil
				},
			},
			{
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
			},

			{
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
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
