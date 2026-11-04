// Command backend is a compatibility wrapper for the Tallpine daemon.
// The user-facing CLI lives at cmd/tallpine; keep this wrapper so existing `go run .`
// development workflows continue to start the daemon while scripts migrate.
package main

import (
	"fmt"
	"os"

	"tallpine.eu/tallpine/backend/internal/daemon"
)

func main() {
	if err := daemon.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "tallpine backend daemon: "+err.Error())
		os.Exit(1)
	}
}
