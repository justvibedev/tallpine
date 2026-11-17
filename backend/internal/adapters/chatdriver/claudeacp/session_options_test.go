package claudeacp

import "testing"

func TestMergePermissionsJoinsDenyLists(t *testing.T) {
	base := map[string]any{"deny": []string{"Read(//x)"}, "defaultMode": "auto"}
	role := map[string]any{"deny": []any{"Grep"}}
	got := mergePermissions(base, role).(map[string]any)
	if deny := got["deny"].([]any); len(deny) != 2 || got["defaultMode"] != "auto" {
		t.Fatalf("merged = %v", got)
	}
}
