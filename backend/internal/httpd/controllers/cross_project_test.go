package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules"
	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
	"tallpine.eu/tallpine/backend/internal/sessiontoken"
)

type memPolicyStore struct{ doc []byte }

func (m *memPolicyStore) AgentPolicy(context.Context) ([]byte, error) { return m.doc, nil }
func (m *memPolicyStore) SetAgentPolicy(_ context.Context, b []byte, _ time.Time) error {
	m.doc = b
	return nil
}

// creatorScope is the scope store plus the project-creator table.
type creatorScope struct {
	scopeSessions
	mu       sync.Mutex
	creators map[domain.ProjectID]domain.ProjectID
}

func (c *creatorScope) SetProjectCreator(_ context.Context, project, creator domain.ProjectID, _ time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.creators[project] = creator
	return nil
}

func (c *creatorScope) ProjectCreator(_ context.Context, project domain.ProjectID) (domain.ProjectID, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	creator, ok := c.creators[project]
	return creator, ok, nil
}

type crossProjects struct {
	projectsvc.Manager
	list    []projectsvc.Summary
	creator *creatorScope
	// appConfig is the config of project app (the creator) that Get returns.
	appConfig domain.ProjectConfig
}

func (p *crossProjects) Get(_ context.Context, id domain.ProjectID) (projectsvc.GetResult, error) {
	cfg := p.appConfig
	return projectsvc.GetResult{Status: "ok", Project: &projectsvc.Project{ID: id, Config: &cfg}}, nil
}

func (p *crossProjects) List(context.Context) ([]projectsvc.Summary, error) {
	return append([]projectsvc.Summary(nil), p.list...), nil
}

func (p *crossProjects) Add(_ context.Context, in projectsvc.AddInput) (projectsvc.Project, error) {
	id := domain.ProjectID("lab")
	if in.CreatedBy != "" {
		// Service.Add records the creator (service/project/agent_create_test.go).
		_ = p.creator.SetProjectCreator(context.Background(), id, in.CreatedBy, time.Now())
	}
	p.list = append(p.list, projectsvc.Summary{ID: id, Name: "Lab", Path: in.Path})
	return projectsvc.Project{ID: id, Name: "Lab", Path: in.Path}, nil
}

func (p *crossProjects) SetConfig(_ context.Context, id domain.ProjectID, _ projectsvc.SetConfigInput) (projectsvc.Project, error) {
	return projectsvc.Project{ID: id}, nil
}

type crossFixture struct {
	scopeFixture
	engine   *agentpolicy.Engine
	creator  *creatorScope
	projects *crossProjects
}

// newCrossFixture is the scope fixture behind the real policy engine, with
// projects app and web (people registered) and lab (created by app).
func newCrossFixture(t *testing.T) crossFixture {
	t.Helper()
	engine, err := agentpolicy.New(rules.All(), &memPolicyStore{})
	if err != nil {
		t.Fatal(err)
	}
	svc := &fakeSessionService{sessions: map[domain.SessionID]domain.Session{}}
	for _, row := range []struct {
		id, project string
		kind        domain.SessionKind
	}{
		{"app-1", "app", domain.KindWorker},
		{"app-orch", "app", domain.KindOrchestrator},
		{"web-1", "web", domain.KindWorker},
		{"web-orch", "web", domain.KindOrchestrator},
		{"lab-orch", "lab", domain.KindOrchestrator},
	} {
		sess := domain.Session{SessionRecord: domain.SessionRecord{ID: domain.SessionID(row.id), ProjectID: domain.ProjectID(row.project), Kind: row.kind}}
		sess.Metadata.BrowserCapabilityVerifier = "v-" + row.id
		svc.sessions[sess.ID] = sess
	}
	creator := &creatorScope{scopeSessions: scopeSessions{svc: svc}, creators: map[domain.ProjectID]domain.ProjectID{"lab": "app"}}
	projects := &crossProjects{creator: creator, list: []projectsvc.Summary{{ID: "app", Name: "App", Path: "/p/app"}, {ID: "web", Name: "Web", Path: "/p/web"}, {ID: "lab", Name: "Lab", Path: "/p/lab"}}}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil,
		httpd.APIDeps{Sessions: svc, Projects: projects, AgentPolicy: engine, AgentScopes: creator, PRScope: scopeSessions{svc: svc},
			Reports: &fakeReportService{}, PRs: &fakePRService{}, Reviews: &fakeReviewService{}},
		httpd.ControlDeps{DaemonToken: scopeInstallToken}))
	t.Cleanup(srv.Close)
	f := scopeFixture{t: t, srv: srv, svc: svc, tokens: sessiontoken.New(scopeInstallToken)}
	return crossFixture{scopeFixture: f, engine: engine, creator: creator, projects: projects}
}

func (f crossFixture) setPolicy(project string, on bool) {
	f.t.Helper()
	value := "false"
	if on {
		value = "true"
	}
	update := agentpolicy.Update{"crossProjectOrchestration": {"enabled": json.RawMessage(value)}}
	if err := f.engine.Update(context.Background(), project, update, time.Now()); err != nil {
		f.t.Fatal(err)
	}
}

func (f crossFixture) enabledIn(project string) bool {
	view := f.engine.Describe(context.Background(), project)
	for _, r := range view.Rules {
		if r.Name == "crossProjectOrchestration" {
			for _, field := range r.Fields {
				if field.Name == "enabled" {
					return string(field.Value) == "true"
				}
			}
		}
	}
	return false
}

type crossCase struct {
	method, path, body string
}

var crossActions = []crossCase{
	{"GET", "/api/v1/orchestrators/web-orch", ""},
	{"GET", "/api/v1/sessions/web-orch", ""},
	{"POST", "/api/v1/sessions/web-orch/send", `{"message":"hi"}`},
	{"POST", "/api/v1/sessions/web-orch/conversation/steer-or-send", `{"text":"hi","clientMessageId":"m1"}`},
	{"POST", "/api/v1/projects", `{"path":"/p/new"}`},
	{"PUT", "/api/v1/projects/lab/config", `{"config":{}}`},
	{"GET", "/api/v1/projects/lab", ""},
	{"POST", "/api/v1/orchestrators", `{"projectId":"lab"}`},
}

// T1: with the policy off, every new capability is refused.
func TestCrossProjectOffByDefaultRefusesEverything(t *testing.T) {
	f := newCrossFixture(t)
	for _, c := range crossActions {
		status, body := f.do(f.as("app-orch"), c.method, c.path, c.body)
		if status != http.StatusForbidden || !strings.Contains(body, "AGENT_SCOPE_FORBIDDEN") {
			t.Errorf("%s %s = %d %s, want 403", c.method, c.path, status, body)
		}
	}
	// The reason names the policy.
	_, body := f.do(f.as("app-orch"), "POST", "/api/v1/sessions/web-orch/send", `{"message":"hi"}`)
	if !strings.Contains(body, "crossProjectOrchestration") || !strings.Contains(body, "off") {
		t.Errorf("refusal does not name the policy: %s", body)
	}
	if got := strings.Join(f.listedIDs(f.as("app-orch"), "/api/v1/orchestrators"), ","); got != "app-orch" {
		t.Errorf("orchestrators listed %s, want only the own project's", got)
	}
	if got := strings.Join(f.listedIDs(f.as("app-orch"), "/api/v1/projects"), ","); got != "app" {
		t.Errorf("projects listed %s, want app", got)
	}
}

// T2: with the sender's policy on, a message to another project's orchestrator
// is delivered framed as orchestrator data.
func TestCrossProjectSendToOrchestratorIsFramed(t *testing.T) {
	f := newCrossFixture(t)
	f.setPolicy("app", true)
	status, body := f.do(f.as("app-orch"), "POST", "/api/v1/sessions/web-orch/send", `{"message":"run the experiment"}`)
	if status != http.StatusOK {
		t.Fatalf("send = %d %s", status, body)
	}
	if got := f.svc.sent; !strings.HasPrefix(got, "<<orchestrator:app-orch n=") || !strings.Contains(got, "run the experiment") {
		t.Fatalf("cross-project message not framed: %q", got)
	}
	status, body = f.do(f.as("app-orch"), "POST", "/api/v1/sessions/web-orch/conversation/steer-or-send", `{"text":"steer me","clientMessageId":"m1"}`)
	// The fixture has no conversation service (501); the gate let it through.
	if status == http.StatusForbidden {
		t.Fatalf("steer-or-send = %d %s", status, body)
	}
}

// T3 + T4: workers and reviewers of other projects stay out of reach; worker
// and reviewer tokens of the opted-in project get nothing new.
func TestCrossProjectNeverReachesWorkersAndGrantsOtherRolesNothing(t *testing.T) {
	f := newCrossFixture(t)
	f.setPolicy("app", true)
	for _, c := range []crossCase{
		{"GET", "/api/v1/sessions/web-1", ""},
		{"POST", "/api/v1/sessions/web-1/send", `{"message":"hi"}`},
		{"POST", "/api/v1/sessions/web-1/conversation/steer-or-send", `{"text":"hi","clientMessageId":"m1"}`},
		{"POST", "/api/v1/sessions/web-1/kill", ""},
	} {
		if status, body := f.do(f.as("app-orch"), c.method, c.path, c.body); status != http.StatusForbidden {
			t.Errorf("orchestrator %s %s = %d %s, want 403", c.method, c.path, status, body)
		}
	}
	for _, token := range []string{f.as("app-1")} {
		for _, c := range crossActions {
			if status, body := f.do(token, c.method, c.path, c.body); status != http.StatusForbidden {
				t.Errorf("worker %s %s = %d %s, want 403", c.method, c.path, status, body)
			}
		}
	}
}

// T5: project create records the creator and turns the policy on; config and
// orchestrator start work only on projects the caller's project created.
func TestCrossProjectCreateConfigureAndStart(t *testing.T) {
	f := newCrossFixture(t)
	f.setPolicy("app", true)
	delete(f.creator.creators, "lab")

	status, body := f.do(f.as("app-orch"), "POST", "/api/v1/projects", `{"path":"/p/lab"}`)
	if status != http.StatusCreated {
		t.Fatalf("create = %d %s", status, body)
	}
	if got := f.creator.creators["lab"]; got != "app" {
		t.Fatalf("creator = %q, want app", got)
	}
	if !f.enabledIn("lab") {
		t.Fatal("the created project did not get crossProjectOrchestration on")
	}
	for _, c := range []crossCase{
		{"PUT", "/api/v1/projects/lab/config", `{"config":{}}`},
		{"POST", "/api/v1/orchestrators", `{"projectId":"lab"}`},
	} {
		if status, body := f.do(f.as("app-orch"), c.method, c.path, c.body); status >= 400 {
			t.Errorf("%s %s = %d %s, want success", c.method, c.path, status, body)
		}
	}
	if status, _ := f.do(f.as("app-orch"), "GET", "/api/v1/projects/lab", ""); status == http.StatusForbidden {
		t.Error("an orchestrator may read a project its project created")
	}
	// A project a person registered, or another project created, is out of reach.
	for _, c := range []crossCase{
		{"PUT", "/api/v1/projects/web/config", `{"config":{}}`},
		{"POST", "/api/v1/orchestrators", `{"projectId":"web"}`},
		{"POST", "/api/v1/orchestrators", `{"projectId":"lab","lane":"B1"}`},
		{"POST", "/api/v1/orchestrators", `{"projectId":"lab","approvalMode":"bypass-permissions"}`},
		{"POST", "/api/v1/projects", `{"path":"/p/x","asWorkspace":true}`},
	} {
		if status, body := f.do(f.as("app-orch"), c.method, c.path, c.body); status != http.StatusForbidden {
			t.Errorf("%s %s %s = %d %s, want 403", c.method, c.path, c.body, status, body)
		}
	}
	f.creator.creators["lab"] = "web"
	if status, body := f.do(f.as("app-orch"), "PUT", "/api/v1/projects/lab/config", `{"config":{}}`); status != http.StatusForbidden {
		t.Errorf("config on a project another project created = %d %s, want 403", status, body)
	}
	if status, body := f.do(f.as("app-orch"), "POST", "/api/v1/orchestrators", `{"projectId":"lab"}`); status != http.StatusForbidden {
		t.Errorf("start on a project another project created = %d %s, want 403", status, body)
	}
}

// T6: an opted-in orchestrator lists other projects' orchestrators and
// projects, never their workers.
func TestCrossProjectListsShowOrchestratorsOnly(t *testing.T) {
	f := newCrossFixture(t)
	f.setPolicy("app", true)
	if got := strings.Join(f.listedIDs(f.as("app-orch"), "/api/v1/orchestrators"), ","); got != "app-orch,lab-orch,web-orch" {
		t.Errorf("orchestrators listed %s", got)
	}
	if got := strings.Join(f.listedIDs(f.as("app-orch"), "/api/v1/sessions"), ","); got != "app-1,app-orch" {
		t.Errorf("sessions listed %s, want the own project's only", got)
	}
	if got := strings.Join(f.listedIDs(f.as("app-orch"), "/api/v1/projects"), ","); got != "app,lab,web" {
		t.Errorf("projects listed %s", got)
	}
	_, body := f.do(f.as("app-orch"), "GET", "/api/v1/projects", "")
	if strings.Contains(body, "/p/web") {
		t.Errorf("another project's path leaked: %s", body)
	}
}

// T7: toggling the policy off applies on the next request.
func TestCrossProjectToggleAppliesWithoutRestart(t *testing.T) {
	f := newCrossFixture(t)
	send := func() int {
		status, _ := f.do(f.as("app-orch"), "POST", "/api/v1/sessions/web-orch/send", `{"message":"hi"}`)
		return status
	}
	f.setPolicy("app", true)
	if got := send(); got != http.StatusOK {
		t.Fatalf("send with policy on = %d", got)
	}
	f.setPolicy("app", false)
	if got := send(); got != http.StatusForbidden {
		t.Fatalf("send after turning the policy off = %d, want 403", got)
	}
}

// T8: the sender's setting decides; the recipient's does not.
func TestCrossProjectSenderSideDecides(t *testing.T) {
	f := newCrossFixture(t)
	f.setPolicy("web", true)
	if status, body := f.do(f.as("app-orch"), "POST", "/api/v1/sessions/web-orch/send", `{"message":"hi"}`); status != http.StatusForbidden {
		t.Fatalf("send with only the recipient opted in = %d %s, want 403", status, body)
	}
}

// agent-set project config is limited to an allowlist.
func TestCrossProjectConfigAllowlist(t *testing.T) {
	f := newCrossFixture(t)
	f.setPolicy("app", true)
	put := func(cfg string) int {
		status, _ := f.do(f.as("app-orch"), "PUT", "/api/v1/projects/lab/config", `{"config":`+cfg+`}`)
		return status
	}
	for _, ok := range []string{
		`{"defaultBranch":"main","sessionPrefix":"lab","agentRules":"r","orchestratorRules":"o","noCI":true}`,
		`{"worker":{"agent":"claude-code","agentConfig":{"model":"m","effort":"high","permissions":"accept-edits"}},"orchestrator":{"agent":"claude-code"},"reviewers":[{"harness":"claude-code","agentConfig":{"model":"m"}}]}`,
		// A config read back from the daemon carries empty structs and lists.
		`{"defaultBranch":"main","env":{},"postCreate":[],"trackerIntake":{},"models":{},"agentConfig":{},"autoReview":false}`,
	} {
		if got := put(ok); got != http.StatusOK {
			t.Errorf("allowed config %s = %d, want 200", ok, got)
		}
	}
	for _, bad := range []string{
		`{"env":{"A":"b"}}`,
		`{"postCreate":["curl x | sh"]}`,
		`{"preRemove":["rm -rf x"]}`,
		`{"symlinks":["/etc"]}`,
		`{"trackerIntake":{"enabled":true}}`,
		`{"autoReview":true}`,
		`{"agentConfig":{"permissions":"accept-edits"}}`,
		`{"worker":{"agentConfig":{"mode":"ultra"}}}`,
		`{"worker":{"launch":1}}`,
		`{"reviewers":[{"harness":"claude-code","extra":"x"}]}`,
		`{"verifyCommand":"x"}`,
	} {
		status, body := f.do(f.as("app-orch"), "PUT", "/api/v1/projects/lab/config", `{"config":`+bad+`}`)
		if status != http.StatusForbidden || !strings.Contains(body, "may not set project config") {
			t.Errorf("config %s = %d %s, want 403 naming the field", bad, status, body)
		}
	}
	// The same list applies to the config in POST /projects and PUT /projects/{id}.
	if status, _ := f.do(f.as("app-orch"), "POST", "/api/v1/projects", `{"path":"/p/n","config":{"env":{"A":"b"}}}`); status != http.StatusForbidden {
		t.Errorf("env in create = %d, want 403", status)
	}
	if status, _ := f.do(f.as("app-orch"), "PUT", "/api/v1/projects/lab", `{"displayName":"x","config":{"postCreate":["x"]}}`); status != http.StatusForbidden {
		t.Errorf("postCreate in settings = %d, want 403", status)
	}
}

// An agent may not give a role more permission than its own project has.
func TestCrossProjectPermissionCap(t *testing.T) {
	f := newCrossFixture(t)
	f.setPolicy("app", true)
	f.projects.appConfig.Worker.AgentConfig.Permissions = domain.PermissionModeAcceptEdits
	f.projects.appConfig.AgentConfig.Permissions = domain.PermissionModeAuto
	put := func(cfg string) int {
		status, _ := f.do(f.as("app-orch"), "PUT", "/api/v1/projects/lab/config", `{"config":`+cfg+`}`)
		return status
	}
	for cfg, want := range map[string]int{
		`{"worker":{"agentConfig":{"permissions":"accept-edits"}}}`:                                    http.StatusOK,
		`{"worker":{"agentConfig":{"permissions":"default"}}}`:                                         http.StatusOK,
		`{"worker":{"agentConfig":{"permissions":"auto"}}}`:                                            http.StatusForbidden, // creator's worker is accept-edits
		`{"worker":{"agentConfig":{"permissions":"bypass-permissions"}}}`:                              http.StatusForbidden,
		`{"orchestrator":{"agentConfig":{"permissions":"auto"}}}`:                                      http.StatusOK, // falls back to the creator default (auto)
		`{"orchestrator":{"agentConfig":{"permissions":"bypass-permissions"}}}`:                        http.StatusForbidden,
		`{"reviewers":[{"harness":"claude-code","agentConfig":{"permissions":"bypass-permissions"}}]}`: http.StatusForbidden,
	} {
		if got := put(cfg); got != want {
			t.Errorf("%s = %d, want %d", cfg, got, want)
		}
	}
	if status, _ := f.do(f.as("app-orch"), "POST", "/api/v1/projects", `{"path":"/p/n","config":{"worker":{"agentConfig":{"permissions":"bypass-permissions"}}}}`); status != http.StatusForbidden {
		t.Errorf("cap in create = %d, want 403", status)
	}
}

// With no explicit setting the cap is the mode a launch would use (project
// default, then auto), not a fixed value.
func TestCrossProjectPermissionCapUsesEffectiveMode(t *testing.T) {
	f := newCrossFixture(t)
	f.setPolicy("app", true)
	f.projects.appConfig.AgentConfig.Permissions = domain.PermissionModeDefault
	f.projects.appConfig.Worker.AgentConfig.Permissions = ""
	f.projects.appConfig.Orchestrator.AgentConfig.Permissions = ""
	for cfg, want := range map[string]int{
		`{"orchestrator":{"agentConfig":{"permissions":"default"}}}`: http.StatusOK,
		`{"orchestrator":{"agentConfig":{"permissions":"auto"}}}`:    http.StatusForbidden,
		`{"worker":{"agentConfig":{"permissions":"accept-edits"}}}`:  http.StatusForbidden,
	} {
		if status, _ := f.do(f.as("app-orch"), "PUT", "/api/v1/projects/lab/config", `{"config":`+cfg+`}`); status != want {
			t.Errorf("%s = %d, want %d", cfg, status, want)
		}
	}
}

// Brief: the initial prompt of `orchestrator new --prompt` rides the start
// (delivered by the spawn once the chat controller is live, unframed), so it
// never races a still-starting session; later messages stay framed.
func TestCrossProjectInitialBriefRidesTheStartUnframed(t *testing.T) {
	f := newCrossFixture(t)
	f.setPolicy("app", true)
	f.svc.orchestratorBriefCreated = true
	body := `{"projectId":"lab","prompt":"do the lab work"}`
	status, out := f.do(f.as("app-orch"), "POST", "/api/v1/orchestrators", body)
	if status != http.StatusCreated || !strings.Contains(out, `"promptDelivered":true`) {
		t.Fatalf("start with prompt = %d %s", status, out)
	}
	// Exactly one delivery, exactly the text (no frame), and no /send involved.
	if f.svc.orchestratorBriefCalls != 1 || f.svc.orchestratorBrief != "do the lab work" || f.svc.sent != "" {
		t.Fatalf("brief calls=%d brief=%q sent=%q", f.svc.orchestratorBriefCalls, f.svc.orchestratorBrief, f.svc.sent)
	}
	// A later message from the same orchestrator is data.
	if status, out := f.do(f.as("app-orch"), "POST", "/api/v1/sessions/lab-orch/send", `{"message":"later"}`); status != http.StatusOK {
		t.Fatalf("send = %d %s", status, out)
	}
	if !strings.HasPrefix(f.svc.sent, "<<orchestrator:app-orch n=") {
		t.Fatalf("later message not framed: %q", f.svc.sent)
	}
}

// A project the caller's project did not create, or a worker token, cannot use the prompt.
func TestCrossProjectInitialBriefRefusals(t *testing.T) {
	f := newCrossFixture(t)
	f.setPolicy("app", true)
	f.svc.orchestratorBriefCreated = true
	for _, tok := range []struct{ who, project string }{{"app-orch", "web"}, {"app-1", "lab"}} {
		body := `{"projectId":"` + tok.project + `","prompt":"x"}`
		if status, out := f.do(f.as(tok.who), "POST", "/api/v1/orchestrators", body); status != http.StatusForbidden {
			t.Errorf("%s -> %s = %d %s, want 403", tok.who, tok.project, status, out)
		}
	}
	if f.svc.orchestratorBriefCalls != 0 {
		t.Fatalf("a refused request reached the service %d times", f.svc.orchestratorBriefCalls)
	}
}

func TestCrossProjectInitialBriefTooLong(t *testing.T) {
	f := newCrossFixture(t)
	f.setPolicy("app", true)
	body := `{"projectId":"lab","prompt":"` + strings.Repeat("x", 16<<10+1) + `"}`
	if status, out := f.do(f.as("app-orch"), "POST", "/api/v1/orchestrators", body); status != http.StatusBadRequest || !strings.Contains(out, "PROMPT_TOO_LONG") {
		t.Fatalf("oversized brief = %d %s", status, out)
	}
	if f.svc.orchestratorBriefCalls != 0 {
		t.Fatal("oversized brief reached the service")
	}
}

// B206: listing another project's sessions is refused, never rewritten to the
// caller's own project.
func TestAgentSessionListRefusesOtherProject(t *testing.T) {
	f := newCrossFixture(t)
	f.setPolicy("app", true)
	status, body := f.do(f.as("app-orch"), "GET", "/api/v1/sessions?project=web", "")
	if status != http.StatusForbidden || !strings.Contains(body, "AGENT_SCOPE_FORBIDDEN") {
		t.Errorf("status %d body %s, want 403 AGENT_SCOPE_FORBIDDEN", status, body)
	}
	if got := strings.Join(f.listedIDs(f.as("app-orch"), "/api/v1/sessions?project=app"), ","); got != "app-1,app-orch" {
		t.Errorf("own project listed %s", got)
	}
}
