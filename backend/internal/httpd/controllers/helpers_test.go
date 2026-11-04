package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
	previewutil "tallpine.eu/tallpine/backend/internal/preview"
	"tallpine.eu/tallpine/backend/internal/previewserver"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

type fakeSessionService struct {
	// lastListFilter is the filter the controller parsed; listPage, when set,
	// replaces the page built from sessions so a test can pin the wire shape.
	lastListFilter             sessionsvc.ListFilter
	listPage                   *sessionsvc.ListPage
	workspaceSnapshotInput     sessionsvc.WorkspaceSelectionRevisionInput
	workspaceSnapshotFilter    string
	cleanupPending             bool
	artifactsDeleted           []domain.SessionID
	deleteArtifactsErr         error
	sessions                   map[domain.SessionID]domain.Session
	sent                       string
	sentAttachment             *ports.SpawnAttachment
	sentDeliveryOptions        ports.MessageDeliveryOptions
	sendReceipt                ports.MessageReceipt
	sendErr                    error
	delegationInput            sessionsvc.DelegateTaskInput
	delegationErr              error
	preparedProject            domain.ProjectID
	canceledPreparation        string
	cleanupProjects            []domain.ProjectID
	cleanupResult              []domain.SessionID
	cleanupSkipped             []sessionsvc.CleanupSkipped
	workspaceFiles             sessionsvc.WorkspaceFiles
	workspaceListHook          func(context.Context) error
	workspaceFile              sessionsvc.WorkspaceFileDetail
	workspaceFileSection       sessionsvc.WorkspaceFileSection
	workspaceFileCommitSHA     string
	workspaceFileUpdate        sessionsvc.UpdateWorkspaceFileInput
	workspaceBlob              sessionsvc.WorkspaceFileBlob
	workspaceDiffs             sessionsvc.WorkspaceDiffs
	workspaceDiffInput         sessionsvc.WorkspaceDiffInput
	workspaceRevision          sessionsvc.WorkspaceFileRevision
	workspaceRevisionPath      string
	workspaceRevisionScope     sessionsvc.WorkspaceDiffScope
	workspaceRevisionSide      sessionsvc.WorkspaceFileBlobSide
	workspaceRevisionVersion   string
	workspaceExpectedRevision  string
	workspaceSelectionRevision sessionsvc.WorkspaceSelectionRevisionInput
	workspaceSelection         sessionsvc.WorkspaceCommitSelection
	workspaceSelectionView     sessionsvc.WorkspaceView
	workspaceSelectionSHAs     []string
	workspaceSelectionVersion  string
	prFiles                    sessionsvc.PRFiles
	prFileCommitSHA            string
	prRevisionCommitSHA        string
	workspaceSearch            sessionsvc.WorkspaceFileSearch
	workspaceSearchQuery       string
	workspaceSearchCursor      string
	workspaceSearchLimit       int
	workspaceResolvePath       string
	workspaceResolution        sessionsvc.WorkspacePathResolution
	workspaceTree              sessionsvc.WorkspaceTree
	workspaceTreePath          string
	workspacePaths             []string
	workspaceReconciles        int
	workspaceHistoryView       sessionsvc.WorkspaceView
	workspaceViewHistory       *sessionsvc.WorkspaceHistory
	workspaceBranches          sessionsvc.WorkspaceBranches
	spawnErr                   error
	lastSpawn                  ports.SpawnConfig
	orchestratorLane           string
	orchestratorBrief          string
	orchestratorBriefCalls     int
	orchestratorBriefCreated   bool
	orchestratorMode           domain.SessionMode
	orchestratorApproval       domain.PermissionMode
	handoffID                  domain.SessionID
	endLaneID                  domain.SessionID
	laneOrchestrator           domain.Session
	handoffNote                string
	claimErr                   error
	lastClaimOptions           sessionsvc.ClaimPROptions
	listPRErr                  error
	linkedPRs                  []domain.ChangeRequestReference
	workspaceErr               error
	staged                     []ports.SpawnAttachment
	stagedPaths                []string
	stageErr                   error
	autoInjectCISession        domain.SessionID
	autoInjectCIEnabled        bool
}

type fakeInterfaceTransitionSessionService struct {
	*fakeSessionService
	transition             domain.SessionInterfaceTransition
	acknowledgedSessionID  domain.SessionID
	acknowledgedTransition string
	startedSessionID       domain.SessionID
	startedTarget          domain.SessionMode
	startedPolicy          domain.SessionInterfaceTransitionPolicy
	startedHistoryPolicy   domain.SessionInterfaceTransitionHistoryPolicy
}

func (f *fakeInterfaceTransitionSessionService) InterfaceTransitionStatus(
	context.Context,
	domain.SessionID,
) (sessionsvc.InterfaceTransitionStatus, error) {
	return sessionsvc.InterfaceTransitionStatus{Supported: true, Transition: &f.transition}, nil
}

func (f *fakeInterfaceTransitionSessionService) StartInterfaceTransition(
	_ context.Context,
	sessionID domain.SessionID,
	target domain.SessionMode,
	policy domain.SessionInterfaceTransitionPolicy,
	historyPolicy domain.SessionInterfaceTransitionHistoryPolicy,
) (domain.SessionInterfaceTransition, error) {
	f.startedSessionID = sessionID
	f.startedTarget = target
	f.startedPolicy = policy
	f.startedHistoryPolicy = historyPolicy
	return f.transition, nil
}

func (f *fakeInterfaceTransitionSessionService) CancelInterfaceTransition(
	context.Context,
	domain.SessionID,
) error {
	return nil
}

func (f *fakeInterfaceTransitionSessionService) AcknowledgeInterfaceTransitionNotice(
	_ context.Context,
	sessionID domain.SessionID,
	transitionID string,
) (domain.SessionInterfaceTransition, error) {
	f.acknowledgedSessionID = sessionID
	f.acknowledgedTransition = transitionID
	return f.transition, nil
}

type fakeManagedPreviewServer struct {
	status         previewserver.Status
	startEnv       map[string]string
	startErr       error
	startName      string
	startWorkspace string
	stopCalls      int
	onStop         func()
}

type allowSessionCapability struct{}

func (allowSessionCapability) Valid(domain.SessionID, string, string) bool { return true }

type denySessionCapability struct{}

func (denySessionCapability) Valid(domain.SessionID, string, string) bool { return false }

type fixedShellPreviewCapability struct {
	token   string
	revoked bool
}

func (f *fixedShellPreviewCapability) ValidPreviewCapability(_ context.Context, id domain.SessionID, token string) (bool, error) {
	return !f.revoked && id == "tp-1" && token == f.token, nil
}

func (f *fakeManagedPreviewServer) Start(
	_ context.Context,
	sessionID domain.SessionID,
	workspacePath string,
	configurationName string,
	projectEnv map[string]string,
) (previewserver.Status, error) {
	f.startName = configurationName
	f.startWorkspace = workspacePath
	f.startEnv = projectEnv
	if f.startErr != nil {
		return previewserver.Status{}, f.startErr
	}
	f.status.SessionID = sessionID
	return f.status, nil
}

type previewProjectManager struct {
	projectsvc.Manager
	env map[string]string
}

func (m previewProjectManager) Get(_ context.Context, id domain.ProjectID) (projectsvc.GetResult, error) {
	return projectsvc.GetResult{Status: "ok", Project: &projectsvc.Project{ID: id, Config: &domain.ProjectConfig{Env: m.env}}}, nil
}

func (f *fakeManagedPreviewServer) Stop(
	_ context.Context,
	sessionID domain.SessionID,
) (previewserver.Status, error) {
	f.stopCalls++
	if f.onStop != nil {
		f.onStop()
	}
	f.status.SessionID = sessionID
	f.status.State = previewserver.StateStopped
	return f.status, nil
}

func (f *fakeManagedPreviewServer) Status(sessionID domain.SessionID) previewserver.Status {
	status := f.status
	status.SessionID = sessionID
	if status.State == "" {
		status.State = previewserver.StateStopped
	}
	if status.Logs == nil {
		status.Logs = []string{}
	}
	return status
}

func newFakeSessionService() *fakeSessionService {
	now := time.Now().UTC()
	s := domain.Session{SessionRecord: domain.SessionRecord{ID: "tp-1", ProjectID: "tp", Kind: domain.KindWorker, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}, AutoInjectReview: true, AutoInjectCI: true, CreatedAt: now, UpdatedAt: now}, Status: domain.StatusIdle, TerminalHandleID: "tp-1/terminal_0"}
	return &fakeSessionService{
		sessions: map[domain.SessionID]domain.Session{s.ID: s},
	}
}

func (f *fakeSessionService) List(_ context.Context, filter sessionsvc.ListFilter) ([]domain.Session, error) {
	var out []domain.Session
	for _, s := range f.sessions {
		if filter.ProjectID != "" && s.ProjectID != filter.ProjectID {
			continue
		}
		if filter.Active != nil && s.IsTerminated == *filter.Active {
			continue
		}
		if filter.OrchestratorOnly && s.Kind != domain.KindOrchestrator {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

func (f *fakeSessionService) ListPage(ctx context.Context, filter sessionsvc.ListFilter) (sessionsvc.ListPage, error) {
	f.lastListFilter = filter
	if f.listPage != nil {
		return *f.listPage, nil
	}
	sessions, err := f.List(ctx, filter)
	return sessionsvc.ListPage{Sessions: sessions}, err
}

func (f *fakeSessionService) Spawn(_ context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error) {
	f.lastSpawn = cfg
	if f.spawnErr != nil {
		return domain.Session{}, 0, 0, f.spawnErr
	}
	now := time.Now().UTC()
	s := domain.Session{SessionRecord: domain.SessionRecord{ID: domain.SessionID(string(cfg.ProjectID) + "-2"), ProjectID: cfg.ProjectID, IssueID: cfg.IssueID, Kind: cfg.Kind, Harness: cfg.Harness, DisplayName: cfg.DisplayName, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}, AutoInjectReview: true, AutoInjectCI: true, CreatedAt: now, UpdatedAt: now}, Status: domain.StatusIdle}
	f.sessions[s.ID] = s
	return s, len(cfg.Prompt), 0, nil
}

func (f *fakeSessionService) SpawnLaneOrchestrator(ctx context.Context, projectID domain.ProjectID, clean bool, requestedMode domain.SessionMode, approval domain.PermissionMode, laneItemID string) (domain.Session, error) {
	f.orchestratorLane = laneItemID
	return f.SpawnOrchestrator(ctx, projectID, clean, requestedMode, approval)
}

func (f *fakeSessionService) SpawnLaneOrchestratorBrief(ctx context.Context, projectID domain.ProjectID, clean bool, requestedMode domain.SessionMode, approval domain.PermissionMode, laneItemID, prompt string) (domain.Session, bool, error) {
	f.orchestratorBrief = prompt
	f.orchestratorBriefCalls++
	sess, err := f.SpawnLaneOrchestrator(ctx, projectID, clean, requestedMode, approval, laneItemID)
	return sess, f.orchestratorBriefCreated, err
}

func (f *fakeSessionService) SpawnOrchestrator(ctx context.Context, projectID domain.ProjectID, clean bool, requestedMode domain.SessionMode, approval domain.PermissionMode) (domain.Session, error) {
	f.orchestratorMode = requestedMode
	f.orchestratorApproval = approval
	if clean {
		active := true
		existing, err := f.List(ctx, sessionsvc.ListFilter{ProjectID: projectID, Active: &active, OrchestratorOnly: true})
		if err != nil {
			return domain.Session{}, err
		}
		for _, o := range existing {
			if _, err := f.Kill(ctx, o.ID); err != nil {
				return domain.Session{}, err
			}
		}
	}
	s, _, _, err := f.Spawn(ctx, ports.SpawnConfig{ProjectID: projectID, Kind: domain.KindOrchestrator, RequestedMode: requestedMode})
	return s, err
}

func (f *fakeSessionService) Get(_ context.Context, id domain.SessionID) (domain.Session, error) {
	s, ok := f.sessions[id]
	if !ok {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return s, nil
}

func (f *fakeSessionService) GetRecord(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	s, err := f.Get(ctx, id)
	return s.SessionRecord, err
}

func (f *fakeSessionService) SetPreview(_ context.Context, id domain.SessionID, previewURL string) (domain.Session, error) {
	s, ok := f.sessions[id]
	if !ok {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	s.Metadata.PreviewURL = previewURL
	// Mirror the store: every set bumps the revision, even when the URL is
	// unchanged, so the controller's refresh contract can be exercised here.
	s.Metadata.PreviewRevision++
	f.sessions[id] = s
	return s, nil
}

func (f *fakeSessionService) SetTerminateOnPRMerge(_ context.Context, id domain.SessionID, terminate bool) (domain.Session, error) {
	s, ok := f.sessions[id]
	if !ok {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	s.TerminateOnPRMerge = terminate
	f.sessions[id] = s
	return s, nil
}

func (f *fakeSessionService) SetAutoInjectReview(_ context.Context, id domain.SessionID, autoInject bool) (domain.Session, error) {
	s, ok := f.sessions[id]
	if !ok {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	s.AutoInjectReview = autoInject
	f.sessions[id] = s
	return s, nil
}

func (f *fakeSessionService) SetAutoInjectCI(_ context.Context, id domain.SessionID, autoInject bool) (domain.Session, error) {
	s, ok := f.sessions[id]
	if !ok {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	f.autoInjectCISession = id
	f.autoInjectCIEnabled = autoInject
	s.AutoInjectCI = autoInject
	f.sessions[id] = s
	return s, nil
}

func (f *fakeSessionService) Pin(_ context.Context, id domain.SessionID) (domain.Session, error) {
	s, ok := f.sessions[id]
	if !ok {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	s.IsPinned = true
	now := time.Now().UTC()
	s.PinnedAt = &now
	f.sessions[id] = s
	return s, nil
}

func (f *fakeSessionService) Unpin(_ context.Context, id domain.SessionID) (domain.Session, error) {
	s, ok := f.sessions[id]
	if !ok {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	s.IsPinned = false
	s.PinnedAt = nil
	f.sessions[id] = s
	return s, nil
}

func (f *fakeSessionService) SetReviewerHarness(_ context.Context, id domain.SessionID, harness domain.ReviewerHarness, config domain.AgentConfig) (domain.Session, error) {
	s, ok := f.sessions[id]
	if !ok {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	s.ReviewerHarness = harness
	s.ReviewerConfig = config
	f.sessions[id] = s
	return s, nil
}

func (f *fakeSessionService) SetAutoReview(_ context.Context, id domain.SessionID, enabled bool) (domain.Session, error) {
	s, ok := f.sessions[id]
	if !ok {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	s.AutoReviewEnabled = enabled
	f.sessions[id] = s
	return s, nil
}

func (f *fakeSessionService) Restore(_ context.Context, id domain.SessionID) (sessionsvc.RestoreOutcome, error) {
	s := f.sessions[id]
	s.IsTerminated = false
	s.Status = domain.StatusIdle
	f.sessions[id] = s
	return sessionsvc.RestoreOutcome{Session: s, Mode: sessionsvc.RestoreModeView("native")}, nil
}

func (f *fakeSessionService) ExitAgent(_ context.Context, id domain.SessionID) (sessionsvc.ExitAgentOutcome, error) {
	s := f.sessions[id]
	s.Activity.State = domain.ActivityExited
	s.Status = domain.StatusExited
	f.sessions[id] = s
	return sessionsvc.ExitAgentOutcome{Session: s}, nil
}

func (f *fakeSessionService) ResumeAgent(_ context.Context, id domain.SessionID) (sessionsvc.ResumeAgentOutcome, error) {
	s := f.sessions[id]
	s.Activity.State = domain.ActivityIdle
	s.Status = domain.StatusIdle
	f.sessions[id] = s
	return sessionsvc.ResumeAgentOutcome{Session: s, Mode: sessionsvc.RestoreModeViewNative}, nil
}

func (f *fakeSessionService) Kill(_ context.Context, id domain.SessionID) (bool, error) {
	s := f.sessions[id]
	s.IsTerminated = true
	s.Status = domain.StatusTerminated
	f.sessions[id] = s
	return true, nil
}

func (f *fakeSessionService) DeleteArtifacts(_ context.Context, id domain.SessionID) error {
	if f.deleteArtifactsErr != nil {
		return f.deleteArtifactsErr
	}
	f.artifactsDeleted = append(f.artifactsDeleted, id)
	return nil
}

func (f *fakeSessionService) RollbackSpawn(_ context.Context, id domain.SessionID) (sessionsvc.RollbackOutcome, error) {
	if _, ok := f.sessions[id]; ok {
		delete(f.sessions, id)
		return sessionsvc.RollbackOutcome{Deleted: true}, nil
	}
	return sessionsvc.RollbackOutcome{}, nil
}

func (f *fakeSessionService) Cleanup(_ context.Context, project domain.ProjectID) (sessionsvc.CleanupOutcome, error) {
	f.cleanupProjects = append(f.cleanupProjects, project)
	cleaned := f.cleanupResult
	if cleaned == nil {
		cleaned = []domain.SessionID{"tp-1"}
	}
	return sessionsvc.CleanupOutcome{Cleaned: cleaned, Skipped: f.cleanupSkipped}, nil
}

func (f *fakeSessionService) Rename(_ context.Context, id domain.SessionID, displayName string) error {
	s, ok := f.sessions[id]
	if !ok {
		return apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	s.DisplayName = displayName
	f.sessions[id] = s
	return nil
}

func (f *fakeSessionService) Send(_ context.Context, _ domain.SessionID, message string, attachment *ports.SpawnAttachment) error {
	f.sent = message
	f.sentAttachment = attachment
	return nil
}

func (f *fakeSessionService) SendWithOptions(_ context.Context, _ domain.SessionID, message string, attachment *ports.SpawnAttachment, options ports.MessageDeliveryOptions) error {
	f.sent = message
	f.sentAttachment = attachment
	f.sentDeliveryOptions = options
	return nil
}

func (f *fakeSessionService) SendWithReceipt(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment, options ports.MessageDeliveryOptions) (ports.MessageReceipt, error) {
	if f.sendErr != nil {
		return ports.MessageReceipt{}, f.sendErr
	}
	return f.sendReceipt, f.SendWithOptions(ctx, id, message, attachment, options)
}

func (f *fakeSessionService) DelegateTask(_ context.Context, in sessionsvc.DelegateTaskInput) (sessionsvc.DelegateTaskOutcome, error) {
	f.delegationInput = in
	if f.delegationErr != nil {
		return sessionsvc.DelegateTaskOutcome{}, f.delegationErr
	}
	return sessionsvc.DelegateTaskOutcome{WorkerID: "tallpine-worker", OrchestratorID: "tp-orch"}, nil
}

func (f *fakeSessionService) PrepareTask(_ context.Context, projectID domain.ProjectID) (string, error) {
	f.preparedProject = projectID
	return "prep-token", nil
}

func (f *fakeSessionService) CancelTaskPreparation(_ context.Context, token string) error {
	f.canceledPreparation = token
	return nil
}

func (f *fakeSessionService) ListPRs(_ context.Context, id domain.SessionID) ([]domain.PRFacts, error) {
	if f.listPRErr != nil {
		return nil, f.listPRErr
	}
	if _, ok := f.sessions[id]; !ok {
		return nil, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return []domain.PRFacts{{URL: "https://github.com/justvibedev/tallpine-dev/pull/142", Number: 142, CI: domain.CIPassing, Review: domain.ReviewRequired, Mergeability: domain.MergeMergeable, UpdatedAt: time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)}}, nil
}

func (f *fakeSessionService) ListPRSummaries(_ context.Context, id domain.SessionID) ([]sessionsvc.PRSummary, error) {
	if f.listPRErr != nil {
		return nil, f.listPRErr
	}
	if _, ok := f.sessions[id]; !ok {
		return nil, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return []sessionsvc.PRSummary{{
		URL:          "https://github.com/justvibedev/tallpine-dev/pull/142",
		HTMLURL:      "https://github.com/justvibedev/tallpine-dev/pull/142",
		Number:       142,
		Title:        "Wire SCM summaries",
		State:        domain.PRStateOpen,
		Provider:     "github",
		Repo:         "justvibedev/tallpine-dev",
		Author:       "ada",
		SourceBranch: "codex/scm-observer-v1",
		TargetBranch: "main",
		HeadSHA:      "abc123",
		CI: sessionsvc.PRCISummary{State: domain.CIFailing, FailingChecks: []sessionsvc.PRFailingCheck{{
			Name:       "unit",
			Status:     domain.PRCheckFailed,
			Conclusion: "failure",
			URL:        "https://github.com/justvibedev/tallpine-dev/actions/runs/1",
		}}},
		Review: sessionsvc.PRReviewSummary{
			Decision:                   domain.ReviewChangesRequest,
			HasUnresolvedHumanComments: true,
			UnresolvedBy: []sessionsvc.PRUnresolvedReviewer{{
				ReviewerID: "reviewer-a",
				Count:      1,
				ReviewURL:  "https://github.com/justvibedev/tallpine-dev/pull/142#pullrequestreview-1",
				Links:      []sessionsvc.PRReviewCommentLink{{URL: "https://github.com/justvibedev/tallpine-dev/pull/142#discussion_r1", File: "main.go", Line: 12}},
			}},
		},
		Mergeability: sessionsvc.PRMergeabilitySummary{
			State:   domain.MergeConflicting,
			Reasons: []string{"conflicts"},
			PRURL:   "https://github.com/justvibedev/tallpine-dev/pull/142",
		},
		StateChangedAt: time.Date(2026, 6, 4, 11, 30, 0, 0, time.UTC),
		CreatedAt:      time.Date(2026, 6, 4, 9, 0, 0, 0, time.UTC),
		UpdatedAt:      time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC),
	}}, nil
}

func (f *fakeSessionService) ListPRListing(ctx context.Context, id domain.SessionID) (sessionsvc.PRListing, error) {
	prs, err := f.ListPRSummaries(ctx, id)
	if err != nil {
		return sessionsvc.PRListing{}, err
	}
	return sessionsvc.PRListing{Tracked: prs, Linked: f.linkedPRs}, nil
}

func (f *fakeSessionService) ClaimPR(_ context.Context, id domain.SessionID, ref string, opts sessionsvc.ClaimPROptions) (sessionsvc.ClaimPRResult, error) {
	f.lastClaimOptions = opts
	if f.claimErr != nil {
		return sessionsvc.ClaimPRResult{}, f.claimErr
	}
	if _, ok := f.sessions[id]; !ok {
		return sessionsvc.ClaimPRResult{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	prs, _ := f.ListPRs(context.Background(), id)
	return sessionsvc.ClaimPRResult{PRs: prs, TakenOverFrom: []domain.SessionID{}, BranchChanged: true}, nil
}

func (f *fakeSessionService) StageAttachments(
	_ context.Context,
	id domain.SessionID,
	attachments []ports.SpawnAttachment,
) ([]string, error) {
	if f.stageErr != nil {
		return nil, f.stageErr
	}
	if _, ok := f.sessions[id]; !ok {
		return nil, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	f.staged = attachments
	return f.stagedPaths, nil
}

func (f *fakeSessionService) ListWorkspaceFiles(ctx context.Context, id domain.SessionID) (sessionsvc.WorkspaceFiles, error) {
	if f.workspaceListHook != nil {
		if err := f.workspaceListHook(ctx); err != nil {
			return sessionsvc.WorkspaceFiles{}, err
		}
	}
	if f.workspaceErr != nil {
		return sessionsvc.WorkspaceFiles{}, f.workspaceErr
	}
	if _, ok := f.sessions[id]; !ok {
		return sessionsvc.WorkspaceFiles{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if f.workspaceFiles.SessionID != "" {
		return f.workspaceFiles, nil
	}
	return sessionsvc.WorkspaceFiles{SessionID: id}, nil
}

func (f *fakeSessionService) GetWorkspaceManifest(ctx context.Context, id domain.SessionID) (sessionsvc.WorkspaceManifest, error) {
	files, err := f.ListWorkspaceFiles(ctx, id)
	if err != nil {
		return sessionsvc.WorkspaceManifest{}, err
	}
	changed := make([]sessionsvc.WorkspaceFileSummary, 0, len(files.Files))
	for _, file := range files.Files {
		if file.Status != sessionsvc.WorkspaceFileUnmodified {
			changed = append(changed, file)
		}
	}
	return sessionsvc.WorkspaceManifest{
		SessionID:        files.SessionID,
		WorkspaceVersion: files.WorkspaceVersion,
		CompareBaseSHA:   files.CompareBaseSHA,
		CompareBaseRef:   files.CompareBaseRef,
		CompareMode:      files.CompareMode,
		Files:            changed,
		Sections:         files.Sections,
		Summary:          files.Summary,
		Truncated:        files.Truncated,
		Degraded:         files.Degraded,
		DegradedCode:     files.DegradedCode,
	}, nil
}

func (f *fakeSessionService) RefreshWorkspaceManifest(ctx context.Context, id domain.SessionID) (sessionsvc.WorkspaceManifest, error) {
	return f.GetWorkspaceManifest(ctx, id)
}

func (f *fakeSessionService) ReconcileWorkspaceManifest(ctx context.Context, id domain.SessionID) (sessionsvc.WorkspaceManifest, error) {
	f.workspaceReconciles++
	return f.GetWorkspaceManifest(ctx, id)
}

func (f *fakeSessionService) GetWorkspaceHistory(ctx context.Context, id domain.SessionID, view sessionsvc.WorkspaceView) (sessionsvc.WorkspaceHistory, error) {
	f.workspaceHistoryView = view
	files, err := f.ListWorkspaceFiles(ctx, id)
	if err != nil {
		return sessionsvc.WorkspaceHistory{}, err
	}
	if f.workspaceViewHistory != nil {
		return *f.workspaceViewHistory, nil
	}
	return sessionsvc.WorkspaceHistory{
		SessionID: files.SessionID, Commits: files.Commits, CommitsTruncated: files.CommitsTruncated,
		Ahead: files.Ahead, Behind: files.Behind,
	}, nil
}

func (f *fakeSessionService) ListWorkspaceBranches(_ context.Context, id domain.SessionID) (sessionsvc.WorkspaceBranches, error) {
	if f.workspaceErr != nil {
		return sessionsvc.WorkspaceBranches{}, f.workspaceErr
	}
	if _, ok := f.sessions[id]; !ok {
		return sessionsvc.WorkspaceBranches{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	branches := f.workspaceBranches
	branches.SessionID = id
	return branches, nil
}

func (f *fakeSessionService) ListPRFiles(_ context.Context, id domain.SessionID, _ int, _ string) (sessionsvc.PRFiles, error) {
	if _, ok := f.sessions[id]; !ok {
		return sessionsvc.PRFiles{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if f.prFiles.SessionID != "" {
		return f.prFiles, nil
	}
	return sessionsvc.PRFiles{SessionID: id}, nil
}

func (f *fakeSessionService) GetPRFile(_ context.Context, id domain.SessionID, _ int, _ string, path string, _ *string) (sessionsvc.WorkspaceFileDetail, error) {
	if _, ok := f.sessions[id]; !ok {
		return sessionsvc.WorkspaceFileDetail{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return sessionsvc.WorkspaceFileDetail{SessionID: id, Path: path}, nil
}

func (f *fakeSessionService) GetPRFileAtCommit(ctx context.Context, id domain.SessionID, number int, sourceURL, path, commitSHA string) (sessionsvc.WorkspaceFileDetail, error) {
	f.prFileCommitSHA = commitSHA
	return f.GetPRFile(ctx, id, number, sourceURL, path, nil)
}

func (f *fakeSessionService) WorkspaceWatchPaths(_ context.Context, id domain.SessionID) ([]string, error) {
	if f.workspaceErr != nil {
		return nil, f.workspaceErr
	}
	session, ok := f.sessions[id]
	if !ok {
		return nil, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if len(f.workspacePaths) > 0 {
		return f.workspacePaths, nil
	}
	return []string{session.Metadata.WorkspacePath}, nil
}

func (f *fakeSessionService) GetWorkspaceFile(_ context.Context, id domain.SessionID, path string, section sessionsvc.WorkspaceFileSection) (sessionsvc.WorkspaceFileDetail, error) {
	f.workspaceFileSection = section
	if f.workspaceErr != nil {
		return sessionsvc.WorkspaceFileDetail{}, f.workspaceErr
	}
	if _, ok := f.sessions[id]; !ok {
		return sessionsvc.WorkspaceFileDetail{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if f.workspaceFile.SessionID != "" {
		return f.workspaceFile, nil
	}
	return sessionsvc.WorkspaceFileDetail{SessionID: id, Path: path}, nil
}

func (f *fakeSessionService) GetWorkspaceFileAtCommit(ctx context.Context, id domain.SessionID, path, commitSHA string) (sessionsvc.WorkspaceFileDetail, error) {
	f.workspaceFileCommitSHA = commitSHA
	return f.GetWorkspaceFile(ctx, id, path, sessionsvc.WorkspaceFileSectionCommitted)
}

func (f *fakeSessionService) UpdateWorkspaceFile(_ context.Context, id domain.SessionID, input sessionsvc.UpdateWorkspaceFileInput) (sessionsvc.WorkspaceFileDetail, error) {
	f.workspaceFileUpdate = input
	file := f.workspaceFile
	file.SessionID = id
	file.Path = input.Path
	file.Content = input.Content
	return file, f.workspaceErr
}

func (f *fakeSessionService) GetWorkspaceFileBlob(_ context.Context, id domain.SessionID, path string, side sessionsvc.WorkspaceFileBlobSide) (sessionsvc.WorkspaceFileBlob, error) {
	if f.workspaceErr != nil {
		return sessionsvc.WorkspaceFileBlob{}, f.workspaceErr
	}
	if _, ok := f.sessions[id]; !ok {
		return sessionsvc.WorkspaceFileBlob{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if f.workspaceBlob.MediaType != "" {
		blob := f.workspaceBlob
		blob.Side = side
		return blob, nil
	}
	return sessionsvc.WorkspaceFileBlob{Path: path, Side: side, MediaType: "image/png"}, nil
}

func (f *fakeSessionService) GetWorkspaceDiffs(_ context.Context, id domain.SessionID, input sessionsvc.WorkspaceDiffInput) (sessionsvc.WorkspaceDiffs, error) {
	f.workspaceDiffInput = input
	if f.workspaceErr != nil {
		return sessionsvc.WorkspaceDiffs{}, f.workspaceErr
	}
	if _, ok := f.sessions[id]; !ok {
		return sessionsvc.WorkspaceDiffs{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if f.workspaceDiffs.SessionID != "" {
		return f.workspaceDiffs, nil
	}
	return sessionsvc.WorkspaceDiffs{SessionID: id}, nil
}

func (f *fakeSessionService) GetWorkspaceFileRevision(_ context.Context, id domain.SessionID, path string, scope sessionsvc.WorkspaceDiffScope, side sessionsvc.WorkspaceFileBlobSide, workspaceVersion, expectedRevision string) (sessionsvc.WorkspaceFileRevision, error) {
	f.workspaceRevisionPath = path
	f.workspaceRevisionScope = scope
	f.workspaceRevisionSide = side
	f.workspaceRevisionVersion = workspaceVersion
	f.workspaceExpectedRevision = expectedRevision
	if f.workspaceErr != nil {
		return sessionsvc.WorkspaceFileRevision{}, f.workspaceErr
	}
	if _, ok := f.sessions[id]; !ok {
		return sessionsvc.WorkspaceFileRevision{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if f.workspaceRevision.SessionID != "" {
		return f.workspaceRevision, nil
	}
	return sessionsvc.WorkspaceFileRevision{SessionID: id, Path: path, Side: side, Revision: expectedRevision, Exists: true}, nil
}

func (f *fakeSessionService) GetPRFileRevision(ctx context.Context, id domain.SessionID, number int, _ string, path string, side sessionsvc.WorkspaceFileBlobSide) (sessionsvc.WorkspaceFileRevision, error) {
	return f.GetWorkspaceFileRevision(ctx, id, path, sessionsvc.WorkspaceDiffCommitted, side, "", "")
}

func (f *fakeSessionService) GetPRFileRevisionAtCommit(ctx context.Context, id domain.SessionID, number int, sourceURL, path string, side sessionsvc.WorkspaceFileBlobSide, commitSHA string) (sessionsvc.WorkspaceFileRevision, error) {
	f.prRevisionCommitSHA = commitSHA
	return f.GetPRFileRevision(ctx, id, number, sourceURL, path, side)
}

func (f *fakeSessionService) GetWorkspaceSelectionFileRevision(ctx context.Context, id domain.SessionID, input sessionsvc.WorkspaceSelectionRevisionInput) (sessionsvc.WorkspaceFileRevision, error) {
	f.workspaceSelectionRevision = input
	return f.GetWorkspaceFileRevision(ctx, id, input.Path, input.Scope, input.Side, input.WorkspaceVersion, input.ExpectedRevision)
}

func (f *fakeSessionService) GetWorkspaceCommitSelection(_ context.Context, id domain.SessionID, view sessionsvc.WorkspaceView, commitSHAs []string, workspaceVersion string) (sessionsvc.WorkspaceCommitSelection, error) {
	f.workspaceSelectionView = view
	f.workspaceSelectionSHAs = commitSHAs
	f.workspaceSelectionVersion = workspaceVersion
	if f.workspaceErr != nil {
		return sessionsvc.WorkspaceCommitSelection{}, f.workspaceErr
	}
	if _, ok := f.sessions[id]; !ok {
		return sessionsvc.WorkspaceCommitSelection{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	selection := f.workspaceSelection
	selection.SessionID = id
	return selection, nil
}

func (f *fakeSessionService) ResolveWorkspacePath(_ context.Context, id domain.SessionID, rawPath string) (sessionsvc.WorkspacePathResolution, error) {
	f.workspaceResolvePath = rawPath
	if f.workspaceErr != nil {
		return sessionsvc.WorkspacePathResolution{}, f.workspaceErr
	}
	if _, ok := f.sessions[id]; !ok {
		return sessionsvc.WorkspacePathResolution{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return f.workspaceResolution, nil
}

func (f *fakeSessionService) SearchWorkspaceFiles(_ context.Context, id domain.SessionID, query, cursor string, limit int) (sessionsvc.WorkspaceFileSearch, error) {
	f.workspaceSearchQuery = query
	f.workspaceSearchCursor = cursor
	f.workspaceSearchLimit = limit
	if f.workspaceErr != nil {
		return sessionsvc.WorkspaceFileSearch{}, f.workspaceErr
	}
	if _, ok := f.sessions[id]; !ok {
		return sessionsvc.WorkspaceFileSearch{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if f.workspaceSearch.SessionID != "" {
		return f.workspaceSearch, nil
	}
	return sessionsvc.WorkspaceFileSearch{SessionID: id, Query: query}, nil
}

func (f *fakeSessionService) ListWorkspaceTree(_ context.Context, id domain.SessionID, path string) (sessionsvc.WorkspaceTree, error) {
	f.workspaceTreePath = path
	if f.workspaceErr != nil {
		return sessionsvc.WorkspaceTree{}, f.workspaceErr
	}
	if _, ok := f.sessions[id]; !ok {
		return sessionsvc.WorkspaceTree{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if f.workspaceTree.SessionID != "" {
		return f.workspaceTree, nil
	}
	return sessionsvc.WorkspaceTree{SessionID: id, Path: path}, nil
}

func (f *fakeSessionService) InvalidateWorkspaceCache(_ domain.SessionID) {}

func newSessionTestServer(t *testing.T, svc *fakeSessionService) *httptest.Server {
	return newSessionTestServerWithPreview(t, svc, nil)
}

func newSessionTestServerWithPreview(
	t *testing.T,
	svc *fakeSessionService,
	managed *fakeManagedPreviewServer,
) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps := httpd.APIDeps{Sessions: svc}
	if managed != nil {
		deps.PreviewServer = managed
		deps.SessionCapabilities = allowSessionCapability{}
	}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{AllowedOrigins: []string{"app://renderer", "http://localhost:5173"}}, log, nil, deps, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func doPreviewOriginRequest(t *testing.T, srv *httptest.Server, previewURL, requestPath string) ([]byte, int, http.Header) {
	return doPreviewOriginMethod(t, srv, http.MethodGet, previewURL, requestPath)
}

func doPreviewOriginMethod(t *testing.T, srv *httptest.Server, method, previewURL, requestPath string) ([]byte, int, http.Header) {
	t.Helper()
	preview, err := url.Parse(previewURL)
	if err != nil {
		t.Fatalf("parse preview URL: %v", err)
	}
	req, err := http.NewRequest(method, srv.URL+requestPath, nil)
	if err != nil {
		t.Fatalf("new preview request: %v", err)
	}
	req.Host = preview.Host
	req.Header.Set("Origin", preview.Scheme+"://"+preview.Host)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do preview request: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read preview response: %v", err)
	}
	return body, resp.StatusCode, resp.Header
}

func mustPreviewFileURL(t *testing.T, srv *httptest.Server, id domain.SessionID, entry string) string {
	t.Helper()
	raw, err := previewutil.FileURL(srv.URL, id, entry)
	if err != nil {
		t.Fatalf("FileURL: %v", err)
	}
	return raw
}

// countingRecorder counts the writes and flushes a handler makes.
type countingRecorder struct {
	*httptest.ResponseRecorder
	writes, flushes int
}

func (r *countingRecorder) Write(p []byte) (int, error) {
	r.writes++
	return r.ResponseRecorder.Write(p)
}

func (r *countingRecorder) Flush() {
	r.flushes++
	r.ResponseRecorder.Flush()
}

type sessionBody struct {
	ID               string `json:"id"`
	ProjectID        string `json:"projectId"`
	IssueID          string `json:"issueId"`
	Kind             string `json:"kind"`
	Harness          string `json:"harness"`
	DisplayName      string `json:"displayName"`
	Branch           string `json:"branch"`
	Status           string `json:"status"`
	SCMStatus        string `json:"scmStatus"`
	KanbanColumn     string `json:"kanbanColumn"`
	DisplayStatus    string `json:"displayStatus"`
	TerminalHandleID string `json:"terminalHandleId"`
}

func (f *fakeSessionService) RequestKill(ctx context.Context, id domain.SessionID) (sessionmanager.KillResult, error) {
	freed, err := f.Kill(ctx, id)
	return sessionmanager.KillResult{Freed: freed && !f.cleanupPending, CleanupPending: f.cleanupPending}, err
}

func (f *fakeSessionService) GetWorkspaceSnapshotFile(ctx context.Context, id domain.SessionID, input sessionsvc.WorkspaceSelectionRevisionInput) (sessionsvc.WorkspaceFileDetail, error) {
	f.workspaceSnapshotInput = input
	file, err := f.GetWorkspaceFile(ctx, id, input.Path, "")
	file.Historical = true
	return file, err
}
func (f *fakeSessionService) ListWorkspaceSnapshotTree(ctx context.Context, id domain.SessionID, input sessionsvc.WorkspaceSelectionRevisionInput, filter string) (sessionsvc.WorkspaceTree, error) {
	f.workspaceSnapshotInput = input
	f.workspaceSnapshotFilter = filter
	tree, err := f.ListWorkspaceTree(ctx, id, input.Path)
	tree.Snapshot = true
	return tree, err
}

func (f *fakeSessionService) GetWorkspaceSnapshotBlob(ctx context.Context, id domain.SessionID, input sessionsvc.WorkspaceSelectionRevisionInput) (sessionsvc.WorkspaceFileBlob, error) {
	f.workspaceSnapshotInput = input
	return f.GetWorkspaceFileBlob(ctx, id, input.Path, input.Side)
}

func (f *fakeSessionService) HandoffOrchestrator(ctx context.Context, id domain.SessionID, notePath string) (domain.Session, error) {
	f.handoffID, f.handoffNote = id, notePath
	old, ok := f.sessions[id]
	if !ok {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return f.SpawnOrchestrator(ctx, old.ProjectID, true, old.Mode, "")
}

func (f *fakeSessionService) EndLane(_ context.Context, id domain.SessionID, _ bool) (sessionsvc.EndLaneOutcome, error) {
	f.endLaneID = id
	return sessionsvc.EndLaneOutcome{Removed: id, Orchestrator: "default-1"}, nil
}

func (f *fakeSessionService) FindLaneOrchestrator(context.Context, domain.ProjectID, string) (domain.Session, bool, error) {
	return f.laneOrchestrator, f.laneOrchestrator.ID != "", nil
}
