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
	"github.com/bethropolis/kcd/internal/doctor"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/pkg/client"
	"github.com/urfave/cli/v2"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func getClient(c *cli.Context) (*client.Client, error) {
	cfgPath := configPathFor(c)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	reportConfigWarnings(cfg)
	return &client.Client{
		SocketPath:        cfg.SocketPath,
		Timeout:           5 * time.Second,
		PairListenTimeout: pairListenDeadline(config.Duration(cfg.Pairing.ListenTimeout)),
		SftpTimeout:       sftpCredentialDeadline(cfg.SFTP.CredentialsTimeoutSecs),
	}, nil
}

func configPathFor(c *cli.Context) string {
	if c.IsSet("config") {
		return c.String("config")
	}
	return config.DefaultConfigPath()
}

func reportConfigWarnings(cfg *config.Config) {
	for _, warning := range cfg.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", warning)
	}
}

// sftpCredentialDeadline outlasts the daemon's wait so failures report the
// phone not answering (usually denied file access), not a socket timeout.
func sftpCredentialDeadline(seconds int) time.Duration {
	const overhead = 10 * time.Second
	const maxDuration = time.Duration(1<<63 - 1)
	if seconds <= 0 {
		seconds = 20
	}
	// Bound the seconds before converting: the config value could overflow
	// time.Duration into a negative deadline that times out instantly.
	const maxSeconds = int64(maxDuration/time.Second) - int64(overhead/time.Second)
	if int64(seconds) > maxSeconds {
		return maxDuration
	}
	return time.Duration(seconds)*time.Second + overhead
}

// pairListenDeadline adds response overhead without overflowing a duration.
func pairListenDeadline(timeout time.Duration) time.Duration {
	const maxDuration = time.Duration(1<<63 - 1)
	if timeout > maxDuration-10*time.Second {
		return maxDuration
	}
	return timeout + 10*time.Second
}

func main() {

	daemon.Version = version

	var app *cli.App
	app = &cli.App{
		Name:                 "kcd",
		Usage:                "KDE Connect daemon and client",
		Version:              fmt.Sprintf("%s (commit %s, built %s)", version, commit, date),
		EnableBashCompletion: true,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "instance",
				Usage:   "Named isolated daemon instance",
				EnvVars: []string{"KCD_INSTANCE"},
			},
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
		Before: func(c *cli.Context) error {
			if instance := c.String("instance"); instance != "" {
				return os.Setenv("KCD_INSTANCE", instance)
			}
			return nil
		},
		Commands: []*cli.Command{
			configCmd,
			dbusCmd,
			{
				Name:  "version",
				Usage: "Show daemon and IPC capability information",
				Flags: []cli.Flag{&cli.BoolFlag{Name: "json", Usage: "Output capability JSON"}},
				Action: func(c *cli.Context) error {
					if !c.Bool("json") {
						fmt.Println(app.Version)
						return nil
					}
					cfg, err := config.Load(configPathFor(c))
					if err != nil {
						return err
					}
					pluginState := map[string]bool{}
					for _, name := range []string{"Pair", "Battery", "Clipboard", "Notification", "Share", "RunCommand", "MPRIS", "Ping", "Telephony", "Connectivity", "Mousepad", "SFTP", "FindMyPhone", "LockDevice", "SystemVolume", "SMS", "Contacts", "Presenter", "FindThisDevice", "RemoteSystemVolume"} {
						pluginState[name] = cfg.PluginEnabledForDevice("", name)
					}
					commands := make([]string, 0, len(app.Commands))
					for _, command := range app.Commands {
						commands = append(commands, command.Name)
					}
					data, err := json.MarshalIndent(ipc.HelloResponse{
						APIVersion:    ipc.APIVersion,
						DaemonVersion: version,
						Commands:      commands,
						Events:        events.AllTypes(),
						OptInEvents:   []string{string(events.TypeSMSIncoming), string(events.TypeSMSAttachment)},
						Plugins:       pluginState,
						Features:      []string{"errorCodes", "keepAlive", "watchResume", "prompts", "config", "schemas"},
					}, "", "  ")
					if err != nil {
						return err
					}
					fmt.Println(string(data))
					return nil
				},
			},
			{

				Name:  "daemon",
				Usage: "Run the kcd daemon",
				Action: func(c *cli.Context) error {
					configPath := configPathFor(c)
					cfg, err := config.Load(configPath)
					if err != nil {
						return fmt.Errorf("failed to load config: %w", err)
					}
					reportConfigWarnings(cfg)

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
			connectivityCmd,
			watchCmd,
			sftpCmd,
			replyCmd,
			dismissCmd,
			callCmd,
			findmyphoneCmd,
			lockCmd,
			unlockCmd,
			shareCmd,
			clipboardCmd,
			runCmd,
			smsCmd,
			contactsCmd,
			mprisCmd,
			volumeCmd,
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
					fmt.Print(formatStatus(st))
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
