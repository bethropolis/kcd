package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/pkg/client"
	"github.com/urfave/cli/v2"
)

var pairCmd = &cli.Command{

	Name:  "pair",
	Usage: "Initiate pairing or accept incoming requests",
	Description: `With a device ID: send a pair request to that device (or accept if they already requested).

Without a device ID: enter listen mode to receive and verify incoming pairing requests.

With --advertise-only: make this machine discoverable and wait, accepting nothing. For clients that cannot prompt.`,
	ArgsUsage: "[device-id]",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:    "yes",
			Aliases: []string{"y"},
			Usage:   "Automatically accept incoming requests without confirmation (headless mode)",
		},
		&cli.StringFlag{
			Name:  "expected-fingerprint",
			Usage: "Only accept a candidate whose TLS cert fingerprint matches (hex, colons optional)",
		},
		&cli.BoolFlag{
			Name:  "known-only",
			Usage: "Only accept candidates already recorded in the known-devices file",
		},
		&cli.BoolFlag{
			Name:  "advertise-only",
			Usage: "Make the daemon discoverable for pairing, then wait. Accepts nothing (headless mode)",
		},
		&cli.BoolFlag{
			Name:  "json",
			Usage: "With --advertise-only: print a machine-readable line when advertising starts",
		},
	},
	Action: func(c *cli.Context) error {
		cl, err := getClient(c)
		if err != nil {
			return err
		}

		if c.Bool("advertise-only") {
			if err := checkAdvertiseOnly(c); err != nil {
				return err
			}
			return advertiseOnly(c, cl)
		}

		if c.NArg() >= 1 {
			targetID := c.Args().First()
			verificationKey, err := cl.Pair(targetID)
			if err != nil {
				return err
			}
			fmt.Printf("Pair request sent / accepted for %s\n", targetID)
			if verificationKey != "" {
				fmt.Printf("Verification code: %s\n", verificationKey)
				fmt.Println("Compare it with the code shown on the device. If they differ, cancel and unpair.")
			}
			return nil
		}

		// Listen mode — wait for any incoming pair request
		fmt.Println("Listening for pair requests… (Ctrl+C to cancel)")

		if c.Bool("yes") && c.String("expected-fingerprint") == "" && !c.Bool("known-only") {
			fmt.Fprintln(os.Stderr, "WARNING: auto-accepting pairing requests from ANY device on the local network.")
			fmt.Fprintln(os.Stderr, "Use --expected-fingerprint or --known-only to restrict which device may pair.")
		}

		// Snapshot of previously seen devices for --known-only. A stranger
		// that was never recorded in the state file is never auto-accepted.
		var known map[string]bool
		if c.Bool("known-only") {
			known = loadKnownDeviceIDs()
		}

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
				fmt.Printf("  Device: %s (%s)\n", protocol.DisplayName(r.result.DeviceName), r.result.DeviceID)
				if r.result.VerificationKey != "" {
					fmt.Printf("  Verification code: %s\n", r.result.VerificationKey)
				}

				// Headless / auto-accept flag
				if c.Bool("yes") {
					if err := checkPairCandidate(c.String("expected-fingerprint"), c.Bool("known-only"), r.result, known); err != nil {
						fmt.Printf("Refusing candidate: %s\n", err)
						_ = cl.Unpair(r.result.DeviceID)
						fmt.Println("Still listening… (Ctrl+C to cancel)")
						continue
					}
					if _, err := cl.Pair(r.result.DeviceID); err != nil {
						return fmt.Errorf("failed to accept pairing: %w", err)
					}
					fmt.Printf("Paired with %s (%s)\n", protocol.DisplayName(r.result.DeviceName), r.result.DeviceID)
					return nil
				}

				// Interactive prompt (default: reject)
				fmt.Print("\nAccept pairing? [y/N]: ")
				var response string
				fmt.Scanln(&response)

				response = strings.TrimSpace(strings.ToLower(response))
				if response == "y" || response == "yes" {
					if err := checkPairCandidate(c.String("expected-fingerprint"), c.Bool("known-only"), r.result, known); err != nil {
						fmt.Printf("Refusing candidate: %s\n", err)
						_ = cl.Unpair(r.result.DeviceID)
						return nil
					}
					if _, err := cl.Pair(r.result.DeviceID); err != nil {
						return fmt.Errorf("failed to accept pairing: %w", err)
					}
					fmt.Printf("Paired with %s (%s)\n", protocol.DisplayName(r.result.DeviceName), r.result.DeviceID)
					return nil
				}

				// User rejected: reject and cancel request
				_ = cl.Unpair(r.result.DeviceID)
				fmt.Printf("Rejected pairing with %s\n", protocol.DisplayName(r.result.DeviceName))
				return nil
			}
		}
	},
}

// checkAdvertiseOnly rejects flag combinations that contradict the mode's
// one guarantee: it never accepts, so --yes and device IDs are refused.
func checkAdvertiseOnly(c *cli.Context) error {
	if c.NArg() >= 1 {
		return fmt.Errorf("pair: --advertise-only takes no device ID")
	}
	if c.Bool("yes") {
		return fmt.Errorf("pair: --advertise-only never accepts, so it cannot be combined with --yes")
	}
	return nil
}

// advertiseOnly makes the daemon discoverable, then blocks without ever
// accepting anything. It exists for clients that cannot drive a prompt.
func advertiseOnly(c *cli.Context, cl *client.Client) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	stop := make(chan struct{})
	go func() {
		<-sigCh
		close(stop)
	}()

	return advertiseUntil(c, cl, stop)
}

// advertiseUntil is advertiseOnly with the stop condition injected, so tests do
// not have to signal the test binary itself.
func advertiseUntil(c *cli.Context, cl *client.Client, stop <-chan struct{}) error {
	if err := cl.BroadcastStart(); err != nil {
		return fmt.Errorf("failed to start broadcast: %w", err)
	}
	// BroadcastStop also drops the discovery connections that never led
	// to pairing, so an interrupted run leaves no strangers behind.
	defer func() { _ = cl.BroadcastStop() }()

	// A supervising client needs to know when the daemon is actually
	// discoverable, not merely that the process launched.
	if c.Bool("json") {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"advertising": true})
	} else {
		fmt.Println("Advertising for pairing requests. Nothing will be accepted; Ctrl+C to stop.")
	}

	<-stop

	if c.Bool("json") {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"advertising": false})
	} else {
		fmt.Println("\nStopped advertising")
	}
	return nil
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

// normalizeFingerprint strips separators and lowercases a hex fingerprint so
// user-supplied values (aa:bb:.., AA BB ..) compare against daemon hex.
func normalizeFingerprint(fp string) string {
	fp = strings.ReplaceAll(fp, ":", "")
	fp = strings.ReplaceAll(fp, " ", "")
	return strings.ToLower(fp)
}

// loadKnownDeviceIDs returns the set of device IDs recorded in the daemon's
// persisted state file. Missing or unreadable state means nothing is known.
func loadKnownDeviceIDs() map[string]bool {
	known := make(map[string]bool)
	infos, err := device.LoadDevices(config.StatePath())
	if err != nil {
		return known
	}
	for _, info := range infos {
		known[info.ID] = true
	}
	return known
}

// checkPairCandidate enforces --expected-fingerprint/--known-only, failing
// closed: a candidate without a reported fingerprint never satisfies one.
func checkPairCandidate(expectedFP string, knownOnly bool, result *ipc.PairListenResult, known map[string]bool) error {
	if expectedFP != "" {
		if got := normalizeFingerprint(result.Fingerprint); got == "" || got != normalizeFingerprint(expectedFP) {
			return fmt.Errorf("candidate fingerprint does not match --expected-fingerprint")
		}
	}
	if knownOnly && !known[result.DeviceID] {
		return fmt.Errorf("candidate %s is not in the known-devices file (--known-only)", result.DeviceID)
	}
	return nil
}
