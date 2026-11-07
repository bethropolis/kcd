package main

import (
	"fmt"
	"os"

	"github.com/bethropolis/kcd/internal/doctor"
	"github.com/urfave/cli/v2"
)

var doctorCmd = &cli.Command{
	Name:  "doctor",
	Usage: "Check runtime dependencies and configuration",
	Action: func(c *cli.Context) error {
		checks := doctor.Run()
		useColor := os.Getenv("TERM") != "" && os.Getenv("NO_COLOR") == ""
		green := ""
		red := ""
		reset := ""
		if useColor {
			green = "\033[32m"
			red = "\033[31m"
			reset = "\033[0m"
		}
		anyFailed := false
		for _, ch := range checks {
			if ch.Pass {
				fmt.Printf("%s✓%s %s\n", green, reset, ch.Name)
			} else {
				fmt.Printf("%s✗%s %s — %s\n", red, reset, ch.Name, ch.Detail)
				anyFailed = true
			}
		}
		if anyFailed {
			fmt.Println("\nSome checks failed.")
			return cli.Exit("", 1)
		}
		fmt.Println("\nAll checks passed.")
		return nil
	},
}
