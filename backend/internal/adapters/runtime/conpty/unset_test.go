package conpty

import (
	"slices"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
)

func TestHostEnvironmentWithholdsDaemonVariablesButKeepsTheSessionsOwn(t *testing.T) {
	base := []string{"GH_TOKEN=daemon", "GITHUB_TOKEN=daemon", "PATH=/bin", "HOME=/h"}
	configured := map[string]string{agentlaunch.EnvUnset: "GH_TOKEN,GITHUB_TOKEN", "GH_TOKEN": "project", "TALLPINE_SESSION_ID": "s-1"}
	got := hostEnvironment(base, configured, nil)
	if slices.Contains(got, "GH_TOKEN=daemon") || slices.Contains(got, "GITHUB_TOKEN=daemon") {
		t.Fatalf("the daemon's tokens must not reach the host: %v", got)
	}
	for _, want := range []string{"PATH=/bin", "HOME=/h", "GH_TOKEN=project", "TALLPINE_SESSION_ID=s-1"} {
		if !slices.Contains(got, want) {
			t.Fatalf("missing %s in %v", want, got)
		}
	}
	for _, e := range got {
		if strings.HasPrefix(e, agentlaunch.EnvUnset+"=") {
			t.Fatalf("the instruction key must not be passed on: %v", got)
		}
	}
	if plain := hostEnvironment(base, map[string]string{"A": "1"}, nil); !slices.Contains(plain, "GH_TOKEN=daemon") {
		t.Fatalf("with no instruction the daemon environment is inherited as before: %v", plain)
	}
}

func TestHostEnvironmentScrubbedStartsFromTheSessionsOwnAndNothingElse(t *testing.T) {
	base := []string{"DATABASE_URL=daemon", "PATH=/bin", "HOME=/daemon-home"}
	configured := map[string]string{agentlaunch.EnvScrub: "1", "PATH": "/session/bin", "HOME": "/h", agentlaunch.EnvUnset: "X"}
	got := hostEnvironment(base, configured, nil)
	for _, e := range got {
		if strings.HasPrefix(e, "DATABASE_URL=") || e == "HOME=/daemon-home" || e == "PATH=/bin" || strings.HasPrefix(e, agentlaunch.EnvScrub+"=") || strings.HasPrefix(e, agentlaunch.EnvUnset+"=") {
			t.Fatalf("a scrubbed host must not inherit the daemon environment or pass the control keys on: %v", got)
		}
	}
	for _, want := range []string{"PATH=/session/bin", "HOME=/h"} {
		if !slices.Contains(got, want) {
			t.Fatalf("missing %s in %v", want, got)
		}
	}
}
