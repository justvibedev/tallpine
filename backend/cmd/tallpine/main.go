package main

import (
	"fmt"
	"os"

	"tallpine.eu/tallpine/backend/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		if msg := err.Error(); msg != "" {
			fmt.Fprintln(os.Stderr, msg)
		}
		os.Exit(cli.ExitCode(err))
	}
}
