package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/urfave/cli/v2"
)

var devicesCmd = &cli.Command{

	Name:  "devices",
	Usage: "List known devices",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "json",
			Usage: "Output in JSON format",
		},
		&cli.BoolFlag{
			Name:    "watch",
			Aliases: []string{"w"},
			Usage:   "Stream device changes in real time",
		},
		&cli.BoolFlag{
			Name:  "connected",
			Usage: "Only show paired devices (including offline ones)",
		},
	},
	Action: func(c *cli.Context) error {
		cl, err := getClient(c)
		if err != nil {
			return err
		}
		if c.Bool("watch") {
			return watchDevices(c, cl)
		}
		devices, err := cl.Devices()
		if err != nil {
			return err
		}
		if c.Bool("connected") {
			filtered := make([]device.DeviceInfo, 0, len(devices))
			for _, d := range devices {
				// Paired devices show even when offline — the CONNECTED
				// column carries liveness. Unpaired strangers (which may
				// briefly hold a raw TCP connection) are hidden.
				if d.State == device.StatePaired {
					filtered = append(filtered, d)
				}
			}
			devices = filtered
		}
		if c.Bool("json") {
			data, _ := json.MarshalIndent(devices, "", "  ")
			fmt.Println(string(data))
			return nil
		}
		printDeviceTable(devices)
		return nil
	},
}

var pairCmd = &cli.Command{

	Name:  "pair",
	Usage: "Initiate pairing or accept incoming requests",
	Description: `With a device ID: send a pair request to that device (or accept if they already requested).

Without a device ID: enter listen mode to receive and verify incoming pairing requests.`,
	ArgsUsage: "[device-id]",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:    "yes",
			Aliases: []string{"y"},
			Usage:   "Automatically accept incoming requests without confirmation (headless mode)",
		},
	},
	Action: func(c *cli.Context) error {
		cl, err := getClient(c)
		if err != nil {
			return err
		}

		if c.NArg() >= 1 {
			targetID := c.Args().First()
			if err := cl.Pair(targetID); err != nil {
				return err
			}
			fmt.Printf("Pair request sent / accepted for %s\n", targetID)
			return nil
		}

		// Listen mode — wait for any incoming pair request
		fmt.Println("Listening for pair requests… (Ctrl+C to cancel)")

		if err := cl.BroadcastStart(); err != nil {
			return fmt.Errorf("failed to start broadcast: %w", err)
		}

		// Stop broadcast on Ctrl+C or normal exit
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		defer func() {
			signal.Stop(sigCh)
			_ = cl.BroadcastStop()
		}()

		type listenResult struct {
			result *ipc.PairListenResult
			err    error
		}

		// Keep waiting past the daemon's 60s pair_listen timeout so a slow
		// phone-side accept doesn't force a restart. Ctrl+C cancels.
		for {
			resultCh := make(chan listenResult, 1)
			go func() {
				r, err := cl.PairListen()
				resultCh <- listenResult{r, err}
			}()

			select {
			case <-sigCh:
				fmt.Println("\nCancelled")
				return nil
			case r := <-resultCh:
				if r.err != nil {
					if strings.Contains(r.err.Error(), "timed out") {
						fmt.Println("No pair requests yet, still listening… (Ctrl+C to cancel)")
						continue
					}
					return r.err
				}

				fmt.Printf("\nIncoming pair request from:\n")
				fmt.Printf("  Device: %s (%s)\n", r.result.DeviceName, r.result.DeviceID)
				if r.result.VerificationKey != "" {
					fmt.Printf("  Verification code: %s\n", r.result.VerificationKey)
				}

				// Headless / auto-accept flag
				if c.Bool("yes") {
					if err := cl.Pair(r.result.DeviceID); err != nil {
						return fmt.Errorf("failed to accept pairing: %w", err)
					}
					fmt.Printf("Paired with %s (%s)\n", r.result.DeviceName, r.result.DeviceID)
					return nil
				}

				// Interactive prompt (default: reject)
				fmt.Print("\nAccept pairing? [y/N]: ")
				var response string
				fmt.Scanln(&response)

				response = strings.TrimSpace(strings.ToLower(response))
				if response == "y" || response == "yes" {
					if err := cl.Pair(r.result.DeviceID); err != nil {
						return fmt.Errorf("failed to accept pairing: %w", err)
					}
					fmt.Printf("Paired with %s (%s)\n", r.result.DeviceName, r.result.DeviceID)
					return nil
				}

				// User rejected: reject and cancel request
				_ = cl.Unpair(r.result.DeviceID)
				fmt.Printf("Rejected pairing with %s\n", r.result.DeviceName)
				return nil
			}
		}
	},
}

var unpairCmd = &cli.Command{

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
}

func printDeviceTable(devices []device.DeviceInfo) {
	if len(devices) == 0 {
		fmt.Println("No devices found.")
		return
	}
	fmt.Printf("%-36s %-20s %-10s %-10s %s\n", "DEVICE ID", "NAME", "TYPE", "STATE", "CONNECTED")
	fmt.Println("---------------------------------------------------------------------------------------------------")
	for _, d := range devices {
		fmt.Printf("%-36s %-20s %-10s %-10s %v\n", d.ID, d.Name, d.Type, d.State, d.Connected)
	}
}
