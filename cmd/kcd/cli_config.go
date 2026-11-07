package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/bethropolis/kcd/internal/config"
	"github.com/urfave/cli/v3"
)

var configCmd = &cli.Command{
	Name:  "config",
	Usage: "Inspect and validate configuration",
	Commands: []*cli.Command{
		{
			Name:  "check",
			Usage: "Parse and validate configuration",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				cfg, err := config.Load(configPathFor(cmd))
				if err != nil {
					return err
				}
				for _, warning := range cfg.Warnings {
					fmt.Fprintf(os.Stderr, "warning: %s\n", warning)
				}
				if err := cfg.Validate(); err != nil {
					return err
				}
				fmt.Println("configuration is valid")
				return nil
			},
		},
		{
			Name:  "path",
			Usage: "Show configuration lookup paths",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				path := configPathFor(cmd)
				fmt.Println(path)
				fmt.Println(filepath.Clean(path + ".d"))
				return nil
			},
		},
		{
			Name:  "show",
			Usage: "Show the effective configuration",
			Flags: []cli.Flag{
				&cli.BoolFlag{Name: "json", Usage: "Output JSON"},
				&cli.BoolFlag{Name: "origin", Usage: "Show the primary config path"},
			},
			Action: func(ctx context.Context, cmd *cli.Command) error {
				cfg, err := config.Load(configPathFor(cmd))
				if err != nil {
					return err
				}
				if cmd.Bool("origin") {
					fmt.Fprintf(os.Stderr, "origin: %s\n", cfg.ConfigPath)
				}
				if cmd.Bool("json") {
					data, err := json.MarshalIndent(cfg, "", "  ")
					if err != nil {
						return err
					}
					fmt.Println(string(data))
					return nil
				}
				return toml.NewEncoder(os.Stdout).Encode(cfg)
			},
		},
		{
			Name:  "defaults",
			Usage: "Print effective default configuration",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				return toml.NewEncoder(os.Stdout).Encode(config.Defaults())
			},
		},
	},
}
