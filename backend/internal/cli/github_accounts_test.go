package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

type githubFixture struct {
	patches []apitypes.UpdateSettingsValuesRequest
	puts    int
	accts   string
}

func githubServer(t *testing.T, f *githubFixture) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/projects/app/github-accounts" || r.URL.Path == "/api/v1/github/accounts":
			_, _ = io.WriteString(w, f.accts)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/settings/values":
			var p apitypes.UpdateSettingsValuesRequest
			_ = json.NewDecoder(r.Body).Decode(&p)
			f.patches = append(f.patches, p)
			_, _ = io.WriteString(w, `{"values":[]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/app":
			_, _ = io.WriteString(w, `{"project":{"id":"app","config":{}}}`)
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/projects/app/config":
			f.puts++
			_, _ = io.WriteString(w, `{"project":{"id":"app"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

const twoAccounts = `{"accounts":[{"login":"justvibedev","canPush":"yes"},{"login":"example-org","canPush":"no"}],"repo":"acme/app","autoPick":"justvibedev"}`

func TestGitHubAccountsCommandListsLogins(t *testing.T) {
	cfg := setConfigEnv(t)
	f := &githubFixture{accts: twoAccounts}
	writeRunFileFor(t, cfg, githubServer(t, f))
	deps := Deps{ProcessAlive: func(int) bool { return true }}
	out, _, err := executeCLI(t, deps, "github", "accounts", "--project", "app")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"justvibedev  can push to acme/app", "example-org  cannot push to acme/app", "auto-pick, which chooses justvibedev"} {
		if !strings.Contains(out, want) {
			t.Fatalf("out missing %q:\n%s", want, out)
		}
	}
}

func TestSetConfigGitHubUserPinsThroughSettingsAndWarnsAboutNewSessions(t *testing.T) {
	cfg := setConfigEnv(t)
	f := &githubFixture{accts: twoAccounts}
	writeRunFileFor(t, cfg, githubServer(t, f))
	deps := Deps{ProcessAlive: func(int) bool { return true }}

	js, _, err := executeCLI(t, deps, "project", "set-config", "app", "--github-user", "example-org", "--json")
	var doc projectResult
	if err != nil || json.Unmarshal([]byte(js), &doc) != nil || doc.Project.ID != "app" || strings.Contains(js, "started from now on") {
		t.Fatalf("--json must print the project document like the config route: out=%q err=%v", js, err)
	}
	f.patches = nil
	out, _, err := executeCLI(t, deps, "project", "set-config", "app", "--github-user", "example-org")
	if err != nil || !strings.Contains(out, "started from now on") {
		t.Fatalf("pin: out=%q err=%v", out, err)
	}
	if len(f.patches) != 1 || f.patches[0].Project != "app" || f.patches[0].Set["project.githubUser"] != "example-org" || f.puts != 0 {
		t.Fatalf("pin must use the settings route only: patches=%+v puts=%d", f.patches, f.puts)
	}

	// A login typed in another case is stored as gh lists it.
	if _, _, err = executeCLI(t, deps, "project", "set-config", "app", "--github-user", "JustVibeDev"); err != nil || f.patches[1].Set["project.githubUser"] != "justvibedev" {
		t.Fatalf("case: err=%v patches=%+v", err, f.patches)
	}

	// An empty value clears the pin back to auto-pick without asking gh anything.
	out, _, err = executeCLI(t, deps, "project", "set-config", "app", "--github-user", "")
	if err != nil || !strings.Contains(out, "started from now on") || len(f.patches) != 3 || f.patches[2].Unset[0] != "project.githubUser" {
		t.Fatalf("clear: out=%q err=%v patches=%+v", out, err, f.patches)
	}

	// Mixed with another field it merges into the config like any other flag.
	if _, _, err = executeCLI(t, deps, "project", "set-config", "app", "--github-user", "justvibedev", "--session-prefix", "x"); err != nil || f.puts != 1 {
		t.Fatalf("mixed: err=%v puts=%d", err, f.puts)
	}
	// plan-in-place is a config field flag too: it must not be dropped by the settings route.
	patches := len(f.patches)
	if _, _, err = executeCLI(t, deps, "project", "set-config", "app", "--github-user", "justvibedev", "--plan-in-place"); err != nil || f.puts != 2 || len(f.patches) != patches {
		t.Fatalf("with --plan-in-place: err=%v puts=%d patches=%d (was %d)", err, f.puts, len(f.patches), patches)
	}
}

func TestSetConfigGitHubUserRejectsAccountNotSignedIn(t *testing.T) {
	cfg := setConfigEnv(t)
	f := &githubFixture{accts: twoAccounts}
	writeRunFileFor(t, cfg, githubServer(t, f))
	deps := Deps{ProcessAlive: func(int) bool { return true }}
	_, _, err := executeCLI(t, deps, "project", "set-config", "app", "--github-user", "ghost")
	if err == nil || !strings.Contains(err.Error(), "not signed in") || !strings.Contains(err.Error(), "justvibedev") {
		t.Fatalf("err = %v", err)
	}
	if len(f.patches) != 0 || f.puts != 0 {
		t.Fatalf("nothing may be written: patches=%d puts=%d", len(f.patches), f.puts)
	}
}
