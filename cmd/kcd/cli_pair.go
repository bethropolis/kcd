package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/urfave/cli/v2"
)

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
		&cli.StringFlag{
			Name:  "expected-fingerprint",
			Usage: "Only accept a candidate whose TLS cert fingerprint matches (hex, colons optional)",
		},
		&cli.BoolFlag{
			Name:  "known-only",
			Usage: "Only accept candidates already recorded in the known-devices file",
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
					if err := cl.Pair(r.result.DeviceID); err != nil {
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
					if err := cl.Pair(r.result.DeviceID); err != nil {
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

// checkPairCandidate enforces the --expected-fingerprint and --known-only
// constraints on a pairing candidate. Fail-closed: a candidate without a
// reported fingerprint never satisfies an expected fingerprint.
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
