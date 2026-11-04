package cli

import (
	"reflect"
	"strings"
	"testing"
)

func TestRetentionPreviewIsADryRunOfSessionRetentionOnly(t *testing.T) {
	cfg := setConfigEnv(t)
	fake := &cleanupServer{}
	writeRunFileFor(t, cfg, fake.start(t))
	if _, errOut, err := executeCLI(t, Deps{In: strings.NewReader(""), ProcessAlive: func(int) bool { return true }}, "retention", "preview"); err != nil {
		t.Fatalf("retention preview: %v\n%s", err, errOut)
	}
	runs := fake.runRequests()
	if len(runs) != 1 || runs[0]["dryRun"] != true || !reflect.DeepEqual(runs[0]["only"], []any{"session_retention"}) {
		t.Fatalf("run requests = %v, want one session_retention dry run", runs)
	}
}

func TestRetentionRunDeclinedRemovesNothing(t *testing.T) {
	cfg := setConfigEnv(t)
	fake := &cleanupServer{}
	writeRunFileFor(t, cfg, fake.start(t))
	if _, _, err := executeCLI(t, Deps{In: strings.NewReader("no\n"), ProcessAlive: func(int) bool { return true }}, "retention", "run"); err != nil {
		t.Fatal(err)
	}
	if runs := fake.runRequests(); len(runs) != 1 || runs[0]["dryRun"] != true {
		t.Fatalf("run requests = %v, want only the preview", runs)
	}
}

func TestRetentionPatch(t *testing.T) {
	got, err := retentionPatch([]string{"enabled=on", "days=45", "dropTranscripts=false"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"enabled": true, "days": 45, "dropTranscripts": false}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("patch = %v, want %v", got, want)
	}
	for _, bad := range []string{"days=soon", "enabled=maybe", "color=red", "days"} {
		if _, err := retentionPatch([]string{bad}); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}
