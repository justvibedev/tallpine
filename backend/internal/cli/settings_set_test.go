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

const settingsPatchReply = `{"values":[{"key":"project.agentRules","value":"Be brief.","source":"project","locked":false,"writable":["user"]},
{"key":"app.versionRefresh","value":false,"source":"user","locked":false,"writable":["user"]}]}`

func settingsPatchServer(t *testing.T, bodies *[]apitypes.UpdateSettingsValuesRequest) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/v1/settings/values" {
			http.NotFound(w, r)
			return
		}
		var p apitypes.UpdateSettingsValuesRequest
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		*bodies = append(*bodies, p)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, settingsPatchReply)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// B202: set reads JSON values, falls back to strings, scopes project keys and
// never sends a project for app keys; unset sends the key to clear.
func TestSettingsSetAndUnsetSendPatch(t *testing.T) {
	cfg := setConfigEnv(t)
	var bodies []apitypes.UpdateSettingsValuesRequest
	writeRunFileFor(t, cfg, settingsPatchServer(t, &bodies))
	deps := Deps{ProcessAlive: func(int) bool { return true }}
	out, _, err := executeCLI(t, deps, "settings", "set", "project.agentRules", "Be brief.", "--project", "app")
	if err != nil {
		t.Fatal(err)
	}
	if out != "project.agentRules = \"Be brief.\" (project)\n" {
		t.Fatalf("out = %q", out)
	}
	if _, _, err := executeCLI(t, deps, "settings", "set", "app.versionRefresh", "false", "--project", "app"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCLI(t, deps, "settings", "unset", "project.agentRules", "--project", "app"); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 3 {
		t.Fatalf("requests = %+v", bodies)
	}
	if bodies[0].Project != "app" || bodies[0].Set["project.agentRules"] != "Be brief." {
		t.Errorf("project set = %+v", bodies[0])
	}
	if bodies[1].Project != "" || bodies[1].Set["app.versionRefresh"] != false {
		t.Errorf("app set = %+v", bodies[1])
	}
	if bodies[2].Project != "app" || len(bodies[2].Unset) != 1 || bodies[2].Unset[0] != "project.agentRules" || bodies[2].Set != nil {
		t.Errorf("unset = %+v", bodies[2])
	}
	_, _, err = executeCLI(t, deps, "settings", "set", "project.noCI", "true")
	if err == nil || !strings.Contains(err.Error(), "pass --project") || len(bodies) != 3 {
		t.Fatalf("project key without --project: err=%v requests=%d", err, len(bodies))
	}
}
