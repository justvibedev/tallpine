//go:build e2e && !windows

package cli_test

import "os/exec"

// killLeftovers SIGKILLs every process whose command line mentions dataDir.
// Detached chat-host helpers and their agents outlive a hard-killed daemon by
// design (and setsid out of its process group), so teardown finds them by the
// per-test data dir they were started with.
func killLeftovers(dataDir string) {
	_ = exec.Command("pkill", "-9", "-f", dataDir).Run()
}
