package agentaccount

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexPrepareWritesManagedRulesAndKeepsUserRules(t *testing.T) {
	device, home := t.TempDir(), filepath.Join(t.TempDir(), "acct")
	write(t, filepath.Join(device, "rules", "mine.rules"), "prefix_rule(pattern=[\"ls\"], decision=\"allow\")\n")
	c := &Codex{DeviceHome: device}
	for range 2 { // every launch runs it
		if err := c.Prepare(home); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(home, "rules", codexManagedRulesFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`prefix_rule(pattern=["tallpine", "report"], decision="allow")`, `prefix_rule(pattern=["tp", "session", "get"], decision="allow")`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("managed rules missing %q:\n%s", want, data)
		}
	}
	// Never a blanket rule: agent-process, browser and the like stay sandboxed.
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "prefix_rule") && (strings.Contains(line, `["tallpine"]`) || strings.Contains(line, `["tp"]`) ||
			strings.Contains(line, "agent-process") || strings.Contains(line, "browser")) {
			t.Fatalf("rule is too broad: %s", line)
		}
	}
	linksTo(t, filepath.Join(home, "rules", "mine.rules"), filepath.Join(device, "rules", "mine.rules"))
	absent(t, filepath.Join(device, "rules", codexManagedRulesFile))
}

// An earlier release linked the whole rules folder to the computer's own home;
// writing through that link would put the rule in the user's ~/.codex.
func TestCodexPrepareNeverWritesThroughALegacyRulesLink(t *testing.T) {
	device, home := t.TempDir(), filepath.Join(t.TempDir(), "acct")
	write(t, filepath.Join(device, "rules", "mine.rules"), "x\n")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(device, "rules"), filepath.Join(home, "rules")); err != nil {
		t.Fatal(err)
	}
	if err := (&Codex{DeviceHome: device}).Prepare(home); err != nil {
		t.Fatal(err)
	}
	absent(t, filepath.Join(device, "rules", codexManagedRulesFile))
	if info, err := os.Lstat(filepath.Join(home, "rules")); err != nil || !info.IsDir() {
		t.Fatalf("rules is not a real directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "rules", codexManagedRulesFile)); err != nil {
		t.Fatal(err)
	}
}

func TestCodexPrepareRulesRefusesTheComputersOwnHome(t *testing.T) {
	device := t.TempDir()
	if err := (&Codex{DeviceHome: device}).prepareCodexRules(device); err != nil {
		t.Fatal(err)
	}
	absent(t, filepath.Join(device, "rules"))
}
