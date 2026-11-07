package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/pkg/client"
	"github.com/urfave/cli/v3"
)

// resolveDeviceID returns the device to act on: an explicit argument, the
// sole paired connected device, or an error naming the candidates.
func resolveDeviceID(cmd *cli.Command, cl *client.Client) (string, error) {
	if cmd.NArg() >= 1 {
		return cmd.Args().First(), nil
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

// deviceActionCmd generates a standardized command for actions targeting a single device.
func deviceActionCmd(name, usage, okMsg string, fn func(*client.Client, string) error) *cli.Command {
	return &cli.Command{
		Name:      name,
		Usage:     usage,
		ArgsUsage: "[device-id]",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cl, err := getClient(cmd)
			if err != nil {
				return err
			}
			deviceID, err := resolveDeviceID(cmd, cl)
			if err != nil {
				return err
			}
			if err := fn(cl, deviceID); err != nil {
				return err
			}
			if okMsg != "" {
				fmt.Println(okMsg)
			}
			return nil
		},
	}
}

var jsonFlag = &cli.BoolFlag{
	Name:  "json",
	Usage: "Output as JSON",
}

// printJSON prints the given value as indented JSON.
func printJSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}
