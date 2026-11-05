package cli

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestProjectSetConfigJSONKeepsClaudeFields(t *testing.T) {
	in := `{"claudePlugins":{"worker":{"a@m":false}},"claudeAutoMemory":{"worker":false},"someFutureKey":{"x":1}}`
	got, _, err := runSetConfig(t, mergeBaseConfig, "--config-json", in)
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	_ = json.Unmarshal([]byte(in), &want)
	for k, v := range want {
		if !reflect.DeepEqual(got[k], v) {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

func TestProjectSetConfigFlagMergeKeepsClaudeFields(t *testing.T) {
	stored := `{"sessionPrefix":"x","claudePlugins":{"worker":{"a@m":false}},"claudeAutoMemory":{"worker":false}}`
	got, _, err := runSetConfig(t, stored, "--session-prefix", "y")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got["claudePlugins"], map[string]any{"worker": map[string]any{"a@m": false}}) ||
		!reflect.DeepEqual(got["claudeAutoMemory"], map[string]any{"worker": false}) {
		t.Errorf("claude fields lost: %v", got)
	}
}
