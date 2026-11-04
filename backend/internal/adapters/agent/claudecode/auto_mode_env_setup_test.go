package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// B523: Claude Code 2.1.318 shows "Teach auto mode about your environment?
// 1. Yes 2. Not now 3. Don't show again" over the input of an auto-mode
// session once the global config counts 5 startups and 5 auto-mode denials,
// unless autoModeEnvSetup.dismissed is true. Every launch records that flag
// ("Don't show again") and keeps the object's other fields.
func TestPreLaunchDismissesTeachAutoModeDialog(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), ".claude.json")
	seed := `{"numStartups":40,"autoModeEnvSetup":{"denials":7},"projects":{"/work":{"hasTrustDialogAccepted":true}}}`
	if err := os.WriteFile(configPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureClaudeLaunchConfigForOS(configPath, "/work", false, "linux"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		NumStartups      int `json:"numStartups"`
		AutoModeEnvSetup struct {
			Dismissed bool `json:"dismissed"`
			Denials   int  `json:"denials"`
		} `json:"autoModeEnvSetup"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	if !root.AutoModeEnvSetup.Dismissed {
		t.Fatalf("Teach auto mode dialog not dismissed: %s", data)
	}
	if root.AutoModeEnvSetup.Denials != 7 || root.NumStartups != 40 {
		t.Fatalf("other config fields lost: %s", data)
	}

	before, _ := os.Stat(configPath)
	if err := ensureClaudeLaunchConfigForOS(configPath, "/work", false, "linux"); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.Stat(configPath); !os.SameFile(before, after) {
		t.Fatal("config rewritten although the dialog was already dismissed")
	}
}
