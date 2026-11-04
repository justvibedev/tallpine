package processenv

import (
	"slices"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
)

func TestMergeDropsWithheldDaemonVariablesAndNeverPassesTheKeyOn(t *testing.T) {
	t.Setenv("GH_TOKEN", "daemon")
	t.Setenv("B2_KEEP", "yes")
	got := Merge(map[string]string{agentlaunch.EnvUnset: "GH_TOKEN", "TALLPINE_SESSION_ID": "s-1"})
	if slices.Contains(got, "GH_TOKEN=daemon") || !slices.Contains(got, "B2_KEEP=yes") || !slices.Contains(got, "TALLPINE_SESSION_ID=s-1") {
		t.Fatalf("%v", got)
	}
	for _, e := range got {
		if strings.HasPrefix(e, agentlaunch.EnvUnset+"=") {
			t.Fatalf("the instruction key leaked: %s", e)
		}
	}
	if own := Merge(map[string]string{agentlaunch.EnvUnset: "GH_TOKEN", "GH_TOKEN": "project"}); !slices.Contains(own, "GH_TOKEN=project") {
		t.Fatalf("the session's own value must survive the withholding: %v", own)
	}
	if plain := Merge(nil); !slices.Contains(plain, "GH_TOKEN=daemon") {
		t.Fatal("no instruction: the daemon environment is inherited as before")
	}
}

func TestMergeScrubbedIsTheOverlayAloneWithoutTheControlKey(t *testing.T) {
	t.Setenv("DATABASE_URL", "daemon")
	got := Merge(map[string]string{agentlaunch.EnvScrub: "1", "PATH": "/p", "TALLPINE_SESSION_ID": "s-1"})
	if slices.Contains(got, "DATABASE_URL=daemon") {
		t.Fatalf("a scrubbed launch must not inherit the daemon environment: %v", got)
	}
	for _, e := range got {
		if strings.HasPrefix(e, agentlaunch.EnvScrub+"=") {
			t.Fatalf("the control key leaked: %s", e)
		}
	}
	if !slices.Contains(got, "PATH=/p") || !slices.Contains(got, "TALLPINE_SESSION_ID=s-1") {
		t.Fatalf("%v", got)
	}
}
