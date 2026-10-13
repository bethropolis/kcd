package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

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

		targetID, err := resolveDeviceID(c, cl)
		if err != nil {
			return err
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

// signalDots renders level 0-4 as a four-position bar, clamped so bogus
// phone levels still parse; shapes are single-cell, not pictographs.
func signalDots(level int) string {
	if level < 0 {
		level = 0
	}
	if level > 4 {
		level = 4
	}
	dots := []string{"○○○○", "●○○○", "●●○○", "●●●○", "●●●●"}
	return dots[level]
}

func formatSIMStatus(networkType string, level int) string {
	if level <= 0 {
		return "No service"
	}
	label := networkType
	if label == "" || strings.EqualFold(label, "unknown") {
		label = "Cellular"
	}
	return fmt.Sprintf("%-8s %s", label, signalDots(level))
}

func formatConnectivity(body connectivity.ConnectivityBody) []string {
	if len(body.SignalStrengths) == 0 {
		return []string{"No SIM or cellular data available"}
	}
	keys := make([]string, 0, len(body.SignalStrengths))
	for k := range body.SignalStrengths {
		keys = append(keys, k)
	}
	sort.Strings(keys)
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
		netType := sig.NetworkDetailedType
		if netType == "" {
			netType = sig.NetworkType
		}
		lines = append(lines, fmt.Sprintf("SIM %s: %s", k, formatSIMStatus(netType, sig.SignalStrength)))
	}
	return lines
}
