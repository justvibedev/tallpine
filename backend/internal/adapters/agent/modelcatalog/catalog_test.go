package modelcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestModelCommandUsesProjectWorkingDirectory(t *testing.T) {
	cmd := modelCommand(context.Background(), "agent", []string{"models"}, "/work/project", map[string]string{"OPENCODE_CONFIG": "/project/opencode.json"})
	if cmd.Dir != "/work/project" {
		t.Fatalf("Dir = %q, want /work/project", cmd.Dir)
	}
	if cmd.WaitDelay != commandTerminationWait {
		t.Fatalf("WaitDelay = %s, want %s", cmd.WaitDelay, commandTerminationWait)
	}
	if !environmentContains(cmd.Env, "OPENCODE_CONFIG=/project/opencode.json") {
		t.Fatalf("Env does not contain project override: %#v", cmd.Env)
	}
}

func TestNormalizeShowsConcreteNameForDefaultCatalogModel(t *testing.T) {
	got := normalize([]ports.AgentModelInfo{
		{ID: "opus", Label: "Opus (default)"},
		{ID: "sonnet", Label: "Default (recommended)"},
	})
	if len(got) != 2 || got[0].ID != "opus" || got[0].Label != "Opus" || !got[0].IsDefault ||
		got[1].ID != "sonnet" || got[1].Label != "sonnet" || !got[1].IsDefault {
		t.Fatalf("normalized models = %#v", got)
	}
}

// Verbatim shape of `cmd --list-models` from Command Code v1.74.0, including the
// banner, section headings, and the `(default)` marker.

func environmentContains(env []string, wanted string) bool {
	for _, item := range env {
		if item == wanted {
			return true
		}
	}
	return false
}

func TestCommandDiscoveryTimeoutAllowsSlowModelRegistries(t *testing.T) {
	if commandTimeout < 20*time.Second {
		t.Fatalf("commandTimeout = %s, want at least 20s", commandTimeout)
	}
}

func TestModelDiscoveryErrorExplainsTimeout(t *testing.T) {
	deadlineCtx, deadlineCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer deadlineCancel()
	err := modelDiscoveryError(deadlineCtx, "kilocode", errors.New("signal: killed"), nil)
	if !strings.Contains(err.Error(), "kilocode model discovery timed out after 20s") {
		t.Fatalf("error = %q, want clear timeout", err)
	}
}

func TestModelDiscoveryErrorSurfacesCommandOutput(t *testing.T) {
	// A live-exit failure must carry the CLI's own stderr so an "exit status 1"
	// is diagnosable; ANSI is stripped and whitespace collapsed to one line.
	err := modelDiscoveryError(context.Background(), "opencode", errors.New("exit status 1"),
		[]byte("\x1b[31mError:\x1b[0m Configuration is invalid at\n  /x/opencode.json: bad file reference\n"))
	msg := err.Error()
	if !strings.Contains(msg, "opencode model discovery: exit status 1") {
		t.Fatalf("error = %q, want wrapped exit status", err)
	}
	if !strings.Contains(msg, "Configuration is invalid at /x/opencode.json: bad file reference") {
		t.Fatalf("error = %q, want single-line command output", err)
	}
	if strings.Contains(msg, "\x1b[") {
		t.Fatalf("error = %q, want ANSI stripped", err)
	}
}

func TestModelDiscoveryErrorTailBounded(t *testing.T) {
	err := modelDiscoveryError(context.Background(), "opencode", errors.New("exit status 1"),
		[]byte(strings.Repeat("x", discoveryErrorDetailMax*3)))
	// The detail is the wrapped error plus a bounded, ellipsis-prefixed tail.
	if detail := discoveryErrorDetail([]byte(strings.Repeat("x", discoveryErrorDetailMax*3))); len([]rune(detail)) != discoveryErrorDetailMax+1 {
		t.Fatalf("detail rune length = %d, want %d", len([]rune(detail)), discoveryErrorDetailMax+1)
	}
	if !strings.Contains(err.Error(), "…") {
		t.Fatalf("error = %q, want truncation ellipsis", err)
	}
}

func TestKiroDiscoveryRunsOnlyAfterAConfirmedSignIn(t *testing.T) {
	const missingBinary = "tallpine-test-missing-kiro-cli"
	type outcome int
	const (
		// ranModelCommand: the model command ran (and failed on the missing binary).
		ranModelCommand outcome = iota
		// skippedSignedOut: a clear signed-out answer skipped discovery.
		skippedSignedOut
		// failedCheck: an inconclusive check is an ordinary, retried failure.
		failedCheck
		// canceled: the caller's context ended during the check.
		canceled
	)
	for _, tc := range []struct {
		name      string
		output    string
		err       error
		block     bool
		cancel    bool
		env       map[string]string
		want      outcome
		wantProbe bool
	}{
		{name: "signed out", output: `{"error":"Not logged in"}`, err: errors.New("exit status 1"), want: skippedSignedOut, wantProbe: true},
		{name: "signed out with clean exit", output: "You are not logged in", want: skippedSignedOut, wantProbe: true},
		{name: "unrecognized output with failed exit", output: "something went wrong", err: errors.New("exit status 2"), want: failedCheck, wantProbe: true},
		{name: "probe could not run", err: errors.New("exec: permission denied"), want: failedCheck, wantProbe: true},
		{name: "probe timed out", block: true, want: failedCheck, wantProbe: true},
		{name: "caller canceled", block: true, cancel: true, want: canceled, wantProbe: true},
		{name: "signed in", output: "Logged in with Google", want: ranModelCommand, wantProbe: true},
		{name: "unrecognized output with clean exit", output: `{"accountType":"BuilderId","email":"dev@example.com"}`, want: ranModelCommand, wantProbe: true},
		{name: "api key", env: map[string]string{"KIRO_API_KEY": "secret"}, want: ranModelCommand},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previousProbe, previousTimeout := signInProbe, signInCheckTimeout
			t.Cleanup(func() { signInProbe, signInCheckTimeout = previousProbe, previousTimeout })
			signInCheckTimeout = 20 * time.Millisecond
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probed := false
			signInProbe = func(probeCtx context.Context, binary string, args []string, _ string, _ map[string]string) ([]byte, error) {
				probed = true
				if binary != missingBinary || !reflect.DeepEqual(args, []string{"whoami", "--format", "json"}) {
					t.Fatalf("probe = %s %q, want the discovery binary running whoami --format json", binary, args)
				}
				if tc.cancel {
					cancel()
				}
				if tc.block {
					<-probeCtx.Done()
					return nil, probeCtx.Err()
				}
				return []byte(tc.output), tc.err
			}

			_, err := (Discoverer{}).Discover(ctx, ports.AgentModelDiscoveryRequest{AgentID: "kiro", Binary: missingBinary, Env: tc.env})
			if err == nil {
				t.Fatal("Discover succeeded against a missing binary")
			}
			if probed != tc.wantProbe {
				t.Fatalf("probed = %t, want %t", probed, tc.wantProbe)
			}
			skipped := errors.Is(err, ports.ErrAgentModelDiscoverySignInRequired)
			checkFailed := strings.Contains(err.Error(), "kiro sign-in check")
			switch tc.want {
			case ranModelCommand:
				if skipped || checkFailed {
					t.Fatalf("Discover error = %v, want the model command to run", err)
				}
			case skippedSignedOut:
				if !skipped {
					t.Fatalf("Discover error = %v, want a sign-in skip", err)
				}
			case failedCheck:
				if skipped || !checkFailed || errors.Is(err, context.Canceled) {
					t.Fatalf("Discover error = %v, want an ordinary sign-in check failure", err)
				}
			case canceled:
				if skipped || !errors.Is(err, context.Canceled) {
					t.Fatalf("Discover error = %v, want the cancellation", err)
				}
			}
		})
	}
}

func TestDiscoveryWithoutASignInCheckNeverProbes(t *testing.T) {
	previous := signInProbe
	t.Cleanup(func() { signInProbe = previous })
	signInProbe = func(context.Context, string, []string, string, map[string]string) ([]byte, error) {
		t.Fatal("sign-in probe ran for a harness without a sign-in check")
		return nil, nil
	}
	for agentID, spec := range commandSpecs {
		if spec.signIn != nil {
			continue
		}
		if _, err := (Discoverer{}).Discover(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: agentID, Binary: "tallpine-test-missing-binary"}); errors.Is(err, ports.ErrAgentModelDiscoverySignInRequired) {
			t.Fatalf("%s discovery was skipped for sign-in", agentID)
		}
	}
}

func TestClaudeWithoutDiscoveryDoesNotInventModels(t *testing.T) {
	request := claudeRequest(t)
	request.Binary = "/missing/claude"
	request.Env["ANTHROPIC_MODEL"] = "claude-opus-4-5-20251101"
	got, err := (Discoverer{}).Discover(context.Background(), request)
	if err == nil {
		t.Fatal("missing provider discovery was reported as success")
	}
	if len(got.Models) != 0 || !got.AllowCustom {
		t.Fatalf("catalog = %#v, want no invented choices with manual entry allowed", got)
	}
}

func TestCustomModelEntryPolicy(t *testing.T) {
	tests := []struct {
		agent         string
		wantEntryMode string
		wantSelection ports.ModelSelectionMode
	}{
		{agent: "claude-code", wantEntryMode: "direct", wantSelection: ports.ModelSelectionCatalog},
		{agent: "codex", wantEntryMode: "direct", wantSelection: ports.ModelSelectionCatalog},
		{agent: "kiro", wantEntryMode: "none", wantSelection: ports.ModelSelectionCatalog},
	}

	for _, tc := range tests {
		t.Run(tc.agent, func(t *testing.T) {
			got := Base(tc.agent)
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err := json.Unmarshal(encoded, &wire); err != nil {
				t.Fatal(err)
			}
			if wire["customModelEntry"] != tc.wantEntryMode {
				t.Fatalf("Base(%q) customModelEntry = %#v, want %q", tc.agent, wire["customModelEntry"], tc.wantEntryMode)
			}
			if got.AllowCustom != (tc.wantEntryMode == "direct") {
				t.Fatalf("Base(%q) allowCustom = %v, want %v", tc.agent, got.AllowCustom, tc.wantEntryMode == "direct")
			}
			if got.SelectionMode != tc.wantSelection {
				t.Fatalf("Base(%q) selectionMode = %q, want %q", tc.agent, got.SelectionMode, tc.wantSelection)
			}
		})
	}
}

func TestBaseDynamicCatalogsContainNoTallpineOwnedModelIDs(t *testing.T) {
	for _, agentID := range []string{"claude-code", "codex"} {
		t.Run(agentID, func(t *testing.T) {
			got := Base(agentID)
			if got.SelectionMode != ports.ModelSelectionCatalog || !got.AllowCustom || got.Source != "cli" {
				t.Fatalf("Base(%q) = %#v", agentID, got)
			}
			if len(got.Models) != 0 {
				t.Fatalf("Base(%q) models = %#v, want no Tallpine-owned model IDs", agentID, got.Models)
			}
		})
	}
}

func TestCodexDiscoveryUsesStructuredProviderCatalog(t *testing.T) {
	discoverer := Discoverer{CodexModels: func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.ChatModel, error) {
		return []ports.ChatModel{
			{ID: "gpt-current", DisplayName: "GPT Current", Default: true},
			{ID: "gpt-other", DisplayName: "GPT Other"},
		}, nil
	}}
	got, err := discoverer.Discover(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "codex", Binary: "/bin/codex"})
	if err != nil {
		t.Fatal(err)
	}
	want := []ports.AgentModelInfo{
		{ID: "gpt-current", Label: "GPT Current", IsDefault: true},
		{ID: "gpt-other", Label: "GPT Other"},
	}
	if !reflect.DeepEqual(got.Models, want) || got.Source != "cli" {
		t.Fatalf("catalog = %#v, want models %#v", got, want)
	}
}

func TestCodexDiscoveryListsNewestModelsFirst(t *testing.T) {
	discoverer := Discoverer{CodexModels: func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.ChatModel, error) {
		return []ports.ChatModel{
			{ID: "gpt-5.2", DisplayName: "GPT-5.2", Default: true},
			{ID: "legacy", DisplayName: "Legacy"},
			{ID: "sol-5", DisplayName: "Sol 5"},
			{ID: "sol-6-astra", DisplayName: "Sol 6 Astra"},
			{ID: "gpt-5.10", DisplayName: "GPT-5.10"},
			{ID: "sol-6", DisplayName: "Sol 6"},
		}, nil
	}}
	got, err := discoverer.Discover(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "codex", Binary: "/bin/codex"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, item := range got.Models {
		ids = append(ids, item.ID)
	}
	want := []string{"sol-6", "sol-6-astra", "gpt-5.10", "gpt-5.2", "sol-5", "legacy"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("model order = %v, want %v", ids, want)
	}
}

func TestParseJSONModelsFindsNestedModels(t *testing.T) {
	got, err := parseJSONModels([]byte(`{"providers":[{"id":"anthropic","models":[{"modelId":"claude-sonnet","displayName":"Claude Sonnet","isDefault":true}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("models = %#v", got)
	}
	var found bool
	for _, model := range got {
		if model.ID == "claude-sonnet" && model.Label == "Claude Sonnet" && model.IsDefault {
			found = true
		}
	}
	if !found {
		t.Fatalf("models = %#v, want nested claude-sonnet", got)
	}
}

func TestParseOMPModelsUsesSelectorAsLaunchID(t *testing.T) {
	got, err := parseJSONModels([]byte(`{"models":[{"provider":"anthropic","id":"claude-opus-5","selector":"anthropic/claude-opus-5","name":"Claude Opus 5"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []ports.AgentModelInfo{{ID: "anthropic/claude-opus-5", Label: "Claude Opus 5", Provider: "anthropic"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("models = %#v, want %#v", got, want)
	}
}

func TestParseJSONModelsUsesModelMapKeysAsSelectableIDs(t *testing.T) {
	got, err := parseJSONModels([]byte(`{
		"models": {
			"kimi-code/kimi-for-coding": {
				"provider": "managed:kimi-code",
				"model": "kimi-for-coding",
				"displayName": "K2.7 Coding"
			}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "kimi-code/kimi-for-coding" || got[0].Label != "K2.7 Coding" || got[0].Provider != "managed:kimi-code" {
		t.Fatalf("models = %#v, want provider-qualified Kimi config alias", got)
	}
}

func TestParseJSONModelsWalksGroupedModelsMaps(t *testing.T) {
	got, err := parseJSONModels([]byte(`{
		"models": {
			"available": [{"modelId": "claude-sonnet", "displayName": "Claude Sonnet"}]
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "claude-sonnet" || got[0].Label != "Claude Sonnet" {
		t.Fatalf("models = %#v, want recursively discovered claude-sonnet", got)
	}
}

func TestParseJSONModelsWalksProviderGroupsWithNestedModels(t *testing.T) {
	got, err := parseJSONModels([]byte(`{
		"models": {
			"anthropic": {
				"provider": "anthropic",
				"models": [{"modelId": "claude-sonnet", "displayName": "Claude Sonnet"}]
			}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "claude-sonnet" || got[0].Label != "Claude Sonnet" {
		t.Fatalf("models = %#v, want nested claude-sonnet without provider-group alias", got)
	}
}

func TestParseJSONModelsSupportsKiroAndDevinFields(t *testing.T) {
	got, err := parseJSONModels([]byte(`{
		"models": [{"model_name": "Auto", "model_id": "auto"}],
		"families": [{
			"slug": "claude-opus-5",
			"family_label": "Claude Opus 5",
			"variants": [{"model_uid": "claude-opus-5-high", "label": "Claude Opus 5 High"}]
		}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"auto":               true,
		"claude-opus-5":      true,
		"claude-opus-5-high": true,
	}
	for _, item := range got {
		delete(want, item.ID)
	}
	if len(want) != 0 {
		t.Fatalf("models = %#v, missing %#v", got, want)
	}
}

func writeClaudeSettings(t *testing.T, dir, model string) {
	t.Helper()
	settingsDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "{}"
	if model != "" {
		body = `{"model": "` + model + `"}`
	}
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogFingerprintTracksTheConfiguredClaudeCodeModel(t *testing.T) {
	claudeRequest(t)
	t.Setenv("ANTHROPIC_MODEL", "")
	dir := t.TempDir()
	writeClaudeSettings(t, dir, "opus")

	first := CatalogFingerprint(context.Background(), "claude-code", "", dir, nil)
	if first == "" {
		t.Fatal("fingerprint is empty for a configured model")
	}
	writeClaudeSettings(t, dir, "haiku")
	second := CatalogFingerprint(context.Background(), "claude-code", "", dir, nil)
	if second == first {
		t.Fatalf("fingerprint unchanged (%q) after the configured model changed", second)
	}
}

func TestCatalogFingerprintTracksClaudeProviderInputs(t *testing.T) {
	claudeRequest(t)
	dir := t.TempDir()
	writeClaudeSettings(t, dir, "opus")
	base := map[string]string{
		"CLAUDE_CODE_USE_BEDROCK":     "1",
		"ANTHROPIC_BASE_URL":          "https://gateway.example",
		"AWS_REGION":                  "us-east-1",
		"ANTHROPIC_VERTEX_PROJECT_ID": "project-a",
		"ANTHROPIC_FOUNDRY_RESOURCE":  "resource-a",
		"ANTHROPIC_API_KEY":           "secret-a",
	}
	first := CatalogFingerprint(context.Background(), "claude-code", "", dir, base)
	if strings.Contains(first, "secret-a") {
		t.Fatal("catalog fingerprint exposed a raw credential")
	}
	changes := map[string]string{
		"CLAUDE_CODE_USE_BEDROCK":     "",
		"CLAUDE_CODE_USE_FOUNDRY":     "1",
		"ANTHROPIC_BASE_URL":          "https://other.example",
		"AWS_REGION":                  "eu-west-1",
		"ANTHROPIC_VERTEX_PROJECT_ID": "project-b",
		"ANTHROPIC_FOUNDRY_RESOURCE":  "resource-b",
		"ANTHROPIC_API_KEY":           "secret-b",
	}
	for key, value := range changes {
		t.Run(key, func(t *testing.T) {
			changed := make(map[string]string, len(base))
			for name, current := range base {
				changed[name] = current
			}
			changed[key] = value
			if got := CatalogFingerprint(context.Background(), "claude-code", "", dir, changed); got == first {
				t.Fatalf("fingerprint unchanged after %s changed", key)
			}
		})
	}
}

func TestClaudeCatalogFingerprintIncludesProviderIdentity(t *testing.T) {
	request := claudeRequest(t)
	identity := "firstParty\x00account-a"
	discoverer := Discoverer{ClaudeFingerprint: func(context.Context, ports.AgentModelDiscoveryRequest) string {
		return identity
	}}
	first := discoverer.CatalogFingerprint(context.Background(), request)
	identity = "firstParty\x00account-b"
	if got := discoverer.CatalogFingerprint(context.Background(), request); got == first {
		t.Fatal("fingerprint unchanged after provider account identity changed")
	}
	identity = "gateway\x00account-b"
	if got := discoverer.CatalogFingerprint(context.Background(), request); got == first {
		t.Fatal("fingerprint unchanged after CLI provider changed")
	}
}

func TestCatalogFingerprintTracksClaudeProviderSettings(t *testing.T) {
	claudeRequest(t)
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	writeClaudeSettings(t, dir, "opus")
	if err := os.WriteFile(settingsPath, []byte(`{"model":"opus","env":{"ANTHROPIC_BASE_URL":"https://gateway-a.example"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	first := CatalogFingerprint(context.Background(), "claude-code", "", dir, nil)
	if err := os.WriteFile(settingsPath, []byte(`{"model":"opus","env":{"ANTHROPIC_BASE_URL":"https://gateway-b.example"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := CatalogFingerprint(context.Background(), "claude-code", "", dir, nil); got == first {
		t.Fatal("fingerprint unchanged after Claude provider settings changed")
	}
}

func TestClaudeCatalogDefaultUsesResolvedSettingsEnvironment(t *testing.T) {
	request := claudeRequest(t)
	configDir := t.TempDir()
	request.Env["CLAUDE_CONFIG_DIR"] = configDir
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(`{"model":"top-level-model","env":{"ANTHROPIC_MODEL":"kimi-k2"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTHROPIC_MODEL", "daemon-model")
	catalog, err := discoverClaudeCatalog(context.Background(), request, func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.AgentModelInfo, error) {
		return []ports.AgentModelInfo{{ID: "kimi-k2", Label: "Kimi K2"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range catalog.Models {
		if item.IsDefault {
			if item.ID != "kimi-k2" {
				t.Fatalf("default = %q, want settings env model", item.ID)
			}
			return
		}
	}
	t.Fatal("configured gateway model is not the default")
}

func TestClaudeCatalogFingerprintUsesOnlyResolvedSettings(t *testing.T) {
	request := claudeRequest(t)
	configDir := t.TempDir()
	request.Env["CLAUDE_CONFIG_DIR"] = configDir
	request.Env["ANTHROPIC_API_KEY"] = "explicit-key"
	path := filepath.Join(configDir, "settings.json")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fingerprint := func() string { return (Discoverer{}).CatalogFingerprint(context.Background(), request) }
	write(`{"env":{"ANTHROPIC_API_KEY":"shadowed-key-a","SECRET":"unrelated-a","ANTHROPIC_DEFAULT_OPUS_MODEL":"glm-4"}}`)
	first := fingerprint()
	write(`{"env":{"ANTHROPIC_API_KEY":"shadowed-key-b","SECRET":"unrelated-b","ANTHROPIC_DEFAULT_OPUS_MODEL":"glm-4"}}`)
	if got := fingerprint(); got != first {
		t.Fatal("shadowed or unapproved settings changed the fingerprint")
	}
	write(`{"env":{"ANTHROPIC_API_KEY":"shadowed-key-b","SECRET":"unrelated-b","ANTHROPIC_DEFAULT_OPUS_MODEL":"glm-5"}}`)
	if got := fingerprint(); got == first {
		t.Fatal("effective configured alias did not change the fingerprint")
	}
}

func TestCatalogFingerprintKeepsTheExecutableOnlyValueForConfiglessAgents(t *testing.T) {
	dir := t.TempDir()
	writeClaudeSettings(t, dir, "opus")
	// codex reads no configuration, so its fingerprint must stay byte-identical
	// to the executable fingerprint earlier daemons cached under.
	got := CatalogFingerprint(context.Background(), "codex", "codex", dir, nil)
	if want := BinaryVersion(context.Background(), "codex"); got != want {
		t.Fatalf("fingerprint = %q, want the executable fingerprint %q", got, want)
	}
}

func TestModelDiscoveryErrorNamesACrashedCLIsError(t *testing.T) {
	// As cursor-agent prints a missing module.
	output := "node:internal/modules/cjs/loader:1228\n  throw err;\n  ^\n\nError: Cannot find module 'tree-sitter'\n" +
		"Require stack:\n- /opt/cursor-agent/index.js\n    at Module._resolveFilename (node:internal/modules/cjs/loader:1225:15)\n" +
		"    at Module.require (node:internal/modules/cjs/loader:1311:19)\n"
	if detail := discoveryErrorDetail([]byte(output)); detail != "Error: Cannot find module 'tree-sitter'" {
		t.Fatalf("detail = %q, want the error line", detail)
	}
}

func TestDeepSeekCatalogComesFromACPOptions(t *testing.T) {
	opt := ports.ChatConfigOption{
		ID: "model", Type: ports.ChatConfigOptionSelect,
		Current: ports.ChatConfigOptionValue{Select: `["deepseek-official","deepseek-flash"]`},
		Choices: []ports.ChatConfigOptionChoice{
			{Value: `["deepseek-official","deepseek-flash"]`, Name: "DeepSeek Flash"},
			{Value: `["deepseek-official","deepseek-v4-pro"]`, Name: "DeepSeek V4 Pro"},
		},
	}
	d := Discoverer{ACPOptions: map[string]ACPOptionListFunc{
		"deepseek-harness": func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.ChatConfigOption, error) {
			return []ports.ChatConfigOption{opt}, nil
		},
	}}
	got, err := d.Discover(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "deepseek-harness"})
	if err != nil || got.Source != "acp" || len(got.Models) != 2 {
		t.Fatalf("catalog = %#v err = %v", got, err)
	}
	for _, m := range got.Models {
		if m.IsDefault != (m.ID == opt.Current.Select) {
			t.Fatalf("default flag wrong for %q", m.ID)
		}
	}

	// A failing or empty ACP session is an error, never an empty "successful" catalog.
	for name, list := range map[string]ACPOptionListFunc{
		"error": func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.ChatConfigOption, error) {
			return nil, errors.New("no workspace")
		},
		"empty": func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.ChatConfigOption, error) {
			return nil, nil
		},
	} {
		d.ACPOptions["deepseek-harness"] = list
		if _, err := d.Discover(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "deepseek-harness"}); err == nil {
			t.Fatalf("%s: discovery reported success", name)
		}
	}
}

func TestDeepSeekFingerprintTracksProfileFiles(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, "profiles", "acp")
	if err := os.MkdirAll(profile, 0o755); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"DSH_HOME": home}
	before := CatalogFingerprint(context.Background(), "deepseek-harness", "", "", env)
	if err := os.WriteFile(filepath.Join(profile, "cordis.patch.yml"), []byte("- id: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if after := CatalogFingerprint(context.Background(), "deepseek-harness", "", "", env); after == before {
		t.Fatal("route change in the profile did not invalidate the cached catalog")
	}
}
