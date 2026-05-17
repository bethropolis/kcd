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
	},
}
