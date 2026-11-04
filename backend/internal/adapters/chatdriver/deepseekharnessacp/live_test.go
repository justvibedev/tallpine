package deepseekharnessacp

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"testing"
	"time"

	acpdriver "tallpine.eu/tallpine/backend/internal/adapters/chatdriver/acp"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// TestLiveDeepSeekACPModelCatalog drives the installed dsh through Tallpine's own ACP
// client with the same arguments the driver launches, and checks the contract
// this binding depends on: a model select with opaque JSON-array values and a
// reasoning_effort select offering off/low/high/max. It needs a working model
// route (a DeepSeek API key in dsh's credential store or DEEPSEEK_API_KEY), so
// it is opt-in:
//
//	TALLPINE_LIVE_DEEPSEEK=1 go test ./internal/adapters/chatdriver/deepseekharnessacp -run Live -v
func TestLiveDeepSeekACPModelCatalog(t *testing.T) {
	if os.Getenv("TALLPINE_LIVE_DEEPSEEK") != "1" {
		t.Skip("set TALLPINE_LIVE_DEEPSEEK=1 to test the installed DeepSeek Harness")
	}
	binary, err := exec.LookPath("dsh")
	if err != nil {
		t.Fatalf("resolve dsh: %v", err)
	}
	dl, err := DiscoveryLaunch(binary, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workdir, _ := os.Getwd()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	options, err := acpdriver.DiscoverConfigOptions(ctx, acpdriver.Launch{
		Command: dl.Command, Args: dl.Args, Env: dl.Env,
	}, workdir, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
	if err != nil {
		t.Fatalf("discover dsh ACP options: %v", err)
	}
	find := func(id string) *ports.ChatConfigOption {
		for i := range options {
			if options[i].ID == id {
				return &options[i]
			}
		}
		return nil
	}
	model := find("model")
	if model == nil || model.Type != ports.ChatConfigOptionSelect || len(model.Choices) == 0 {
		t.Fatalf("model option = %#v", model)
	}
	for _, c := range model.Choices {
		if len(c.Value) == 0 || c.Value[0] != '[' {
			t.Fatalf("model choice %q is not a JSON array value", c.Value)
		}
	}
	for _, c := range model.Choices {
		t.Logf("model choice %s (%s), current=%v", c.Value, c.Name, c.Value == model.Current.Select)
	}
	effort := find("reasoning_effort")
	if effort == nil {
		t.Fatalf("no reasoning_effort option: %#v", options)
	}
	offered := map[string]bool{}
	for _, c := range effort.Choices {
		offered[c.Value] = true
	}
	t.Logf("reasoning_effort choices: %v", effort.Choices)
	for _, want := range []string{"off", "low", "high", "max"} {
		if !offered[want] {
			t.Fatalf("reasoning_effort lacks %q: %#v", want, effort.Choices)
		}
	}
}
