package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/daemon"
	"github.com/bethropolis/kcd/internal/doctor"
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
		Name:                 "kcd",
		Usage:                "KDE Connect daemon and client",
		Version:              fmt.Sprintf("%s (commit %s, built %s)", version, commit, date),
		EnableBashCompletion: true,
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
			devicesCmd,
			{

				Name:      "connect",
				Usage:     "Manually connect to a device by IP address (bypasses UDP discovery)",
				ArgsUsage: "<ip-address>",
				Action: func(c *cli.Context) error {
					if c.NArg() < 1 {
						return fmt.Errorf("missing IP address")
					}
					cl, err := getClient(c)
					if err != nil {
						return err
					}
					if err := cl.Connect(c.Args().First()); err != nil {
						return err
					}
					fmt.Println("Connection request sent to daemon.")
					return nil
				},
			},
			pairCmd,
			unpairCmd,
			pingCmd,
			batteryCmd,
			watchCmd,
			sftpCmd,
			replyCmd,
			callCmd,
			findmyphoneCmd,
			lockCmd,
			unlockCmd,
			shareCmd,
			clipboardCmd,
			runCmd,
			smsCmd,
			{

				Name:  "doctor",
				Usage: "Check runtime dependencies and configuration",
				Action: func(c *cli.Context) error {
					checks := doctor.Run()
					useColor := os.Getenv("TERM") != "" && os.Getenv("NO_COLOR") == ""
					green := ""
					red := ""
					reset := ""
					if useColor {
						green = "\033[32m"
						red = "\033[31m"
						reset = "\033[0m"
					}
					anyFailed := false
					for _, ch := range checks {
						if ch.Pass {
							fmt.Printf("%s✓%s %s\n", green, reset, ch.Name)
						} else {
							fmt.Printf("%s✗%s %s — %s\n", red, reset, ch.Name, ch.Detail)
							anyFailed = true
						}
					}
					if anyFailed {
						fmt.Println("\nSome checks failed.")
						return cli.Exit("", 1)
					}
					fmt.Println("\nAll checks passed.")
					return nil
				},
			},
			{

				Name:  "status",
				Usage: "Show daemon status and runtime information",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "json", Usage: "Output raw JSON"},
				},
				Action: func(c *cli.Context) error {
					cl, err := getClient(c)
					if err != nil {
						return err
					}
					st, err := cl.Status()
					if err != nil {
						return err
					}
					if c.Bool("json") {
						data, _ := json.MarshalIndent(st, "", "  ")
						fmt.Println(string(data))
						return nil
					}
					fmt.Printf("kcd %s — up %s\n", st.Version, st.UptimeHuman)
					fmt.Printf("Socket:    %s\n", st.SocketPath)
					fmt.Printf("Config:    %s\n", st.ConfigPath)
					fmt.Printf("Devices:   %d known, %d connected\n", st.DeviceCount, st.ConnectedCount)
					fmt.Printf("Plugins:   %s\n", strings.Join(st.Plugins, ", "))
					return nil
				},
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}

// printDeviceTable prints a human-readable device list table.

// watchDevices prints the current device list then re-prints it on every
// connect/disconnect/add/remove event.
