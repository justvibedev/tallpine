package store

import (
	"encoding/json"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func decodeDetail(t *testing.T, raw string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("pruned detail is not JSON: %v\n%s", err, raw)
	}
	return out
}

// The rewrite cuts only fields the timeline can mark as cut, sets each one's
// own flag, keeps every other field exact (a large integer stays an integer,
// markup is not HTML-escaped, an unflagged field is untouched), and records
// the digest of the original for native-history reconciliation.
func TestPrunedActivityDetailCutsOnlyFlaggedFields(t *testing.T) {
	long := strings.Repeat("<x>", 2000)
	raw := `{"pid":9007199254740993,"command":"a && b","rawCommand":"` + long + `","output":"` + long + `","terminalInput":"` + long + `"}`
	got, ok := prunedActivityDetail(domain.ActivityKindCommand, raw, 16)
	if !ok {
		t.Fatal("long command detail was not pruned")
	}
	if !strings.Contains(got, `"pid":9007199254740993`) || !strings.Contains(got, `"command":"a && b"`) {
		t.Fatalf("other fields changed: %s", got)
	}
	d := decodeDetail(t, got)
	if d["output"] != "<x><x><x><x><x><" || d["outputTruncated"] != true ||
		d["terminalInput"] != "<x><x><x><x><x><" || d["terminalInputTruncated"] != true {
		t.Fatalf("flagged fields = %v", d)
	}
	if d["rawCommand"] != long {
		t.Fatal("a field the timeline cannot mark as cut was shortened")
	}
	if d[domain.ActivityDetailOriginalDigestKey] != domain.ActivityDetailDigest([]byte(raw)) {
		t.Fatalf("original digest = %v", d[domain.ActivityDetailOriginalDigestKey])
	}
	if domain.ActivityDetailDigest([]byte(got)) != domain.ActivityDetailDigest([]byte(raw)) {
		t.Fatal("the pruned detail does not identify as the original")
	}
}

// A file change keeps oldText/newText whole (cutting them would make the
// fallback diff invent changes) and cuts patch only with patchTruncated.
func TestPrunedFileChangeCutsPatchWithItsFlag(t *testing.T) {
	long := strings.Repeat("+line\n", 1000)
	files, _ := json.Marshal([]map[string]any{
		{"path": "a.go", "additions": 1000, "deletions": 0, "patch": long},
		{"path": "b.go", "additions": 1, "deletions": 1, "oldText": long, "newText": long + "x"},
	})
	raw := `{"files":` + string(files) + `}`
	got, ok := prunedActivityDetail(domain.ActivityKindFileChange, raw, 64)
	if !ok {
		t.Fatal("long patch was not pruned")
	}
	entries := decodeDetail(t, got)["files"].([]any)
	first, second := entries[0].(map[string]any), entries[1].(map[string]any)
	if len(first["patch"].(string)) != 64 || first["patchTruncated"] != true {
		t.Fatalf("patch = %d bytes truncated=%v", len(first["patch"].(string)), first["patchTruncated"])
	}
	if second["oldText"] != long || second["newText"] != long+"x" || second["patchTruncated"] != nil {
		t.Fatal("oldText/newText were changed")
	}
}

// User-authored and exact rows are never rewritten: a steer (a system row
// whose text is what the user typed and whose provider input is replayed by
// RetryPrompt), any other non-output kind, and detail that is not an object.
func TestPrunedActivityDetailLeavesUserAuthoredRowsAlone(t *testing.T) {
	long := strings.Repeat("y", 5000)
	steer := `{"event":"steer","text":"` + long + `","providerInputText":"` + long + `","providerInputContent":[{"type":"image","data":"` + long + `"}]}`
	for _, tc := range []struct {
		kind domain.ActivityKind
		raw  string
	}{
		{domain.ActivityKindSystem, steer},
		// Even under a prunable kind, an event row is left alone.
		{domain.ActivityKindCommand, steer},
		{domain.ActivityKindUserInput, `{"text":"` + long + `"}`},
		{domain.ActivityKindCommand, `"` + long + `"`},
		{domain.ActivityKindCommand, `{"output":"short"}`},
	} {
		if got, ok := prunedActivityDetail(tc.kind, tc.raw, 16); ok || got != tc.raw {
			t.Fatalf("%s detail %.30q was rewritten", tc.kind, tc.raw)
		}
	}
}
