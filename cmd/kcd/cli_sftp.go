package main

import (
	"fmt"

	"github.com/urfave/cli/v2"
)

var sftpCmd = &cli.Command{
	Name:  "sftp",
	Usage: "Manage SFTP connections to a device",
	Subcommands: []*cli.Command{
		{
			Name:      "request",
			Usage:     "Request SFTP connection details from the device",
			ArgsUsage: "<device-id>",
			Description: `Send a kdeconnect.sftp.request to the device, asking it to start its embedded SFTP server.
The device responds with connection credentials on 'kcd watch'.`,
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("missing device ID")
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				if err := cl.SftpMount(c.Args().First()); err != nil {
					return err
				}
				fmt.Println("SFTP mount requested. Run 'kcd sftp info' or 'kcd watch' to see details.")
				return nil
			},
		},
		{
			Name:      "info",
			Usage:     "Show cached SFTP connection details for a device",
			ArgsUsage: "<device-id>",
			Description: `Display the cached SFTP server credentials (IP, port, user, volumes).
Use 'kcd sftp request' first to populate the cache.`,
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("missing device ID")
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				info, err := cl.SftpInfo(c.Args().First())
				if err != nil {
					return err
				}
				fmt.Printf("IP:       %s\n", info.IP)
				fmt.Printf("Port:     %s\n", info.Port)
				fmt.Printf("User:     %s\n", info.User)
				fmt.Printf("Password: %s\n", info.Password)
				fmt.Printf("Path:     %s\n", info.Path)
				if len(info.Volumes) > 0 {
					fmt.Println("\nStorage volumes:")
					for _, v := range info.Volumes {
						fmt.Printf("  %s  %s\n", v.Name, v.Path)
					}
				}
				return nil
			},
		},
		{
			Name:      "volumes",
			Usage:     "List available storage volumes on a device",
			ArgsUsage: "<device-id>",
			Description: `Show the browsable storage roots exposed by the device.
Uses the multiPaths/pathNames fields from the cached SFTP credentials.`,
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("missing device ID")
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				volumes, err := cl.SftpVolumes(c.Args().First())
				if err != nil {
					return err
				}
				if len(volumes) == 0 {
					fmt.Println("No volumes available. Try 'kcd sftp request' first.")
					return nil
				}
				fmt.Printf("%-20s %s\n", "NAME", "PATH")
				for _, v := range volumes {
					fmt.Printf("%-20s %s\n", v.Name, v.Path)
				}
				return nil
			},
		},
		{
			Name:      "mount",
			Usage:     "Request SFTP credentials from the phone, mount via sshfs, and open in file manager",
			ArgsUsage: "<device-id>",
			Description: `Send a request, wait for the phone to respond with credentials,
mount the filesystem via sshfs, and open it in the default file manager.
Requires sshfs to be installed.`,
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("missing device ID")
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				fmt.Println("Requesting SFTP credentials from phone (waiting up to 20s)…")
				path, err := cl.SftpMountLocal(c.Args().First())
				if err != nil {
					return err
				}
				fmt.Printf("Mounted at: %s\n", path)
				return nil
			},
		},
		{
			Name:      "unmount",
			Usage:     "Unmount a previously mounted phone filesystem",
			ArgsUsage: "<device-id>",
			Description: `Unmount the sshfs mount for a device.
Safe to call even if already unmounted (returns error in that case).`,
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("missing device ID")
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}
				if err := cl.SftpUnmount(c.Args().First()); err != nil {
					return err
				}
				fmt.Println("Unmounted successfully.")
				return nil
			},
		},
		{
			Name:      "browse",
			Usage:     "Request fresh credentials and browse or mount a storage volume",
			ArgsUsage: "<device-id> [volume-index|volume-name|volume-path]",
			Description: `Request fresh SFTP credentials from the phone and either list
available volumes or mount a specific one.

Without a volume argument, lists available volumes with their index, name, and path.

With a volume argument (index, name, or path), mounts that volume via sshfs and
opens it in the default file manager.

Examples:
  kcd sftp browse myphone
  kcd sftp browse myphone 0
  kcd sftp browse myphone "SD card"
  kcd sftp browse myphone /storage/ABCD-1234`,
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("missing device ID")
				}
				cl, err := getClient(c)
				if err != nil {
					return err
				}

				volume := ""
				if c.NArg() > 1 {
					volume = c.Args().Get(1)
				}

				fmt.Println("Requesting SFTP credentials from phone (waiting up to 20s)…")
				path, volumes, err := cl.SftpBrowse(c.Args().First(), volume)
				if err != nil {
					return err
				}

				if path != "" {
					fmt.Printf("Mounted at: %s\n", path)
					return nil
				}

				if len(volumes) == 0 {
					fmt.Println("No storage volumes reported by device.")
					return nil
				}

				fmt.Println("Available volumes:")
				for i, v := range volumes {
					fmt.Printf("  %d. %-30s %s\n", i, v.Name, v.Path)
				}
				fmt.Println("\nMount a volume: kcd sftp browse <device-id> <index|name|path>")
				return nil
			},
		},
	},
}
