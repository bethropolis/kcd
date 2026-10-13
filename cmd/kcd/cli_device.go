package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/pkg/client"
	"github.com/urfave/cli/v2"
)

// resolveDeviceID returns the device to act on: an explicit argument, the
// sole paired connected device, or an error naming the candidates.
func resolveDeviceID(c *cli.Context, cl *client.Client) (string, error) {
	if c.NArg() >= 1 {
		return c.Args().First(), nil
	}

	devs, err := cl.Devices()
	if err != nil {
		return "", err
	}

	var paired []string
	for _, d := range devs {
		if d.Connected && d.State == device.StatePaired {
			paired = append(paired, d.ID)
		}
	}
	switch len(paired) {
	case 0:
		return "", fmt.Errorf("no paired connected devices found — pass a device ID")
	case 1:
		return paired[0], nil
	default:
		sort.Strings(paired)
		return "", fmt.Errorf("multiple devices connected (%s) — pass a device ID", strings.Join(paired, ", "))
	}
}
