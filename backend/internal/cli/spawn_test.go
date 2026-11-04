package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func authorizedAgentsJSON(agent string) string {
	return readinessAgentsJSON(agent, "installed", "authorized")
}

func readinessAgentsJSON(agent, installation, authentication string) string {
	return `{"agents":[{"id":` + jsonQuote(agent) + `,"label":` + jsonQuote(agent) +
		`,"installation":{"state":` + jsonQuote(installation) + `,"freshness":"fresh","reasonCode":"test","reason":"test"}` +
		`,"authentication":{"state":` + jsonQuote(authentication) + `,"freshness":"fresh","reasonCode":"test","reason":"test"}` +
		`,"effectiveReadiness":"unknown","usageCount":0}]}`
}

func TestSpawnHelpListsOnlySupportedHarnesses(t *testing.T) {
	out, errOut, err := executeCLI(t, Deps{}, "spawn", "--help")
	if err != nil {
		t.Fatalf("spawn --help: %v\nstderr: %s", err, errOut)
	}
	if !strings.Contains(out, "claude-code, codex, kiro") {
		t.Fatalf("spawn help does not list the supported harnesses:\n%s", out)
	}
	if strings.Contains(out, "opencode") {
		t.Fatalf("spawn help still lists a removed harness:\n%s", out)
	}
}

// TestSpawnCommand_MissingProjectContext asserts `tallpine spawn` gives a project
// setup hint when neither --project, TALLPINE_PROJECT_ID, nor cwd can resolve one.
func TestSpawnCommand_MissingProjectContext(t *testing.T) {
	cfg := setConfigEnv(t)
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appendPrimaryRequest(&requests, r)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects" {
			_, _ = io.WriteString(w, `{"projects":[]}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "spawn", "--agent", "codex", "--name", "worker")
	if err == nil {
		t.Fatal("expected an error when project context is missing")
	}
	if !strings.Contains(err.Error(), "tallpine project add --path <repo-path> --worker-agent <agent>") {
		t.Fatalf("error = %v, want project add hint", err)
	}
	if want := []string{"GET /api/v1/projects"}; !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests=%#v want %#v", requests, want)
	}
}

// TestProjectAddCommand_RequiresPath asserts `tallpine project add` rejects a missing
// --path before touching the network.
func TestProjectAddCommand_RequiresPath(t *testing.T) {
	var out, errb bytes.Buffer
	root := NewRootCommand(Deps{Out: &out, Err: &errb})
	root.SetArgs([]string{"project", "add"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error when --path is missing")
	}
	if !strings.Contains(err.Error(), "--path is required") {
		t.Fatalf("error = %v, want it to mention --path is required", err)
	}
}

func TestSpawnNoTakeoverRequiresClaimPR(t *testing.T) {
	_, _, err := executeCLI(t, Deps{}, "spawn", "--project", "demo", "--name", "worker", "--no-takeover")
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "--no-takeover requires --claim-pr") {
		t.Fatalf("err=%v exit=%d", err, ExitCode(err))
	}
}

// TestSpawnCommand_RequiresName asserts `tallpine spawn` rejects a missing --name
// without contacting the daemon.
func TestSpawnCommand_RequiresName(t *testing.T) {
	_, _, err := executeCLI(t, Deps{}, "spawn", "--project", "demo", "--agent", "codex")
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "--name is required") {
		t.Fatalf("err=%v exit=%d, want --name is required", err, ExitCode(err))
	}
}

// TestSpawnCommand_RejectsOverlongName asserts `tallpine spawn` rejects a --name
// longer than 100 characters without contacting the daemon.
func TestSpawnCommand_RejectsOverlongName(t *testing.T) {
	_, _, err := executeCLI(t, Deps{}, "spawn", "--project", "demo", "--name", strings.Repeat("x", 101))
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "100 characters or fewer") {
		t.Fatalf("err=%v exit=%d, want 100 characters or fewer", err, ExitCode(err))
	}
}

func TestSpawnConfirmationIncludesDisplayName(t *testing.T) {
	// Issue #2592: the confirmation line echoed only the session id, forcing a
	// follow-up lookup to map the id back to the --name just passed.
	tests := []struct {
		name        string
		sessionJSON string
		promptBytes int
		want        string
	}{
		{
			name:        "daemon echoes displayName",
			sessionJSON: `{"id":"demo-11","status":"idle","displayName":"worker"}`,
			want:        `spawned session demo-11 "worker" (idle)`,
		},
		{
			name:        "daemon omits displayName falls back to --name",
			sessionJSON: `{"id":"demo-11","status":"idle"}`,
			want:        `spawned session demo-11 "worker" (idle)`,
		},
		{
			// An asynchronous chat start has not run the agent yet; "idle"
			// would claim a ready session that may still fail to start.
			name:        "chat start still provisioning reads starting",
			sessionJSON: `{"id":"demo-11","status":"idle","displayName":"worker","provisionState":"provisioning"}`,
			want:        `spawned session demo-11 "worker" (starting)`,
		},
		{
			// A delivered task prompt is picked up seconds later; "idle"
			// would read as a worker with nothing to do.
			name:        "idle with a delivered prompt reads starting",
			sessionJSON: `{"id":"demo-11","status":"idle","displayName":"worker"}`,
			promptBytes: 42,
			want:        `spawned session demo-11 "worker" (starting)`,
		},
		{
			name:        "non-idle status with a prompt is kept",
			sessionJSON: `{"id":"demo-11","status":"working","displayName":"worker"}`,
			promptBytes: 42,
			want:        `spawned session demo-11 "worker" (working)`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
					_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","config":{"worker":{"agent":"codex"}}}}`)
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents/readiness/ensure":
					_, _ = io.WriteString(w, authorizedAgentsJSON("codex"))
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
					_, _ = io.WriteString(w, `{"session":`+tt.sessionJSON+`,"promptBytes":`+strconv.Itoa(tt.promptBytes)+`,"systemPromptBytes":0}`)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(srv.Close)
			writeRunFileFor(t, cfg, srv)

			out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
				"spawn", "--project", "demo", "--agent", "codex", "--name", "worker")
			if err != nil {
				t.Fatalf("spawn failed: %v stderr=%s", err, errOut)
			}
			if !strings.Contains(out, tt.want) {
				t.Fatalf("confirmation missing display name:\nwant %q\n got %q", tt.want, out)
			}
		})
	}
}

func TestSpawnResolvesProjectFromEnvAndDefaultAgent(t *testing.T) {
	cfg := setConfigEnv(t)
	var requests []string
	var req apitypes.SpawnSessionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appendPrimaryRequest(&requests, r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","config":{"worker":{"agent":"codex"}}}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents/readiness/ensure":
			_, _ = io.WriteString(w, authorizedAgentsJSON("codex"))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			_, _ = io.WriteString(w, `{"session":{"id":"demo-11","status":"idle"},"promptBytes":0,"systemPromptBytes":123}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	t.Setenv("TALLPINE_PROJECT_ID", "demo")

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "spawn", "--prompt", "Fix failing tests in auth", "--name", "worker")
	if err != nil {
		t.Fatalf("spawn failed: %v stderr=%s", err, errOut)
	}
	if !strings.Contains(out, "spawned session demo-11") {
		t.Fatalf("output missing spawn: %s", out)
	}
	if !strings.Contains(out, "[prompt 0 B, system 123 B]") {
		t.Fatalf("output missing system-only prompt metrics: %s", out)
	}
	if req.ProjectID != "demo" || req.Harness != "codex" || req.DisplayName != "worker" {
		t.Fatalf("spawn request = %#v", req)
	}
	want := []string{"GET /api/v1/projects/demo", "POST /api/v1/agents/readiness/ensure", "POST /api/v1/sessions"}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests=%#v want %#v", requests, want)
	}
}

func TestSpawnResolvesProjectFromTallpineSessionID(t *testing.T) {
	cfg := setConfigEnv(t)
	var requests []string
	var req apitypes.SpawnSessionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appendPrimaryRequest(&requests, r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/demo-1":
			_, _ = io.WriteString(w, `{"session":`+sessionJSON("demo-1", "demo", "worker", "idle", false)+`}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","config":{"worker":{"agent":"codex"}}}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents/readiness/ensure":
			_, _ = io.WriteString(w, authorizedAgentsJSON("codex"))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			_, _ = io.WriteString(w, `{"session":{"id":"demo-15","status":"idle"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	t.Setenv("TALLPINE_SESSION_ID", "demo-1")

	_, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "spawn", "--prompt", "Fix tests", "--name", "worker")
	if err != nil {
		t.Fatalf("spawn failed: %v stderr=%s", err, errOut)
	}
	if req.ProjectID != "demo" || req.Harness != "codex" || req.ParentSessionID != "demo-1" {
		t.Fatalf("spawn request = %#v", req)
	}
	want := []string{"GET /api/v1/sessions/demo-1", "GET /api/v1/projects/demo", "POST /api/v1/agents/readiness/ensure", "POST /api/v1/sessions"}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests=%#v want %#v", requests, want)
	}
}

func TestSpawnTallpineSessionIDFailureRequiresProject(t *testing.T) {
	cfg := setConfigEnv(t)
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appendPrimaryRequest(&requests, r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"not_found","code":"SESSION_NOT_FOUND","message":"Session not found"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	t.Setenv("TALLPINE_SESSION_ID", "missing")

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "spawn", "--agent", "codex", "--name", "worker")
	if err == nil || !strings.Contains(err.Error(), `project could not be resolved from TALLPINE_SESSION_ID "missing"; pass --project`) {
		t.Fatalf("err=%v, want TALLPINE_SESSION_ID project error", err)
	}
	want := []string{"GET /api/v1/sessions/missing"}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests=%#v want %#v", requests, want)
	}
}

func TestSpawnResolvesProjectFromCWD(t *testing.T) {
	cfg := setConfigEnv(t)
	repo := filepath.Join(t.TempDir(), "repo")
	subdir := filepath.Join(repo, "pkg")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(subdir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })

	var req apitypes.SpawnSessionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects":
			_, _ = io.WriteString(w, `{"projects":[{"id":"demo","name":"Demo"}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":`+jsonQuote(repo)+`,"config":{"worker":{"agent":"codex"}}}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents/readiness/ensure":
			_, _ = io.WriteString(w, authorizedAgentsJSON("codex"))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			_, _ = io.WriteString(w, `{"session":{"id":"demo-12","status":"idle"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "spawn", "--prompt", "Fix tests", "--name", "worker")
	if err != nil {
		t.Fatalf("spawn failed: %v stderr=%s", err, errOut)
	}
	if req.ProjectID != "demo" || req.Harness != "codex" {
		t.Fatalf("spawn request = %#v", req)
	}
}

func TestSpawnStandaloneOmitsProject(t *testing.T) {
	cfg := setConfigEnv(t)
	var requests []string
	var req apitypes.SpawnSessionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appendPrimaryRequest(&requests, r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents/readiness/ensure":
			_, _ = io.WriteString(w, authorizedAgentsJSON("codex"))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			_, _ = io.WriteString(w, `{"session":{"id":"standalone-1","status":"idle"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "spawn", "--standalone", "--agent", "codex", "--name", "Try Tallpine", "--prompt", "Try Tallpine")
	if err != nil {
		t.Fatalf("spawn failed: %v stderr=%s", err, errOut)
	}
	if !strings.Contains(out, "spawned session standalone-1") {
		t.Fatalf("output missing standalone session: %s", out)
	}
	if req.ProjectID != "" || req.Harness != "codex" || req.Kind != "worker" || req.Branch != "" {
		t.Fatalf("spawn request = %#v", req)
	}
	want := []string{"POST /api/v1/agents/readiness/ensure", "POST /api/v1/sessions"}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests=%#v want %#v", requests, want)
	}
}

func TestRequestedOutputArtifactSetsRequestedOutput(t *testing.T) {
	cfg := setConfigEnv(t)
	var req map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents/readiness/ensure":
			_, _ = io.WriteString(w, authorizedAgentsJSON("codex"))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			_, _ = io.WriteString(w, `{"session":{"id":"standalone-1","status":"idle"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	if _, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "spawn", "--standalone", "--agent", "codex", "--name", "Report", "--output", "artifact"); err != nil {
		t.Fatalf("spawn failed: %v stderr=%s", err, errOut)
	}
	if req["requestedOutput"] != "artifact" {
		t.Fatalf("requestedOutput = %#v, want artifact; request=%#v", req["requestedOutput"], req)
	}
}

func TestSpawnOutputRejectsInvalidUse(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "unknown value", args: []string{"--output", "slides"}, want: `--output must be "artifact"`},
		{name: "orchestrator", args: []string{"--output", "artifact", "--kind", "orchestrator"}, want: "--output is supported for worker sessions only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"spawn", "--project", "demo", "--name", "worker"}, tc.args...)
			_, _, err := executeCLI(t, Deps{}, args...)
			if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v exit=%d, want %q", err, ExitCode(err), tc.want)
			}
		})
	}
}

func TestSpawnStandaloneRejectsProjectOnlyOptions(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "project", args: []string{"--project", "demo"}},
		{name: "branch", args: []string{"--branch", "feature/x"}},
		{name: "issue", args: []string{"--issue", "42"}},
		{name: "claim pr", args: []string{"--claim-pr", "42"}},
		{name: "orchestrator", args: []string{"--kind", "orchestrator"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"spawn", "--standalone", "--agent", "codex", "--name", "Notes"}
			args = append(args, tc.args...)
			_, _, err := executeCLI(t, Deps{}, args...)
			if err == nil || ExitCode(err) != 2 {
				t.Fatalf("err=%v exit=%d, want usage error", err, ExitCode(err))
			}
		})
	}
}

func TestSpawnScratchRejectsGitOnlyFlags(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "branch", args: []string{"spawn", "--project", "scratch", "--agent", "codex", "--name", "Scratch Task", "--branch", "feature/x"}, wantErr: "scratch projects do not support --branch"},
		{name: "claim pr", args: []string{"spawn", "--project", "scratch", "--agent", "codex", "--name", "Scratch Task", "--claim-pr", "142"}, wantErr: "scratch projects do not support --claim-pr"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			var requests []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				appendPrimaryRequest(&requests, r)
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/scratch":
					_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"scratch","name":"Scratch","kind":"scratch","path":"/tallpine/scratch","config":{"worker":{"agent":"codex"}}}}`)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(srv.Close)
			writeRunFileFor(t, cfg, srv)

			_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, tc.args...)
			if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err=%v exit=%d, want %q", err, ExitCode(err), tc.wantErr)
			}
			want := []string{"GET /api/v1/projects/scratch"}
			if !reflect.DeepEqual(requests, want) {
				t.Fatalf("requests=%#v want %#v", requests, want)
			}
		})
	}
}

// TestSpawnCommand_RejectsInvalidKind asserts `tallpine spawn` rejects a --kind value
// outside worker/orchestrator at the CLI boundary, without contacting the daemon.
func TestSpawnCommand_RejectsInvalidKind(t *testing.T) {
	// Pass a valid --name so this exercises the --kind boundary specifically:
	// spawn validates the required --name before --kind, so omitting it would
	// trip the "--name is required" error instead of the kind error.
	_, _, err := executeCLI(t, Deps{}, "spawn", "--project", "demo", "--name", "orch", "--kind", "orchestartor")
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), `--kind must be "worker" or "orchestrator"`) {
		t.Fatalf("err=%v exit=%d, want --kind validation error", err, ExitCode(err))
	}
}

// TestResolveSpawnHarness_OrchestratorDefault asserts the orchestrator role falls
// back to the project's orchestrator agent (and worker to the worker agent), while
// an explicit --agent always wins.
func TestResolveSpawnHarness_OrchestratorDefault(t *testing.T) {
	project := projectDetails{
		ID: "demo",
		Config: &projectConfig{
			Worker:       roleOverride{Agent: "codex"},
			Orchestrator: roleOverride{Agent: "claude-code"},
		},
	}
	if got, err := resolveSpawnHarness("", "orchestrator", project); err != nil || got != "claude-code" {
		t.Fatalf("orchestrator default: got %q err %v, want claude-code", got, err)
	}
	if got, err := resolveSpawnHarness("", "worker", project); err != nil || got != "codex" {
		t.Fatalf("worker default: got %q err %v, want codex", got, err)
	}
	if got, err := resolveSpawnHarness("aider", "orchestrator", project); err != nil || got != "aider" {
		t.Fatalf("explicit agent: got %q err %v, want aider", got, err)
	}
	// Unset kind is the default `tallpine spawn` path and must resolve to worker.agent.
	if got, err := resolveSpawnHarness("", "", project); err != nil || got != "codex" {
		t.Fatalf("unset kind: got %q err %v, want codex", got, err)
	}
	// Orchestrator spawn with no orchestrator.agent configured surfaces the
	// --orchestrator-agent hint (the error branch this PR adds).
	noOrch := projectDetails{ID: "demo", Config: &projectConfig{Worker: roleOverride{Agent: "codex"}}}
	if _, err := resolveSpawnHarness("", "orchestrator", noOrch); err == nil || !strings.Contains(err.Error(), "--orchestrator-agent") {
		t.Fatalf("missing orchestrator agent: err=%v, want --orchestrator-agent hint", err)
	}
}

// TestSpawnModelFlagWiring asserts `tallpine spawn --model` sends the model override
// to the daemon without touching project config.
func TestSpawnModelFlagWiring(t *testing.T) {
	cfg := setConfigEnv(t)
	var req apitypes.SpawnSessionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents/readiness/ensure":
			_, _ = io.WriteString(w, authorizedAgentsJSON("codex"))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			_, _ = io.WriteString(w, `{"session":{"id":"demo-20","status":"idle"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "spawn", "--project", "demo", "--agent", "codex", "--name", "worker", "--model", "gpt-5.6-sol")
	if err != nil {
		t.Fatalf("spawn failed: %v stderr=%s", err, errOut)
	}
	if req.Model != "gpt-5.6-sol" {
		t.Fatalf("spawn request model = %q, want gpt-5.6-sol", req.Model)
	}
}

// TestSpawnEffortFlagWiring asserts `tallpine spawn --effort` sends the reasoning
// effort override to the daemon without touching project config.
func TestSpawnEffortFlagWiring(t *testing.T) {
	cfg := setConfigEnv(t)
	var req apitypes.SpawnSessionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents/readiness/ensure":
			_, _ = io.WriteString(w, authorizedAgentsJSON("codex"))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			_, _ = io.WriteString(w, `{"session":{"id":"demo-21","status":"idle"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "spawn", "--project", "demo", "--agent", "codex", "--name", "worker", "--effort", " high ")
	if err != nil {
		t.Fatalf("spawn failed: %v stderr=%s", err, errOut)
	}
	if req.Effort != "high" {
		t.Fatalf("spawn request effort = %q, want high", req.Effort)
	}
}

// TestSpawnEffortSurfacesDaemonUnsupportedEffort asserts `tallpine spawn --effort`
// on a harness that cannot honor it fails with the daemon's UNSUPPORTED_EFFORT
// envelope instead of a CLI-side harness allowlist: the daemon owns which
// harnesses support effort, so the list cannot drift in the thin client.
func TestSpawnEffortSurfacesDaemonUnsupportedEffort(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents/readiness/ensure":
			_, _ = io.WriteString(w, authorizedAgentsJSON("gemini"))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"invalid","code":"UNSUPPORTED_EFFORT","message":"effort is only supported for codex, claude-code, and command-code agents","requestId":"req-1"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "spawn", "--project", "demo", "--agent", "gemini", "--name", "Worker", "--effort", "high")
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("err=%v exit=%d, want daemon error exit 1", err, ExitCode(err))
	}
	if !strings.Contains(err.Error(), "UNSUPPORTED_EFFORT") || !strings.Contains(err.Error(), "effort is only supported") {
		t.Fatalf("err=%v, want daemon UNSUPPORTED_EFFORT envelope", err)
	}
}

func TestSpawnWorkerSkipsForbiddenReadinessPreflight(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","repo":"https://github.com/justvibedev/tallpine-dev","defaultBranch":"main"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents/readiness/ensure":
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"error":"forbidden","code":"AGENT_SCOPE_FORBIDDEN","message":"the worker role has no access to this route"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			_, _ = io.WriteString(w, `{"session":{"id":"demo-9","status":"idle"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "spawn", "--project", "demo", "--agent", "codex", "--name", "worker")
	if err != nil {
		t.Fatalf("spawn failed: %v stderr=%s", err, errOut)
	}
	if !strings.Contains(errOut, "cannot check agent") {
		t.Fatalf("stderr missing warning: %s", errOut)
	}
}

// resolveProjectFromCWD matches inclusively (cwd == project root counts), and a
// project whose path merely shares a string prefix with cwd (repo vs
// repo-extra) must not match.
func TestResolveProjectFromCWDContainment(t *testing.T) {
	cfg := setConfigEnv(t)
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	sibling := filepath.Join(base, "repo-extra")
	for _, d := range []string{repo, sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/projects":
			_, _ = io.WriteString(w, `{"projects":[{"id":"short"}]}`)
		case "/api/v1/projects/short":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"short","path":`+jsonQuote(repo)+`}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	c := &commandContext{deps: Deps{ProcessAlive: func(int) bool { return true }}.withDefaults()}

	t.Chdir(repo)
	if got, ok, err := c.resolveProjectFromCWD(context.Background()); err != nil || !ok || got.ID != "short" {
		t.Fatalf("cwd at project root: %#v ok=%v err=%v", got, ok, err)
	}
	t.Chdir(sibling)
	if got, ok, err := c.resolveProjectFromCWD(context.Background()); err != nil || ok {
		t.Fatalf("prefix-sibling cwd matched %#v ok=%v err=%v", got, ok, err)
	}
}
