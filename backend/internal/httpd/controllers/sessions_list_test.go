package controllers_test

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	previewutil "tallpine.eu/tallpine/backend/internal/preview"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
	"tallpine.eu/tallpine/backend/pkg/contract"
)

func TestSessionsRoutes_DefaultToStubsWithoutService(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	body, status, headers := doRequest(t, srv, "GET", "/api/v1/sessions", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")

	body, status, headers = doRequest(t, srv, "GET", "/api/v1/sessions/tp-1/pr/42/file/revision?path=README.md", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

func TestSessionsAPI_AcknowledgeInterfaceTransitionNotice(t *testing.T) {
	acknowledgedAt := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	svc := &fakeInterfaceTransitionSessionService{
		fakeSessionService: newFakeSessionService(),
		transition: domain.SessionInterfaceTransition{
			ID: "transition-1", SessionID: "tp-1",
			SourceMode: domain.SessionModeChat, TargetMode: domain.SessionModeTUI,
			Policy:    domain.SessionInterfaceTransitionDrain,
			Phase:     domain.SessionInterfaceTransitionRecovery,
			CreatedAt: acknowledgedAt.Add(-time.Hour), UpdatedAt: acknowledgedAt.Add(-time.Minute),
			CompletedAt: acknowledgedAt.Add(-time.Minute), NoticeAcknowledgedAt: acknowledgedAt,
		},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(
		config.Config{}, log, nil, httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{},
	))
	t.Cleanup(srv.Close)

	body, status, _ := doRequest(t, srv, http.MethodPut,
		"/api/v1/sessions/tp-1/interface-transition/transition-1/notice-acknowledgement", "")
	if status != http.StatusOK {
		t.Fatalf("acknowledge notice = %d, want 200; body=%s", status, body)
	}
	if svc.acknowledgedSessionID != "tp-1" || svc.acknowledgedTransition != "transition-1" {
		t.Fatalf("acknowledgement target = %s/%s", svc.acknowledgedSessionID, svc.acknowledgedTransition)
	}
	var response controllers.InterfaceTransitionNoticeAckResponse
	mustJSON(t, body, &response)
	if !response.OK || response.Transition.NoticeAcknowledgedAt == nil ||
		!response.Transition.NoticeAcknowledgedAt.Equal(acknowledgedAt) {
		t.Fatalf("acknowledgement response = %+v", response)
	}
}

func TestSessionsAPI_StartInterfaceTransitionCarriesExplicitHistoryPolicy(t *testing.T) {
	now := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	svc := &fakeInterfaceTransitionSessionService{
		fakeSessionService: newFakeSessionService(),
		transition: domain.SessionInterfaceTransition{
			ID: "transition-provider-history", SessionID: "tp-1",
			SourceMode: domain.SessionModeTUI, TargetMode: domain.SessionModeChat,
			Policy:        domain.SessionInterfaceTransitionDrain,
			HistoryPolicy: domain.SessionInterfaceTransitionHistoryProvider,
			Phase:         domain.SessionInterfaceTransitionRequested, CreatedAt: now, UpdatedAt: now,
		},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(
		config.Config{}, log, nil, httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{},
	))
	t.Cleanup(srv.Close)

	body, status, _ := doRequest(t, srv, http.MethodPost,
		"/api/v1/sessions/tp-1/interface-transition",
		`{"targetMode":"chat","policy":"drain","historyPolicy":"provider_history"}`)
	if status != http.StatusAccepted {
		t.Fatalf("start transition = %d, want 202; body=%s", status, body)
	}
	if svc.startedSessionID != "tp-1" || svc.startedTarget != domain.SessionModeChat ||
		svc.startedPolicy != domain.SessionInterfaceTransitionDrain ||
		svc.startedHistoryPolicy != domain.SessionInterfaceTransitionHistoryProvider {
		t.Fatalf("start input = session:%q target:%q policy:%q history:%q", svc.startedSessionID,
			svc.startedTarget, svc.startedPolicy, svc.startedHistoryPolicy)
	}
	var response controllers.StartSessionInterfaceTransitionResponse
	mustJSON(t, body, &response)
	if response.Transition.HistoryPolicy != domain.SessionInterfaceTransitionHistoryProvider {
		t.Fatalf("response history policy = %q", response.Transition.HistoryPolicy)
	}
}

func TestSessionsAPI_StartInterfaceTransitionDefaultsOmittedHistoryPolicyToStrict(t *testing.T) {
	now := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	svc := &fakeInterfaceTransitionSessionService{
		fakeSessionService: newFakeSessionService(),
		transition: domain.SessionInterfaceTransition{
			ID: "transition-strict", SessionID: "tp-1",
			SourceMode: domain.SessionModeTUI, TargetMode: domain.SessionModeChat,
			Policy: domain.SessionInterfaceTransitionDrain, Phase: domain.SessionInterfaceTransitionRequested,
			CreatedAt: now, UpdatedAt: now,
		},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(
		config.Config{}, log, nil, httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{},
	))
	t.Cleanup(srv.Close)

	body, status, _ := doRequest(t, srv, http.MethodPost,
		"/api/v1/sessions/tp-1/interface-transition", `{"targetMode":"chat","policy":"drain"}`)
	if status != http.StatusAccepted {
		t.Fatalf("start transition = %d, want 202; body=%s", status, body)
	}
	if svc.startedHistoryPolicy != domain.SessionInterfaceTransitionHistoryStrict {
		t.Fatalf("omitted history policy = %q, want strict", svc.startedHistoryPolicy)
	}
	var response controllers.StartSessionInterfaceTransitionResponse
	mustJSON(t, body, &response)
	if response.Transition.HistoryPolicy != domain.SessionInterfaceTransitionHistoryStrict {
		t.Fatalf("response history policy = %q, want strict", response.Transition.HistoryPolicy)
	}
}

// A phone polls only live workers and pages the archive when it is opened. The
// paging query reaches the service intact, and the page's cursor and the
// archive's size are on the wire only when there is something to report.
func TestSessionsAPI_ListPagesTheArchiveAndSizesIt(t *testing.T) {
	svc := newFakeSessionService()
	svc.listPage = &sessionsvc.ListPage{
		Sessions:   []domain.Session{svc.sessions["tp-1"]},
		NextCursor: "next-page",
		Archived:   &sessionsvc.ArchiveSummary{Total: 3, ByProject: map[domain.ProjectID]int{"tp": 2, "": 1}},
	}
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions?active=true&kind=worker&limit=50&cursor=abc", "")
	if status != http.StatusOK {
		t.Fatalf("GET sessions = %d, want 200; body=%s", status, body)
	}
	if f := svc.lastListFilter; f.Active == nil || !*f.Active || f.Kind != domain.KindWorker || f.Limit != 50 || f.Cursor != "abc" {
		t.Fatalf("filter = %+v, want active worker page of 50 after abc", f)
	}
	var page struct {
		Sessions   []sessionBody `json:"sessions"`
		NextCursor string        `json:"nextCursor"`
		Archived   *struct {
			Total     int            `json:"total"`
			ByProject map[string]int `json:"byProject"`
		} `json:"archived"`
	}
	mustJSON(t, body, &page)
	if len(page.Sessions) != 1 || page.NextCursor != "next-page" {
		t.Fatalf("page = %d sessions, cursor %q", len(page.Sessions), page.NextCursor)
	}
	if page.Archived == nil || page.Archived.Total != 3 || page.Archived.ByProject["tp"] != 2 || page.Archived.ByProject[""] != 1 {
		t.Fatalf("archived = %+v, want 3 (tallpine 2, standalone 1)", page.Archived)
	}

	svc.listPage = nil
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/sessions", "")
	if status != http.StatusOK {
		t.Fatalf("GET sessions = %d, want 200; body=%s", status, body)
	}
	if strings.Contains(string(body), "nextCursor") || strings.Contains(string(body), "\"archived\"") {
		t.Fatalf("unpaged list carries paging fields: %s", body)
	}
}

func TestSessionsAPI_ListRejectsBadPagingQuery(t *testing.T) {
	srv := newSessionTestServer(t, newFakeSessionService())
	for _, query := range []string{"kind=reviewer", "limit=0", "limit=201", "limit=ten", "cursor=abc"} {
		body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions?"+query, "")
		if status != http.StatusBadRequest || !strings.Contains(string(body), "INVALID_QUERY") {
			t.Fatalf("GET sessions?%s = %d %s, want 400 INVALID_QUERY", query, status, body)
		}
	}
}

func TestSessionsAPI_ListSpawnGetAndActions(t *testing.T) {
	svc := newFakeSessionService()
	s := svc.sessions["tp-1"]
	s.Metadata = domain.SessionMetadata{Branch: "qa/modal-worker", WorkspacePath: "/tmp/private-worktree", RuntimeHandleID: "runtime-1", Prompt: "private prompt"}
	s.SCMStatus = domain.StatusReviewPending
	s.KanbanColumn = domain.KanbanNeedsReview
	s.DisplayStatus = contract.DisplayNeedsHumanReview
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions?project=tp", "")
	if status != http.StatusOK {
		t.Fatalf("GET sessions = %d, want 200; body=%s", status, body)
	}
	var list struct {
		Sessions []sessionBody `json:"sessions"`
	}
	mustJSON(t, body, &list)
	if len(list.Sessions) != 1 || list.Sessions[0].ID != "tp-1" || list.Sessions[0].Status != string(domain.StatusIdle) || list.Sessions[0].SCMStatus != string(domain.StatusReviewPending) || list.Sessions[0].TerminalHandleID != "tp-1/terminal_0" {
		t.Fatalf("list = %#v", list)
	}
	if list.Sessions[0].KanbanColumn != string(domain.KanbanNeedsReview) {
		t.Fatalf("kanbanColumn = %q, want the derived column on the wire", list.Sessions[0].KanbanColumn)
	}
	if list.Sessions[0].DisplayStatus != string(contract.DisplayNeedsHumanReview) {
		t.Fatalf("displayStatus = %q, want the derived phrase on the wire", list.Sessions[0].DisplayStatus)
	}
	if list.Sessions[0].Branch != "qa/modal-worker" {
		t.Fatalf("branch = %q, want qa/modal-worker", list.Sessions[0].Branch)
	}
	var rawList struct {
		Sessions []map[string]any `json:"sessions"`
	}
	mustJSON(t, body, &rawList)
	if _, ok := rawList.Sessions[0]["metadata"]; ok {
		t.Fatalf("list leaked metadata: %s", body)
	}
	if _, ok := rawList.Sessions[0]["workspacePath"]; ok {
		t.Fatalf("list leaked workspacePath: %s", body)
	}
	if _, ok := rawList.Sessions[0]["prompt"]; ok {
		t.Fatalf("list leaked prompt: %s", body)
	}

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions", `{"projectId":"tp","issueId":"ISS-1","kind":"worker","harness":"codex","prompt":"fix","displayName":"my worker"}`)
	if status != http.StatusCreated {
		t.Fatalf("POST session = %d, want 201; body=%s", status, body)
	}
	var spawned struct {
		Session           sessionBody `json:"session"`
		PromptBytes       *int        `json:"promptBytes"`
		SystemPromptBytes *int        `json:"systemPromptBytes"`
	}
	mustJSON(t, body, &spawned)
	if spawned.Session.ID != "tp-2" || spawned.Session.IssueID != "ISS-1" || spawned.Session.Harness != "codex" {
		t.Fatalf("spawned = %#v", spawned)
	}
	if spawned.Session.DisplayName != "my worker" {
		t.Fatalf("spawned displayName = %q, want %q", spawned.Session.DisplayName, "my worker")
	}
	if spawned.PromptBytes == nil || *spawned.PromptBytes != len("fix") {
		t.Fatalf("spawned promptBytes = %v, want %d", spawned.PromptBytes, len("fix"))
	}
	if spawned.SystemPromptBytes == nil || *spawned.SystemPromptBytes != 0 {
		t.Fatalf("spawned systemPromptBytes = %v, want present zero", spawned.SystemPromptBytes)
	}

	body, status, _ = doRequest(t, srv, "GET", "/api/v1/sessions/tp-2", "")
	if status != http.StatusOK {
		t.Fatalf("GET session = %d, want 200; body=%s", status, body)
	}

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/tp-2/send", "{\"message\":\"con\\u0000tinue\"}")
	if status != http.StatusOK || svc.sent != "continue" {
		t.Fatalf("send status=%d sent=%q body=%s", status, svc.sent, body)
	}

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/tp-2/kill", "")
	if status != http.StatusOK {
		t.Fatalf("kill = %d, want 200; body=%s", status, body)
	}
	var killed struct {
		SessionID string `json:"sessionId"`
		Freed     bool   `json:"freed"`
	}
	mustJSON(t, body, &killed)
	if killed.SessionID != "tp-2" || !killed.Freed {
		t.Fatalf("kill response = %#v", killed)
	}

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/tp-2/restore", "")
	if status != http.StatusOK {
		t.Fatalf("restore = %d, want 200; body=%s", status, body)
	}
	var restored struct {
		SessionID   string `json:"sessionId"`
		RestoreMode string `json:"restoreMode"`
	}
	mustJSON(t, body, &restored)
	if restored.SessionID != "tp-2" || restored.RestoreMode != "native" {
		t.Fatalf("restore response = %#v", restored)
	}

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/tp-2/exit-agent", "")
	if status != http.StatusOK {
		t.Fatalf("exit agent = %d, want 200; body=%s", status, body)
	}
	var exited struct {
		SessionID string `json:"sessionId"`
		Session   struct {
			Activity domain.Activity `json:"activity"`
		} `json:"session"`
	}
	mustJSON(t, body, &exited)
	if exited.SessionID != "tp-2" || exited.Session.Activity.State != domain.ActivityExited {
		t.Fatalf("exit response = %#v", exited)
	}

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/tp-2/resume-agent", "")
	if status != http.StatusOK {
		t.Fatalf("resume agent = %d, want 200; body=%s", status, body)
	}
	var resumed struct {
		SessionID  string `json:"sessionId"`
		ResumeMode string `json:"resumeMode"`
	}
	mustJSON(t, body, &resumed)
	if resumed.SessionID != "tp-2" || resumed.ResumeMode != "native" {
		t.Fatalf("resume response = %#v", resumed)
	}

	body, status, _ = doRequest(t, srv, "PATCH", "/api/v1/sessions/tp-2", `{"displayName":"Renamed"}`)
	if status != http.StatusOK {
		t.Fatalf("rename = %d, want 200; body=%s", status, body)
	}
	var renamed struct {
		OK          bool   `json:"ok"`
		SessionID   string `json:"sessionId"`
		DisplayName string `json:"displayName"`
	}
	mustJSON(t, body, &renamed)
	if !renamed.OK || renamed.SessionID != "tp-2" || renamed.DisplayName != "Renamed" {
		t.Fatalf("rename response = %#v", renamed)
	}
	if svc.sessions["tp-2"].DisplayName != "Renamed" {
		t.Fatalf("session displayName not updated: %+v", svc.sessions["tp-2"])
	}

	body, status, _ = doRequest(t, srv, "PATCH", "/api/v1/sessions/tp-2/merge-policy", `{"terminateOnPrMerge":true}`)
	if status != http.StatusOK {
		t.Fatalf("merge policy = %d, want 200; body=%s", status, body)
	}
	var policy struct {
		OK                 bool   `json:"ok"`
		SessionID          string `json:"sessionId"`
		TerminateOnPRMerge bool   `json:"terminateOnPrMerge"`
	}
	mustJSON(t, body, &policy)
	if !policy.OK || policy.SessionID != "tp-2" || !policy.TerminateOnPRMerge {
		t.Fatalf("merge policy response = %#v", policy)
	}
	if !svc.sessions["tp-2"].TerminateOnPRMerge {
		t.Fatalf("session merge policy not updated: %+v", svc.sessions["tp-2"])
	}

	body, status, _ = doRequest(t, srv, "PUT", "/api/v1/sessions/tp-2/auto-review", `{"enabled":true}`)
	if status != http.StatusOK {
		t.Fatalf("auto review = %d, want 200; body=%s", status, body)
	}
	var autoReview struct {
		Session domain.Session `json:"session"`
	}
	mustJSON(t, body, &autoReview)
	if !autoReview.Session.AutoReviewEnabled || !svc.sessions["tp-2"].AutoReviewEnabled {
		t.Fatalf("auto review response=%+v stored=%+v", autoReview, svc.sessions["tp-2"])
	}

	body, status, _ = doRequest(t, srv, "PUT", "/api/v1/sessions/tp-2/auto-review", `{}`)
	if status != http.StatusBadRequest || !strings.Contains(string(body), "AUTO_REVIEW_ENABLED_REQUIRED") {
		t.Fatalf("missing auto review enabled = %d, want 400; body=%s", status, body)
	}
	if !svc.sessions["tp-2"].AutoReviewEnabled {
		t.Fatal("malformed auto review request changed persisted state")
	}

	body, status, _ = doRequest(t, srv, "PATCH", "/api/v1/sessions/tp-2/auto-inject-review", `{"autoInjectReview":false}`)
	if status != http.StatusOK {
		t.Fatalf("auto-inject review policy = %d, want 200; body=%s", status, body)
	}
	var autoInjectPolicy struct {
		OK               bool   `json:"ok"`
		SessionID        string `json:"sessionId"`
		AutoInjectReview bool   `json:"autoInjectReview"`
	}
	mustJSON(t, body, &autoInjectPolicy)
	if !autoInjectPolicy.OK || autoInjectPolicy.SessionID != "tp-2" || autoInjectPolicy.AutoInjectReview {
		t.Fatalf("auto-inject review policy response = %#v", autoInjectPolicy)
	}
	if svc.sessions["tp-2"].AutoInjectReview {
		t.Fatalf("session auto-inject review policy not updated: %+v", svc.sessions["tp-2"])
	}

	body, status, _ = doRequest(t, srv, "PATCH", "/api/v1/sessions/tp-2/auto-inject-ci", `{"autoInjectCI":false}`)
	if status != http.StatusOK {
		t.Fatalf("auto-inject CI policy = %d, want 200; body=%s", status, body)
	}
	var ciPolicy struct {
		OK           bool   `json:"ok"`
		SessionID    string `json:"sessionId"`
		AutoInjectCI bool   `json:"autoInjectCI"`
	}
	mustJSON(t, body, &ciPolicy)
	if !ciPolicy.OK || ciPolicy.SessionID != "tp-2" || ciPolicy.AutoInjectCI {
		t.Fatalf("auto-inject CI policy response = %#v", ciPolicy)
	}
	if svc.autoInjectCISession != "tp-2" || svc.autoInjectCIEnabled || svc.sessions["tp-2"].AutoInjectCI {
		t.Fatalf("auto-inject CI service input = session:%q enabled:%v", svc.autoInjectCISession, svc.autoInjectCIEnabled)
	}

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/tp-2/pin", "")
	if status != http.StatusOK {
		t.Fatalf("pin = %d, want 200; body=%s", status, body)
	}
	var pinned struct {
		Session struct {
			IsPinned bool `json:"isPinned"`
		} `json:"session"`
	}
	mustJSON(t, body, &pinned)
	if !pinned.Session.IsPinned {
		t.Fatalf("pin response = %#v", pinned)
	}
	if !svc.sessions["tp-2"].IsPinned {
		t.Fatalf("session pin not updated: %+v", svc.sessions["tp-2"])
	}

	body, status, _ = doRequest(t, srv, "DELETE", "/api/v1/sessions/tp-2/pin", "")
	if status != http.StatusOK {
		t.Fatalf("unpin = %d, want 200; body=%s", status, body)
	}
	var unpinned struct {
		Session struct {
			IsPinned bool `json:"isPinned"`
		} `json:"session"`
	}
	mustJSON(t, body, &unpinned)
	if unpinned.Session.IsPinned {
		t.Fatalf("unpin response = %#v", unpinned)
	}
	if svc.sessions["tp-2"].IsPinned {
		t.Fatalf("session unpin not updated: %+v", svc.sessions["tp-2"])
	}

	_, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/ghost-1/pin", "")
	if status != http.StatusNotFound {
		t.Fatalf("pin unknown = %d, want 404", status)
	}

	_, status, _ = doRequest(t, srv, "DELETE", "/api/v1/sessions/ghost-1/pin", "")
	if status != http.StatusNotFound {
		t.Fatalf("unpin unknown = %d, want 404", status)
	}

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/orchestrators", `{"projectId":"tp"}`)
	if status != http.StatusCreated {
		t.Fatalf("orchestrator = %d, want 201; body=%s", status, body)
	}
}

// A list row carries no artifact files (the service skips the directory walk)
// but still says whether the session has artifacts, from its output type.
func TestSessionsAPI_ListReportsHasArtifactsWithoutFiles(t *testing.T) {
	svc := newFakeSessionService()
	s := svc.sessions["tp-1"]
	s.OutputType = domain.SessionOutputPRAndArtifact
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions", "")
	if status != http.StatusOK {
		t.Fatalf("GET sessions = %d, want 200; body=%s", status, body)
	}
	var resp struct {
		Sessions []map[string]any `json:"sessions"`
	}
	mustJSON(t, body, &resp)
	var found bool
	for _, row := range resp.Sessions {
		if row["id"] != "tp-1" {
			continue
		}
		found = true
		if row["hasArtifacts"] != true {
			t.Errorf("hasArtifacts = %v, want true for a pr_artifact session", row["hasArtifacts"])
		}
		if _, present := row["artifactFiles"]; present {
			t.Errorf("list row carries artifactFiles, want it omitted: %v", row["artifactFiles"])
		}
	}
	if !found {
		t.Fatalf("tp-1 missing from list: %s", body)
	}
}

func TestSessionsAPI_GetExposesArtifactFilesAndServesHTMLArtifact(t *testing.T) {
	artifactDir := t.TempDir()
	artifactPath := filepath.Join(artifactDir, "site", "index.html")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatalf("mkdir artifact dir: %v", err)
	}
	if err := os.WriteFile(artifactPath, []byte("<html><body>artifact preview</body></html>"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}

	// A real workspace file at the same literal path the legacy
	// __ao_artifacts__/ marker scheme would have used. The advertised
	// previewUrl must still resolve to the artifact's own content: it now
	// carries scope in its host, not in a path prefix a workspace file could
	// also contain, so this collision cannot affect it.
	workspace := t.TempDir()
	collidingPath := filepath.Join(workspace, "__ao_artifacts__", "site", "index.html")
	if err := os.MkdirAll(filepath.Dir(collidingPath), 0o755); err != nil {
		t.Fatalf("mkdir workspace collision dir: %v", err)
	}
	if err := os.WriteFile(collidingPath, []byte("workspace collision content"), 0o644); err != nil {
		t.Fatalf("write colliding workspace file: %v", err)
	}

	svc := newFakeSessionService()
	s := svc.sessions["tp-1"]
	s.OutputType = domain.SessionOutputArtifact
	s.Metadata.ArtifactDir = artifactDir
	s.Metadata.WorkspacePath = workspace
	s.ArtifactFiles = []domain.SessionArtifactFile{
		{
			Path:      "site/index.html",
			Name:      "index.html",
			Kind:      domain.SessionArtifactHTML,
			Size:      42,
			UpdatedAt: time.Now().UTC().Truncate(time.Second),
		},
		{
			Path:      "notes/readme.md",
			Name:      "readme.md",
			Kind:      domain.SessionArtifactMarkdown,
			Size:      12,
			UpdatedAt: time.Now().UTC().Truncate(time.Second),
		},
	}
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1", "")
	if status != http.StatusOK {
		t.Fatalf("GET session = %d, want 200; body=%s", status, body)
	}
	var resp struct {
		Session struct {
			ID            string `json:"id"`
			OutputType    string `json:"outputType"`
			HasArtifacts  bool   `json:"hasArtifacts"`
			ArtifactFiles []struct {
				Path       string `json:"path"`
				Kind       string `json:"kind"`
				PreviewURL string `json:"previewUrl"`
				InlineURL  string `json:"inlineUrl"`
			} `json:"artifactFiles"`
		} `json:"session"`
	}
	mustJSON(t, body, &resp)
	if !resp.Session.HasArtifacts {
		t.Fatalf("hasArtifacts = false, want true for a session listing artifact files")
	}
	if resp.Session.OutputType != string(domain.SessionOutputArtifact) {
		t.Fatalf("outputType = %q, want %q", resp.Session.OutputType, domain.SessionOutputArtifact)
	}
	if len(resp.Session.ArtifactFiles) != 2 {
		t.Fatalf("artifactFiles = %+v, want 2", resp.Session.ArtifactFiles)
	}
	if resp.Session.ArtifactFiles[0].Path != "site/index.html" || resp.Session.ArtifactFiles[0].Kind != "html" {
		t.Fatalf("html artifact = %+v", resp.Session.ArtifactFiles[0])
	}
	if !strings.Contains(resp.Session.ArtifactFiles[0].PreviewURL, "ao-preview-artifact.") ||
		!strings.HasSuffix(resp.Session.ArtifactFiles[0].PreviewURL, "/site/index.html") {
		t.Fatalf("html previewUrl = %q, want an ao-preview-artifact origin with no path marker", resp.Session.ArtifactFiles[0].PreviewURL)
	}
	if resp.Session.ArtifactFiles[1].PreviewURL != "" {
		t.Fatalf("markdown previewUrl = %q, want empty", resp.Session.ArtifactFiles[1].PreviewURL)
	}
	// The chat frames the page from its own inline origin, not the preview one.
	if inline := resp.Session.ArtifactFiles[0].InlineURL; !strings.Contains(inline, "://ao-inline-artifact.") || !strings.HasSuffix(inline, "/site/index.html") {
		t.Fatalf("html inlineUrl = %q, want an ao-inline-artifact origin", inline)
	}
	if resp.Session.ArtifactFiles[1].InlineURL != "" {
		t.Fatalf("markdown inlineUrl = %q, want empty", resp.Session.ArtifactFiles[1].InlineURL)
	}

	directPreviewPath, err := url.Parse(resp.Session.ArtifactFiles[0].PreviewURL)
	if err != nil {
		t.Fatalf("parse html artifact previewUrl: %v", err)
	}
	directBody, directStatus, _ := doPreviewOriginRequest(t, srv, resp.Session.ArtifactFiles[0].PreviewURL, directPreviewPath.EscapedPath())
	if directStatus != http.StatusOK {
		t.Fatalf("GET direct artifact preview path = %d, want 200; body=%s", directStatus, directBody)
	}
	if !bytes.Contains(directBody, []byte("artifact preview")) {
		t.Fatalf("direct artifact body = %q, want html artifact content, not the colliding workspace file's", directBody)
	}

	remoteBody, remoteStatus, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/preview/files/site/index.html?source=artifact", "")
	if remoteStatus != http.StatusOK || !bytes.Contains(remoteBody, []byte("artifact preview")) {
		t.Fatalf("GET artifact through API = %d, body=%q", remoteStatus, remoteBody)
	}
	workspaceBody, workspaceStatus, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/preview/files/site/index.html", "")
	if workspaceStatus != http.StatusNotFound {
		t.Fatalf("GET default workspace root = %d, body=%q", workspaceStatus, workspaceBody)
	}

	// The colliding workspace file, addressed on the ordinary (non-artifact)
	// preview origin at its own literal path, is unaffected and still opens
	// its own real content.
	workspacePreviewURL, err := previewutil.FileURL(srv.URL, "tp-1", "__ao_artifacts__/site/index.html")
	if err != nil {
		t.Fatalf("build workspace preview URL: %v", err)
	}
	collisionBody, collisionStatus, _ := doPreviewOriginRequest(t, srv, workspacePreviewURL, "/__ao_artifacts__/site/index.html")
	if collisionStatus != http.StatusOK {
		t.Fatalf("GET colliding workspace file = %d, want 200; body=%s", collisionStatus, collisionBody)
	}
	if !bytes.Contains(collisionBody, []byte("workspace collision content")) {
		t.Fatalf("colliding workspace body = %q, want the workspace file's own content", collisionBody)
	}
}

func TestSessionsAPI_RenameNotFound(t *testing.T) {
	srv := newSessionTestServer(t, newFakeSessionService())

	body, status, _ := doRequest(t, srv, "PATCH", "/api/v1/sessions/missing-1", `{"displayName":"Renamed"}`)
	assertErrorCode(t, body, status, http.StatusNotFound, "SESSION_NOT_FOUND")
}

func TestSessionsAPI_RenameValidation(t *testing.T) {
	srv := newSessionTestServer(t, newFakeSessionService())

	exact := strings.Repeat("x", 100)
	body, status, _ := doRequest(t, srv, "PATCH", "/api/v1/sessions/tp-1", `{"displayName":"`+exact+`"}`)
	if status != http.StatusOK {
		t.Fatalf("rename 100-char displayName = %d, want 200; body=%s", status, body)
	}

	body, status, _ = doRequest(t, srv, "PATCH", "/api/v1/sessions/tp-1", `{"displayName":"  "}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "DISPLAY_NAME_REQUIRED")

	overlong := strings.Repeat("x", 101)
	body, status, _ = doRequest(t, srv, "PATCH", "/api/v1/sessions/tp-1", `{"displayName":"`+overlong+`"}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "DISPLAY_NAME_TOO_LONG")

	body, status, _ = doRequest(t, srv, "PATCH", "/api/v1/sessions/tp-1", `{`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
}

func TestSessionsAPI_ListOrchestratorsOnly(t *testing.T) {
	svc := newFakeSessionService()
	now := time.Now().UTC()
	svc.sessions["tp-orch"] = domain.Session{
		SessionRecord: domain.SessionRecord{
			ID:        "tp-orch",
			ProjectID: "tp",
			Kind:      domain.KindOrchestrator,
			Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
			CreatedAt: now,
			UpdatedAt: now,
		},
		Status: domain.StatusIdle,
	}
	svc.sessions["other-orch"] = domain.Session{
		SessionRecord: domain.SessionRecord{
			ID:        "other-orch",
			ProjectID: "other",
			Kind:      domain.KindOrchestrator,
			Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
			CreatedAt: now,
			UpdatedAt: now,
		},
		Status: domain.StatusIdle,
	}
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/orchestrators", "")
	if status != http.StatusOK {
		t.Fatalf("GET orchestrators = %d, want 200; body=%s", status, body)
	}
	var list struct {
		Sessions []sessionBody `json:"sessions"`
	}
	mustJSON(t, body, &list)
	if len(list.Sessions) != 2 {
		t.Fatalf("len(orchestrators) = %d, want 2; body=%s", len(list.Sessions), body)
	}
	got := map[string]string{}
	for _, sess := range list.Sessions {
		got[sess.ID] = sess.Kind
	}
	if got["tp-orch"] != string(domain.KindOrchestrator) || got["other-orch"] != string(domain.KindOrchestrator) {
		t.Fatalf("missing orchestrators: %#v", got)
	}
	if _, ok := got["tp-1"]; ok {
		t.Fatalf("worker session leaked into orchestrator list: %#v", got)
	}
}
