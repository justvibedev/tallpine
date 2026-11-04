package testenv

import (
	"os"
	"testing"
)

// SkipIfFencedNoPTY skips t when the Claude security-mode fence (the gate scripts export
// TALLPINE_TEST_FENCED=1) denies opening a PTY, which it does on purpose (B463). Outside the
// fence, or when a PTY opens, the test runs and fails normally.
func SkipIfFencedNoPTY(t testing.TB) {
	t.Helper()
	if os.Getenv("TALLPINE_TEST_FENCED") == "" {
		return
	}
	f, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err == nil {
		_ = f.Close()
		return
	}
	t.Skipf("PTY denied by the Claude fence: %v", err)
}

// skipIfFenced skips a test of tmux servers and process listing, both denied by the Claude
// security-mode fence on purpose (B463); the gate scripts export TALLPINE_TEST_FENCED=1 there.
func skipIfFenced(t testing.TB) {
	t.Helper()
	if os.Getenv("TALLPINE_TEST_FENCED") != "" {
		t.Skip("tmux servers and ps are denied by the Claude fence")
	}
}
