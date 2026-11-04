package settingsreg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAgentPolicyDocListsEveryManagedSetting fails when a lockable setting
// (or desktop-app key) is missing from the "Settings managed by your
// organization" table in docs/agent-policy.md.
func TestAgentPolicyDocListsEveryManagedSetting(t *testing.T) {
	raw, err := os.ReadFile(filepath.FromSlash("../../../docs/agent-policy.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	start := strings.Index(doc, "## Settings managed by your organization")
	if start < 0 {
		t.Fatal("docs/agent-policy.md lacks the managed settings section")
	}
	section := doc[start:]
	if end := strings.Index(section[3:], "\n## "); end >= 0 {
		section = section[:end+3]
	}
	for _, k := range Default().ManagedKeys() {
		want := "`" + k.Key + "`"
		if _, ok := cutPrefix(k.PlistKey, "settings."); !ok {
			want = "`" + k.PlistKey + "`" // desktop keys keep their top-level names
			if !strings.Contains(doc, want) {
				t.Errorf("docs/agent-policy.md does not mention %s", want)
			}
			continue
		}
		if !strings.Contains(section, want) {
			t.Errorf("managed settings table does not list %s", want)
		}
	}
}
