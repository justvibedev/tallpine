package cli

import (
	"strings"
	"testing"
)

const settingsValuesBody = `{"project":"app","values":[` +
	`{"key":"project.agentRules","value":"Be brief.","source":"project","locked":false,"writable":["user","orchestrator"]},` +
	`{"key":"policy.permissionCap.max","value":"auto","source":"managed","locked":true,"lockedBy":"managed","writable":[]}]}`

func TestSettingsListRendersValuesSourcesAndLocks(t *testing.T) {
	cfg := setConfigEnv(t)
	var requests []string
	writeRunFileFor(t, cfg, settingsAPIServer(t, &requests, "/api/v1/settings/values", settingsValuesBody))
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "settings", "ls", "--project", "my app")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`project.agentRules = "Be brief." (project)`,
		`policy.permissionCap.max = "auto" (managed, locked by managed)`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if requests[0] != "/api/v1/settings/values?project=my+app" {
		t.Fatalf("requests = %v", requests)
	}
}

func TestSettingsGetPrintsOneSetting(t *testing.T) {
	cfg := setConfigEnv(t)
	var requests []string
	writeRunFileFor(t, cfg, settingsAPIServer(t, &requests, "/api/v1/settings/values", settingsValuesBody))
	deps := Deps{ProcessAlive: func(int) bool { return true }}
	out, _, err := executeCLI(t, deps, "settings", "get", "project.agentRules", "--project", "app")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "\"Be brief.\"\n(project); writable by user, orchestrator") {
		t.Fatalf("get output:\n%s", out)
	}
	out, _, err = executeCLI(t, deps, "settings", "get", "policy.permissionCap.max", "--project", "app")
	if err != nil || !strings.Contains(out, "locked by managed); writable by nobody") {
		t.Fatalf("locked get: %v\n%s", err, out)
	}
	if _, _, err := executeCLI(t, deps, "settings", "get", "project.agentRules"); err == nil || !strings.Contains(err.Error(), "pass --project") {
		t.Fatalf("project key without --project: %v", err)
	}
	if _, _, err := executeCLI(t, deps, "settings", "get", "app.nope", "--project", "app"); err == nil || !strings.Contains(err.Error(), "unknown setting") {
		t.Fatalf("unknown key: %v", err)
	}
}

// An app.* key is read app-wide even when --project is given.
func TestSettingsGetAppKeyIgnoresProject(t *testing.T) {
	cfg := setConfigEnv(t)
	var requests []string
	body := `{"values":[{"key":"app.versionRefresh","value":true,"source":"default","locked":false,"writable":["user"]}]}`
	writeRunFileFor(t, cfg, settingsAPIServer(t, &requests, "/api/v1/settings/values", body))
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "settings", "get", "app.versionRefresh", "--project", "foo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "true\n(default); writable by user") || len(requests) != 1 || requests[0] != "/api/v1/settings/values" {
		t.Fatalf("out=%q requests=%v", out, requests)
	}
}
