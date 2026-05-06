package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/pkg/client"
	"github.com/urfave/cli/v2"
)

var watchCmd = &cli.Command{

	Name:  "watch",
	Usage: "Monitor real-time events from the daemon (notifications, battery, transfers)",
	Flags: []cli.Flag{
		&cli.StringSliceFlag{
			Name:    "events",
			Aliases: []string{"e"},
			Usage:   "Filter events by type (e.g. device.connected, battery.update)",
		},
		&cli.BoolFlag{
			Name:  "json",
			Usage: "Output raw NDJSON instead of human-readable text",
		},
	},
	Action: func(c *cli.Context) error {
		cl, err := getClient(c)
		if err != nil {
			return err
		}

		isJSON := c.Bool("json")

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-stop
			cancel()
		}()

		ch := make(chan events.Event, 64)

		// Consumer goroutine
		go func() {
			for ev := range ch {
				if isJSON {
					b, _ := json.Marshal(ev)
					fmt.Println(string(b))
				} else {
					switch ev.Type {
					case events.TypeBatteryUpdate:
						payload, _ := ev.Payload.(map[string]interface{})
						fmt.Printf("[%s] battery: %v%% (charging: %v)\n", ev.DeviceID, payload["charge"], payload["charging"])
					case events.TypeNotification:
						payload, _ := ev.Payload.(map[string]interface{})
						fmt.Printf("[%s] notification: %s - %s\n", ev.DeviceID, payload["appName"], payload["title"])
					case events.TypeShareProgress:
						payload, _ := ev.Payload.(map[string]interface{})
						fmt.Printf("\r[%s] transfer: %s... %v/%v bytes", ev.DeviceID, payload["file"], payload["current"], payload["total"])
					case events.TypeShareComplete:
						payload, _ := ev.Payload.(map[string]interface{})
						fmt.Printf("\n[%s] transfer complete: %s\n", ev.DeviceID, payload["file"])
					case events.TypeShareText:
						payload, _ := ev.Payload.(map[string]interface{})
						fmt.Printf("[%s] share text: %s\n", ev.DeviceID, payload["text"])
					case events.TypeShareURL:
						payload, _ := ev.Payload.(map[string]interface{})
						fmt.Printf("[%s] share url: %s\n", ev.DeviceID, payload["url"])
					case events.TypeSftpMount:
						payload, _ := ev.Payload.(map[string]interface{})
						fmt.Printf("[%s] SFTP credentials received: %s\n", ev.DeviceID, payload["uri"])
					case events.TypePairRequested:
						payload, _ := ev.Payload.(map[string]interface{})
						fmt.Printf("[%s] pair request from %s (%s). code: %v\n", ev.DeviceID, payload["name"], payload["type"], payload["verificationKey"])
					case events.TypePairAccepted:
						payload, _ := ev.Payload.(map[string]interface{})
						fmt.Printf("[%s] paired with %s\n", ev.DeviceID, payload["name"])
					case events.TypePairRejected:
						payload, _ := ev.Payload.(map[string]interface{})
						fmt.Printf("[%s] pairing rejected or cancelled by %s\n", ev.DeviceID, payload["name"])
					case events.TypeNotificationCanceled:
						payload, _ := ev.Payload.(map[string]interface{})
						fmt.Printf("[%s] notification cancelled: %s\n", ev.DeviceID, payload["id"])
					default:
						fmt.Printf("[%s] %s\n", ev.DeviceID, ev.Type)
					}
				}
			}
		}()

		backoff := 1 * time.Second
		maxBackoff := 30 * time.Second

		for {
			if ctx.Err() != nil {
				return nil
			}

			start := time.Now()
			err := cl.Watch(ctx, c.StringSlice("events"), ch)

			// If err != nil, the connection failed or disconnected
			if !isJSON && err != nil {
				fmt.Fprintf(os.Stderr, "Daemon disconnected or not running: %v. Reconnecting in %v...\n", err, backoff)
			}

			if time.Since(start) > 5*time.Second {
				backoff = 1 * time.Second
			}

			select {
			case <-time.After(backoff):
				// Exponential backoff
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			case <-ctx.Done():
				return nil
			}
		}
	},
}

func watchDevices(c *cli.Context, cl *client.Client) error {
	devs, err := cl.Devices()
	if err != nil {
		return err
	}
	printDeviceTable(devs)

	ctx, cancel := context.WithCancel(c.Context)
	defer cancel()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		cancel()
	}()

	filters := []string{"device.connected", "device.disconnected", "device.added", "device.removed"}
	ch := make(chan events.Event, 8)
	go func() {
		for range ch {
			devs, err := cl.Devices()
			if err != nil {
				continue
			}
			fmt.Print("\033[2J\033[H") // clear screen
			printDeviceTable(devs)
		}
	}()

	return cl.Watch(ctx, filters, ch)
}
