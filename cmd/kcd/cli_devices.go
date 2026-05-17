package main

import (
	"encoding/json"
	"fmt"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/urfave/cli/v2"
)

var devicesCmd = &cli.Command{

	Name:  "devices",
	Usage: "List all known and reachable devices",
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
	Usage: "Initiate pairing with a remote device",
	Description: `With a device ID: send a pair request to that device.

Without a device ID: enter listen mode. Waits for any incoming pair
request and auto-accepts it. Press Ctrl+C to cancel.`,
	ArgsUsage: "[device-id]",
	Action: func(c *cli.Context) error {
		cl, err := getClient(c)
		if err != nil {
			return err
		}

		if c.NArg() >= 1 {
			if err := cl.Pair(c.Args().First()); err != nil {
				return err
			}
			fmt.Println("Pairing request sent")
			return nil
		}

		// Listen mode — wait for any incoming pair request
		fmt.Println("Listening for pair requests… (Ctrl+C to cancel)")
		result, err := cl.PairListen()
		if err != nil {
			return err
		}
		fmt.Printf("Paired with %s (%s)\n", result.DeviceName, result.DeviceID)
		if result.VerificationKey != "" {
			fmt.Printf("Verification code: %s\n", result.VerificationKey)
		}
		return nil
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
