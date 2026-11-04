package kiroacp

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	acpdriver "tallpine.eu/tallpine/backend/internal/adapters/chatdriver/acp"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// TestLiveKiroACPOptions starts the installed kiro-cli with the driver's launch
// arguments in a scratch workspace holding a `tallpine` custom agent, and checks
// the contract this binding depends on: the profile is offered as a `mode` value
// (the model select arrives late and is only logged). It sends no prompt, so it costs no credits, but it
// needs a logged-in kiro-cli, so it is opt-in:
//
//	TALLPINE_LIVE_KIRO=1 TALLPINE_LIVE_KIRO_HOME=$HOME go test ./internal/adapters/chatdriver/kiroacp -run Live -v
func TestLiveKiroACPOptions(t *testing.T) {
	if os.Getenv("TALLPINE_LIVE_KIRO") != "1" {
		t.Skip("set TALLPINE_LIVE_KIRO=1 to test the installed Kiro CLI")
	}
	binary, err := exec.LookPath("kiro-cli")
	if err != nil {
		t.Fatalf("resolve kiro-cli: %v", err)
	}
	workdir := t.TempDir()
	agents := filepath.Join(workdir, ".kiro", "agents")
	if err := os.MkdirAll(agents, 0o750); err != nil {
		t.Fatal(err)
	}
	profile := `{"name":"tallpine","description":"t","prompt":null,"mcpServers":{},"tools":["*"],"permissions":{"rules":[{"capability":"fs_read","match":["**"],"effect":"allow"}]}}`
	if err := os.WriteFile(filepath.Join(agents, "tallpine.json"), []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	// The test sandbox replaces HOME, which hides the kiro-cli login.
	env := map[string]string{}
	if home := os.Getenv("TALLPINE_LIVE_KIRO_HOME"); home != "" {
		env["HOME"] = home
	}
	launch, err := launchFor(binary, acpdriver.LaunchConfig{Env: env})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	options, err := acpdriver.DiscoverConfigOptions(ctx, launch, workdir,
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
	if err != nil {
		t.Fatalf("discover kiro ACP options: %v", err)
	}
	offers := func(id, value string) bool {
		for _, option := range options {
			if option.ID != id || option.Type != ports.ChatConfigOptionSelect {
				continue
			}
			for _, choice := range option.Choices {
				if choice.Value == value {
					return true
				}
			}
		}
		return false
	}
	if !offers("mode", "tallpine") {
		t.Fatalf("mode option does not offer the tallpine profile: %#v", options)
	}
	// Kiro publishes the model catalog after session/new answers, so discovery
	// may not see it; the driver waits for it (Config.OptionCatalogWait).
	t.Logf("model select offered at session/new: %v", offers("model", "auto"))
}
