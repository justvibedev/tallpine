package controllers

import (
	"reflect"
	"testing"

	"tallpine.eu/tallpine/backend/internal/skillbridge"
)

func TestLSPAdviceRecommendsAndFlagsMismatches(t *testing.T) {
	plugins := []skillbridge.Plugin{
		{ID: "swift-lsp@claude-plugins-official", Name: "swift-lsp", Marketplace: skillbridge.LSPMarketplace, Installed: true, AccountEnabled: true},
		{ID: "gopls-lsp@claude-plugins-official", Name: "gopls-lsp", Marketplace: skillbridge.LSPMarketplace, Installed: true},
		{ID: "design@m", Name: "design", Installed: true, AccountEnabled: true},
	}
	detect := func(string) []skillbridge.LangCount {
		return []skillbridge.LangCount{{Language: "go", Files: 40}, {Language: "typescript", Files: 10}}
	}
	binary := func(_, name string) bool { return name == "gopls" }
	got := lspAdvice("/x", "/h", plugins, nil, []string{"claude-code"}, detect, binary, func(string, skillbridge.LSPLanguage) string { return "" })

	if len(got.Recommendations) != 2 {
		t.Fatalf("recommendations: %+v", got.Recommendations)
	}
	goRec, tsRec := got.Recommendations[0], got.Recommendations[1]
	if goRec.PluginID != "gopls-lsp@claude-plugins-official" || !goRec.PluginInstalled || !goRec.BinaryInstalled || goRec.Binary != "gopls" {
		t.Fatalf("go: %+v", goRec)
	}
	if tsRec.PluginInstalled || tsRec.BinaryInstalled || tsRec.PluginInstall != "claude plugin install typescript-lsp@claude-plugins-official" || tsRec.BinaryInstall == "" {
		t.Fatalf("ts: %+v", tsRec)
	}
	// Nothing is on by default: the go plugin is not account-enabled.
	for _, r := range goRec.Roles {
		if r.On {
			t.Fatalf("recommended plugin on by default: %+v", r)
		}
	}
	// swift-lsp is account-enabled but swift is not a main language.
	want := []ProjectLSPMismatch{{PluginID: "swift-lsp@claude-plugins-official", Language: "swift", Roles: []string{"worker", "orchestrator"}}}
	if !reflect.DeepEqual(got.Mismatches, want) {
		t.Fatalf("mismatches: %+v", got.Mismatches)
	}
	// Switched off for those roles: no mismatch left.
	off := map[string]map[string]bool{"worker": {"swift-lsp@claude-plugins-official": false}, "orchestrator": {"swift-lsp@claude-plugins-official": false}}
	if got := lspAdvice("/x", "/h", plugins, off, nil, detect, binary, func(string, skillbridge.LSPLanguage) string { return "" }); len(got.Mismatches) != 0 {
		t.Fatalf("still flagged: %+v", got.Mismatches)
	}
}
