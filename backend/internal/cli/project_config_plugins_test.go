package cli

import (
	"reflect"
	"strings"
	"testing"
)

const pluginsBase = `{"defaultBranch":"main","model":"x","claudePlugins":{"worker":{"a@m":true,"b@m":false},"reviewer":{"c@m":false}}}`

func TestSetConfigClaudePluginTogglesOnePairAndKeepsTheRest(t *testing.T) {
	got, _, err := runSetConfig(t, pluginsBase, "--claude-plugin", "worker=swift-lsp@claude-plugins-official:on", "--claude-plugin", "worker=b@m:unset", "--claude-plugin", "orchestrator=a@m:off")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"worker":       map[string]any{"a@m": true, "swift-lsp@claude-plugins-official": true},
		"reviewer":     map[string]any{"c@m": false},
		"orchestrator": map[string]any{"a@m": false},
	}
	if !reflect.DeepEqual(got["claudePlugins"], want) {
		t.Fatalf("claudePlugins = %v, want %v", got["claudePlugins"], want)
	}
	if got["defaultBranch"] != "main" || got["model"] != "x" {
		t.Fatalf("other config lost: %v", got)
	}
}

func TestSetConfigClaudePluginUnsetPrunesEmptyMaps(t *testing.T) {
	got, _, err := runSetConfig(t, `{"claudePlugins":{"worker":{"a@m":true}}}`, "--claude-plugin", "worker=a@m:unset")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["claudePlugins"]; ok {
		t.Fatalf("empty claudePlugins kept: %v", got)
	}
}

func TestSetConfigClaudePluginRejectsBadSpecs(t *testing.T) {
	for _, spec := range []string{"a@m:on", "boss=a@m:on", "worker=a@m:maybe", "worker=:on"} {
		_, _, err := runSetConfig(t, pluginsBase, "--claude-plugin", spec)
		if err == nil || !strings.Contains(err.Error(), "--claude-plugin") {
			t.Errorf("%q: err = %v", spec, err)
		}
	}
}
