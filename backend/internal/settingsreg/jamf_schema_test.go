package settingsreg

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
)

// jamfSchemaFile is the generated Jamf Custom Schema, also attached to releases.
const jamfSchemaFile = "../../../frontend/src/shared/jamf-schema.json"

// TestJamfSchemaFileIsCurrent keeps the committed schema in step with the
// registry. Regenerate with UPDATE_JAMF_SCHEMA=1 go test ./internal/settingsreg.
func TestJamfSchemaFileIsCurrent(t *testing.T) {
	want, err := Default().JamfSchema()
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("UPDATE_JAMF_SCHEMA") != "" {
		if err := os.WriteFile(filepath.FromSlash(jamfSchemaFile), want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(filepath.FromSlash(jamfSchemaFile))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("frontend/src/shared/jamf-schema.json is stale (err=%v); run UPDATE_JAMF_SCHEMA=1 go test ./internal/settingsreg", err)
	}
}

func TestJamfSchemaCoversManagedKeys(t *testing.T) {
	raw, err := Default().JamfSchema()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Title      string                    `json:"title"`
		Properties map[string]map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Title != "ee.justdev.tallpine" {
		t.Errorf("title = %q", doc.Title)
	}
	settings := doc.Properties["settings"]["properties"].(map[string]any)
	for _, k := range Default().ManagedKeys() {
		if after, ok := cutPrefix(k.PlistKey, "settings."); ok {
			if _, found := settings[after]; !found {
				t.Errorf("schema lacks settings key %s", after)
			}
		} else if _, found := doc.Properties[k.PlistKey]; !found {
			t.Errorf("schema lacks top-level key %s", k.PlistKey)
		}
	}
	if doc.Properties["schemaVersion"]["default"] != float64(agentpolicy.ManagedSchemaVersion) {
		t.Errorf("schemaVersion default = %v", doc.Properties["schemaVersion"]["default"])
	}
	if enum, _ := doc.Properties["profile"]["enum"].([]any); len(enum) != 1 || enum[0] != string(agentpolicy.ProfileHardened) {
		t.Errorf("profile enum = %v", doc.Properties["profile"]["enum"])
	}
	rules := doc.Properties["rules"]["properties"].(map[string]any)
	pc, ok := rules["permissionCap"].(map[string]any)["properties"].(map[string]any)["max"].(map[string]any)
	if !ok || pc["type"] != "string" || pc["enum"] == nil {
		t.Errorf("rules.permissionCap.max = %v", pc)
	}
}

func cutPrefix(s, p string) (string, bool) {
	if len(s) >= len(p) && s[:len(p)] == p {
		return s[len(p):], true
	}
	return s, false
}
