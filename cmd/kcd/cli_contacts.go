package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/urfave/cli/v2"
)

var contactsCmd = &cli.Command{
	Name:  "contacts",
	Usage: "Sync and browse the phone address book",
	Subcommands: []*cli.Command{
		{
			Name:      "sync",
			Usage:     "Request a contacts sync from a device",
			ArgsUsage: "<device-id>",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("missing device ID")
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				if err := cl.ContactsSync(c.Args().Get(0)); err != nil {
					return err
				}
				fmt.Println("Contacts sync requested. Use `kcd watch --events contacts.updated` to see results.")
				return nil
			},
		},
		{
			Name:      "list",
			Usage:     "List cached contacts for a device",
			ArgsUsage: "<device-id>",
			Flags: []cli.Flag{
				&cli.BoolFlag{
					Name:  "json",
					Usage: "Output raw JSON",
				},
			},
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("missing device ID")
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				list, err := cl.ContactsList(c.Args().Get(0))
				if err != nil {
					return err
				}
				if len(list) == 0 {
					fmt.Println("No cached contacts. Run `kcd contacts sync <device-id>` first.")
					return nil
				}
				if c.Bool("json") {
					out, _ := json.MarshalIndent(list, "", "  ")
					fmt.Println(string(out))
					return nil
				}
				for _, ct := range list {
					phones := strings.Join(ct.Phones, ", ")
					fmt.Printf("%-30s %s\n", ct.Name, phones)
				}
				return nil
			},
		},
	},
}
