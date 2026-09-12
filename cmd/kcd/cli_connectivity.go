package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/plugins/connectivity"
	"github.com/urfave/cli/v2"
)

var connectivityCmd = &cli.Command{
	Name:      "connectivity",
	Usage:     "Show cellular signal strength and network type",
	ArgsUsage: "[device-id]",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "json",
			Usage: "Output the raw report as JSON",
		},
	},
	Action: func(c *cli.Context) error {
		cl, err := getClient(c)
		if err != nil {
			return err
		}

		targetID := c.Args().First()
		if targetID == "" {
			devs, err := cl.Devices()
			if err != nil {
				return err
			}
			for _, d := range devs {
				if d.Connected && d.State == device.StatePaired {
					targetID = d.ID
					break
				}
			}
			if targetID == "" {
				return fmt.Errorf("no paired connected devices found")
			}
		}

		raw, err := cl.Connectivity(targetID)
		if err != nil {
			return err
		}
		if c.Bool("json") {
			fmt.Println(string(raw))
			return nil
		}

		var body connectivity.ConnectivityBody
		if err := json.Unmarshal(raw, &body); err != nil {
			return fmt.Errorf("decode connectivity report: %w", err)
		}
		for _, line := range formatConnectivity(body) {
			fmt.Println(line)
		}
		return nil
	},
}

// formatConnectivity renders one line per SIM, e.g. "LTE [███░] (3/4)".
// The primary SIM ("0", else lowest key) comes first; keys are sorted for
// stable output. Level is clamped to 0-4 so the bar always parses.
func formatConnectivity(body connectivity.ConnectivityBody) []string {
	if len(body.SignalStrengths) == 0 {
		return []string{"No signal data reported"}
	}
	keys := make([]string, 0, len(body.SignalStrengths))
	for k := range body.SignalStrengths {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// Primary SIM first.
	if _, ok := body.SignalStrengths["0"]; ok {
		ordered := []string{"0"}
		for _, k := range keys {
			if k != "0" {
				ordered = append(ordered, k)
			}
		}
		keys = ordered
	}

	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		sig := body.SignalStrengths[k]
		level := sig.SignalStrength
		if level < 0 {
			level = 0
		}
		if level > 4 {
			level = 4
		}
		netType := sig.NetworkDetailedType
		if netType == "" {
			netType = sig.NetworkType
		}
		if netType == "" {
			netType = "CELL"
		}
		bar := strings.Repeat("█", level) + strings.Repeat("░", 4-level)
		if len(keys) == 1 {
			lines = append(lines, fmt.Sprintf("%s [%s] (%d/4)", strings.ToUpper(netType), bar, level))
		} else {
			lines = append(lines, fmt.Sprintf("SIM %s: %s [%s] (%d/4)", k, strings.ToUpper(netType), bar, level))
		}
	}
	return lines
}
