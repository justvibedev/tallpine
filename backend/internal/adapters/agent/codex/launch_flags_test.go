package codex

import (
	"context"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestGetLaunchCommandSetsAutoCompactTokenLimitFromPercent(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "codex"}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{Config: ports.AgentConfig{
		Launch: domain.AgentLaunchOptions{AutoCompactWindow: "20%", ModelContextWindow: 400_000},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// A percent is of a fixed 1M: 20% is 200k.
	if !containsSubsequence(cmd, []string{"-c", "model_auto_compact_token_limit=200000"}) {
		t.Fatalf("command %#v missing the token limit", cmd)
	}
	// 60% (600k) does not fit a 400k model: Codex keeps its own default.
	cmd, _ = plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{Config: ports.AgentConfig{
		Launch: domain.AgentLaunchOptions{AutoCompactWindow: "60%", ModelContextWindow: 400_000},
	}})
	for _, arg := range cmd {
		if strings.HasPrefix(arg, "model_auto_compact_token_limit") {
			t.Fatalf("command %#v set a limit the model cannot hold", cmd)
		}
	}
	// An unknown window must not invent a limit.
	cmd, _ = plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{Config: ports.AgentConfig{
		Launch: domain.AgentLaunchOptions{AutoCompactWindow: "80%"},
	}})
	for _, arg := range cmd {
		if strings.HasPrefix(arg, "model_auto_compact_token_limit") {
			t.Fatalf("command %#v set a limit with an unknown window", cmd)
		}
	}
}

func TestGetLaunchCommandFloorsTheTokenLimitAtTheBaseline(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "codex"}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{Config: ports.AgentConfig{
		Launch: domain.AgentLaunchOptions{AutoCompactWindow: "120k", ModelContextWindow: 400_000, AutoCompactBaseline: 66_000},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubsequence(cmd, []string{"-c", "model_auto_compact_token_limit=166000"}) {
		t.Fatalf("command %#v: want the limit floored at 166000", cmd)
	}
}
