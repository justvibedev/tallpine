package controllers

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/attachmentstore"
	"tallpine.eu/tallpine/backend/internal/bgtasks"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/previewserver"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
	usagesvc "tallpine.eu/tallpine/backend/internal/service/usage"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

const (
	maxPromptLen      = 16 << 10
	maxMessageLen     = 4096
	maxModelLen       = 256
	maxDisplayNameLen = 100
	maxIdempotencyKey = 128

	// Attachment limits guard the daemon against oversized spawn bodies. Files
	// are pasted/dropped into the task brief and inlined as base64 in the JSON
	// body, so the caps are deliberately conservative.
	maxAttachments      = 8
	maxAttachmentBytes  = attachmentstore.MaxFileBytes // 50 MiB per file, decoded
	maxAttachmentsBytes = 100 << 20                    // 100 MiB total, decoded
	// maxSpawnBodyBytes bounds the raw request body before it is decoded. The
	// per-attachment and total caps above only apply after the whole body is
	// materialized, so without this an oversized body (base64 inflates the
	// decoded total by ~4/3) would allocate in full first. Derived from the
	// decoded total plus headroom for the prompt and JSON envelope.
	maxSpawnBodyBytes = maxAttachmentsBytes*4/3 + (2 << 20)
	// maxSendBodyBytes is the /send equivalent of maxSpawnBodyBytes, sized off
	// maxAttachmentBytes (one attachment) rather than maxAttachmentsBytes
	// (spawn's up-to-eight): unlike spawn, a send carries at most one inline
	// attachment.
	maxSendBodyBytes = maxAttachmentBytes*4/3 + (2 << 20)
)

var (
	errPreviewFileNotFound         = errors.New("preview file not found")
	errPreviewFileOutsideWorkspace = errors.New("preview file is outside the session workspace")
)

// SessionService is the controller-facing session service contract.
type SessionService interface {
	List(ctx context.Context, filter sessionsvc.ListFilter) ([]domain.Session, error)
	ListPage(ctx context.Context, filter sessionsvc.ListFilter) (sessionsvc.ListPage, error)
	Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error)
	SpawnOrchestrator(ctx context.Context, projectID domain.ProjectID, clean bool, requestedMode domain.SessionMode, approval domain.PermissionMode) (domain.Session, error)
	// SpawnLaneOrchestrator is SpawnOrchestrator for the lane with this backlog item id.
	SpawnLaneOrchestrator(ctx context.Context, projectID domain.ProjectID, clean bool, requestedMode domain.SessionMode, approval domain.PermissionMode, laneItemID string) (domain.Session, error)
	// SpawnLaneOrchestratorBrief is SpawnLaneOrchestrator with a first user turn
	// delivered like a spawn prompt; created is false when a running one was returned.
	SpawnLaneOrchestratorBrief(ctx context.Context, projectID domain.ProjectID, clean bool, requestedMode domain.SessionMode, approval domain.PermissionMode, laneItemID, prompt string) (sess domain.Session, created bool, err error)
	HandoffOrchestrator(ctx context.Context, id domain.SessionID, notePath string) (domain.Session, error)
	// EndLane removes a lane orchestrator and moves its workers to the default one.
	EndLane(ctx context.Context, id domain.SessionID, dryRun bool) (sessionsvc.EndLaneOutcome, error)
	// FindLaneOrchestrator returns the newest live orchestrator of a lane.
	FindLaneOrchestrator(ctx context.Context, projectID domain.ProjectID, laneItemID string) (domain.Session, bool, error)
	Get(ctx context.Context, id domain.SessionID) (domain.Session, error)
	GetRecord(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error)
	Restore(ctx context.Context, id domain.SessionID) (sessionsvc.RestoreOutcome, error)
	ExitAgent(ctx context.Context, id domain.SessionID) (sessionsvc.ExitAgentOutcome, error)
	ResumeAgent(ctx context.Context, id domain.SessionID) (sessionsvc.ResumeAgentOutcome, error)
	RequestKill(ctx context.Context, id domain.SessionID) (sessionmanager.KillResult, error)
	DeleteArtifacts(ctx context.Context, id domain.SessionID) error
	RollbackSpawn(ctx context.Context, id domain.SessionID) (sessionsvc.RollbackOutcome, error)
	Cleanup(ctx context.Context, project domain.ProjectID) (sessionsvc.CleanupOutcome, error)
	Rename(ctx context.Context, id domain.SessionID, displayName string) error
	SetPreview(ctx context.Context, id domain.SessionID, previewURL string) (domain.Session, error)
	SetTerminateOnPRMerge(ctx context.Context, id domain.SessionID, terminate bool) (domain.Session, error)
	SetAutoInjectReview(ctx context.Context, id domain.SessionID, autoInject bool) (domain.Session, error)
	SetAutoInjectCI(ctx context.Context, id domain.SessionID, autoInject bool) (domain.Session, error)
	SetReviewerHarness(ctx context.Context, id domain.SessionID, harness domain.ReviewerHarness, config domain.AgentConfig) (domain.Session, error)
	SetAutoReview(ctx context.Context, id domain.SessionID, enabled bool) (domain.Session, error)
	Send(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment) error
	DelegateTask(ctx context.Context, in sessionsvc.DelegateTaskInput) (sessionsvc.DelegateTaskOutcome, error)
	PrepareTask(ctx context.Context, projectID domain.ProjectID) (string, error)
	CancelTaskPreparation(ctx context.Context, token string) error
	ListPRListing(ctx context.Context, id domain.SessionID) (sessionsvc.PRListing, error)
	ClaimPR(ctx context.Context, id domain.SessionID, ref string, opts sessionsvc.ClaimPROptions) (sessionsvc.ClaimPRResult, error)
	StageAttachments(ctx context.Context, id domain.SessionID, attachments []ports.SpawnAttachment) ([]string, error)
	WorkspaceWatchPaths(ctx context.Context, id domain.SessionID) ([]string, error)
	GetWorkspaceManifest(ctx context.Context, id domain.SessionID) (sessionsvc.WorkspaceManifest, error)
	RefreshWorkspaceManifest(ctx context.Context, id domain.SessionID) (sessionsvc.WorkspaceManifest, error)
	ReconcileWorkspaceManifest(ctx context.Context, id domain.SessionID) (sessionsvc.WorkspaceManifest, error)
	GetWorkspaceHistory(ctx context.Context, id domain.SessionID, view sessionsvc.WorkspaceView) (sessionsvc.WorkspaceHistory, error)
	ListWorkspaceBranches(ctx context.Context, id domain.SessionID) (sessionsvc.WorkspaceBranches, error)
	ListWorkspaceFiles(ctx context.Context, id domain.SessionID) (sessionsvc.WorkspaceFiles, error)
	GetWorkspaceFile(ctx context.Context, id domain.SessionID, path string, section sessionsvc.WorkspaceFileSection) (sessionsvc.WorkspaceFileDetail, error)
	GetWorkspaceFileAtCommit(ctx context.Context, id domain.SessionID, path, commitSHA string) (sessionsvc.WorkspaceFileDetail, error)
	UpdateWorkspaceFile(ctx context.Context, id domain.SessionID, input sessionsvc.UpdateWorkspaceFileInput) (sessionsvc.WorkspaceFileDetail, error)
	ListPRFiles(ctx context.Context, id domain.SessionID, number int, sourceURL string) (sessionsvc.PRFiles, error)
	GetPRFile(ctx context.Context, id domain.SessionID, number int, sourceURL, path string, previousPath *string) (sessionsvc.WorkspaceFileDetail, error)
	GetPRFileAtCommit(ctx context.Context, id domain.SessionID, number int, sourceURL, path, commitSHA string) (sessionsvc.WorkspaceFileDetail, error)
	GetPRFileRevision(ctx context.Context, id domain.SessionID, number int, sourceURL, path string, side sessionsvc.WorkspaceFileBlobSide) (sessionsvc.WorkspaceFileRevision, error)
	GetPRFileRevisionAtCommit(ctx context.Context, id domain.SessionID, number int, sourceURL, path string, side sessionsvc.WorkspaceFileBlobSide, commitSHA string) (sessionsvc.WorkspaceFileRevision, error)
	GetWorkspaceFileBlob(ctx context.Context, id domain.SessionID, path string, side sessionsvc.WorkspaceFileBlobSide) (sessionsvc.WorkspaceFileBlob, error)
	GetWorkspaceDiffs(ctx context.Context, id domain.SessionID, input sessionsvc.WorkspaceDiffInput) (sessionsvc.WorkspaceDiffs, error)
	GetWorkspaceFileRevision(ctx context.Context, id domain.SessionID, path string, scope sessionsvc.WorkspaceDiffScope, side sessionsvc.WorkspaceFileBlobSide, workspaceVersion, expectedRevision string) (sessionsvc.WorkspaceFileRevision, error)
	GetWorkspaceSelectionFileRevision(ctx context.Context, id domain.SessionID, input sessionsvc.WorkspaceSelectionRevisionInput) (sessionsvc.WorkspaceFileRevision, error)
	GetWorkspaceSnapshotBlob(ctx context.Context, id domain.SessionID, input sessionsvc.WorkspaceSelectionRevisionInput) (sessionsvc.WorkspaceFileBlob, error)
	GetWorkspaceSnapshotFile(ctx context.Context, id domain.SessionID, input sessionsvc.WorkspaceSelectionRevisionInput) (sessionsvc.WorkspaceFileDetail, error)
	ListWorkspaceSnapshotTree(ctx context.Context, id domain.SessionID, input sessionsvc.WorkspaceSelectionRevisionInput, filter string) (sessionsvc.WorkspaceTree, error)
	GetWorkspaceCommitSelection(ctx context.Context, id domain.SessionID, view sessionsvc.WorkspaceView, commitSHAs []string, workspaceVersion string) (sessionsvc.WorkspaceCommitSelection, error)
	SearchWorkspaceFiles(ctx context.Context, id domain.SessionID, query, cursor string, limit int) (sessionsvc.WorkspaceFileSearch, error)
	ResolveWorkspacePath(ctx context.Context, id domain.SessionID, rawPath string) (sessionsvc.WorkspacePathResolution, error)
	ListWorkspaceTree(ctx context.Context, id domain.SessionID, path string) (sessionsvc.WorkspaceTree, error)
	InvalidateWorkspaceCache(id domain.SessionID)
	Pin(ctx context.Context, id domain.SessionID) (domain.Session, error)
	Unpin(ctx context.Context, id domain.SessionID) (domain.Session, error)
}

type sessionMessageReceiptSender interface {
	SendWithReceipt(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment, options ports.MessageDeliveryOptions) (ports.MessageReceipt, error)
}

type sessionMessageOptionsSender interface {
	SendWithOptions(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment, options ports.MessageDeliveryOptions) error
}

// ActivityRecorder applies an agent activity-state signal to a session. It is
// satisfied directly by *lifecycle.Manager: an activity signal is a pure
// lifecycle reduction (no runtime/workspace teardown), so it bypasses
// SessionService rather than threading a no-op passthrough through the session
// manager.
type ActivityRecorder interface {
	ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) error
}

// ManagedPreviewServer is the deterministic server lifecycle attached to a
// worker. It is separate from static file rendering and browser automation.
type ManagedPreviewServer interface {
	Start(ctx context.Context, sessionID domain.SessionID, workspacePath, configurationName string, projectEnv map[string]string, fence previewserver.Fence) (previewserver.Status, error)
	Stop(ctx context.Context, sessionID domain.SessionID) (previewserver.Status, error)
	Status(sessionID domain.SessionID) previewserver.Status
}

// SessionCapabilityValidator verifies the daemon-issued token injected only
// into the owning worker session.
type SessionCapabilityValidator interface {
	Valid(sessionID domain.SessionID, token, verifier string) bool
}

// ShellPreviewCapabilityValidator checks the separate, preview-only bearer
// held by a live session-scoped user shell.
type ShellPreviewCapabilityValidator interface {
	ValidPreviewCapability(ctx context.Context, sessionID domain.SessionID, token string) (bool, error)
}

// UsageHookRecorder consumes transcript metadata from the same native hook
// callback without changing activity-state semantics.
type UsageHookRecorder interface {
	RecordHook(ctx context.Context, id domain.SessionID, signal usagesvc.HookSignal) error
}

// UsageLimitReader reports a session paused by its provider's usage limit.
type UsageLimitReader interface {
	UsageLimitPausedUntil(context.Context, domain.SessionID) (time.Time, bool)
}

// SessionsController owns the session routes. Nil keeps routes registered but
// returns OpenAPI-backed 501s.
type SessionsController struct {
	Svc SessionService
	// Policy decides the agent policy of an agent-scoped caller; nil restricts nothing.
	Policy PolicyDecider
	// Backlog validates and links `tallpine spawn --backlog`; nil answers 501 for that flag.
	Backlog BacklogSpawner
	// PlannerEnabled reports the TALLPINE_PLANNER switch; nil means off, and a
	// spawn with role planner is refused.
	PlannerEnabled func() bool
	Projects       interface {
		Get(context.Context, domain.ProjectID) (projectsvc.GetResult, error)
	}
	// OwnerMessages resolves `tallpine send --relay-owner-message`; nil refuses it.
	OwnerMessages OwnerMessageLookup
	// Routes warns at spawn when the project's reports bypass its orchestrator; nil omits it.
	Routes   RouteNoticer
	Activity ActivityRecorder
	// ClearedNotes builds the note a cleared context gets; nil answers 501.
	ClearedNotes             ClearedContextNoter
	DataDir                  string
	RunFilePath              string // locates the Tallpine state a fenced preview is denied
	Usage                    UsageHookRecorder
	Attachments              *attachmentstore.Store
	PreviewServer            ManagedPreviewServer
	Capabilities             SessionCapabilityValidator
	ShellPreviewCapabilities ShellPreviewCapabilityValidator
	// AgentBrowser is the host's agent browser and its credential
	// (config.AgentBrowserURL); nil when the host has none.
	AgentBrowser      *url.URL
	AgentBrowserToken string
	// ArtifactVersions serves artifact version history; nil leaves those routes unimplemented.
	ArtifactVersions ArtifactVersionService
	// LastMessages reads the latest turn's final assistant message for the
	// session detail read; nil leaves it out.
	LastMessages LastAssistantMessageReader
	// ServedModel reports the model a live session is actually answering with
	// (it differs from the launch model after an in-session /model switch); ""
	// keeps the launch model. Read only by `session get`: it scans a transcript.
	ServedModel func(ctx context.Context, sess domain.Session) string
	// Models fills the model of a session spawned without one; nil leaves it empty.
	Models ModelResolver
	// Instructions reads the effective worker project rules for the session
	// detail read; nil leaves them out.
	Instructions WorkerInstructionsReader
	// BackgroundTasks names the harness background tasks a session waits on; nil leaves waitingOn out.
	BackgroundTasks interface {
		Waiting(domain.SessionID) []string
		Running(domain.SessionID) []bgtasks.Info
		ExpectStop(domain.SessionID, string)
		CancelStop(domain.SessionID, string)
	}
	// UsageLimits reports a quota pause on a session; nil leaves pausedUntil out.
	UsageLimits UsageLimitReader
	// MessageSent observes each message a session delivered; nil ignores them.
	MessageSent MessageSentFunc
}

// MessageSentFunc observes a message the sender session delivered to target,
// so a worker that messaged its orchestrator is not reported as silent.
type MessageSentFunc func(ctx context.Context, sender, target domain.SessionID)

// WorkerInstructionsReader returns the project rules a worker session receives
// in its system prompt.
type WorkerInstructionsReader interface {
	WorkerInstructions(ctx context.Context, id domain.SessionID) (string, error)
}

// LastAssistantMessageReader reads the display copy of a session's latest
// main-turn final message.
type LastAssistantMessageReader interface {
	LastAssistantMessage(ctx context.Context, id domain.SessionID) (string, time.Time, bool, error)
}

// ModelResolver names the model a chat session runs when none was pinned.
type ModelResolver interface {
	EffectiveModel(ctx context.Context, id domain.SessionID) string
}

// Register mounts the session routes on the supplied router.
func (c *SessionsController) Register(r chi.Router) {
	r.Get("/sessions", c.list)
	r.Post("/sessions", c.spawn)
	r.Post("/sessions/cleanup", c.cleanup)
	r.Delete("/sessions/{sessionId}", c.deleteSession)
	r.Get("/sessions/{sessionId}", c.get)
	r.Get("/sessions/{sessionId}/preview", c.preview)
	r.Post("/sessions/{sessionId}/preview", c.setPreview)
	r.Delete("/sessions/{sessionId}/preview", c.clearPreview)
	r.Get("/sessions/{sessionId}/preview/server", c.previewServerStatus)
	r.Post("/sessions/{sessionId}/preview/server", c.startPreviewServer)
	r.Delete("/sessions/{sessionId}/preview/server", c.stopPreviewServer)
	r.Get("/sessions/{sessionId}/preview/files/*", c.previewFile)
	r.Get("/sessions/{sessionId}/preview/file-url", c.previewFileOriginURL)
	r.Get("/sessions/{sessionId}/preview/artifacts/*", c.previewArtifactFile)
	r.Get("/sessions/{sessionId}/artifact-files/*", c.artifactFile)
	r.Delete("/sessions/{sessionId}/artifact-files", c.deleteArtifactFile)
	r.Put("/sessions/{sessionId}/artifact-files", c.updateArtifactFile)
	r.Get("/sessions/{sessionId}/artifact-versions", c.listArtifactVersions)
	r.Get("/sessions/{sessionId}/artifact-versions/{version}", c.getArtifactVersion)
	r.Post("/sessions/{sessionId}/artifact-versions/restore", c.restoreArtifactVersion)
	r.Post("/sessions/{sessionId}/artifact-versions/sync", c.syncArtifactVersions)
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		r.Method(method, "/sessions/{sessionId}/preview/app/*", http.HandlerFunc(c.previewApp))
	}
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost} {
		r.Method(method, "/sessions/{sessionId}/agent-browser/*", http.HandlerFunc(c.agentBrowser))
	}
	r.Post("/sessions/{sessionId}/attachments", c.stageAttachments)
	r.Get("/sessions/{sessionId}/workspace/manifest", c.getWorkspaceManifest)
	r.Get("/sessions/{sessionId}/workspace/history", c.getWorkspaceHistory)
	r.Post("/sessions/{sessionId}/workspace/history/selection", c.getWorkspaceCommitSelection)
	r.Get("/sessions/{sessionId}/workspace/branches", c.listWorkspaceBranches)
	r.Get("/sessions/{sessionId}/workspace/files", c.listWorkspaceFiles)
	r.Get("/sessions/{sessionId}/workspace/file", c.getWorkspaceFile)
	r.Put("/sessions/{sessionId}/workspace/file", c.updateWorkspaceFile)
	r.Get("/sessions/{sessionId}/workspace/file/blob", c.getWorkspaceFileBlob)
	r.Post("/sessions/{sessionId}/workspace/diffs", c.getWorkspaceDiffs)
	r.Get("/sessions/{sessionId}/workspace/file/revision", c.getWorkspaceFileRevision)
	r.Get("/sessions/{sessionId}/workspace/search", c.searchWorkspaceFiles)
	r.Get("/sessions/{sessionId}/workspace/resolve", c.resolveWorkspacePath)
	r.Get("/sessions/{sessionId}/workspace/tree", c.listWorkspaceTree)
	r.Get("/sessions/{sessionId}/pr/{prNumber}/files", c.listPRFiles)
	r.Get("/sessions/{sessionId}/pr/{prNumber}/file", c.getPRFile)
	r.Get("/sessions/{sessionId}/pr/{prNumber}/file/revision", c.getPRFileRevision)
	r.Get("/sessions/{sessionId}/pr", c.listPRs)
	r.Post("/sessions/{sessionId}/pr/claim", c.claimPR)
	r.Patch("/sessions/{sessionId}", c.rename)
	r.Patch("/sessions/{sessionId}/merge-policy", c.setMergePolicy)
	r.Patch("/sessions/{sessionId}/auto-inject-review", c.setAutoInjectReviewPolicy)
	r.Patch("/sessions/{sessionId}/auto-inject-ci", c.setAutoInjectCIPolicy)
	r.Put("/sessions/{sessionId}/reviewer", c.setReviewer)
	r.Put("/sessions/{sessionId}/auto-review", c.setAutoReview)
	r.Post("/sessions/{sessionId}/restore", c.restore)
	r.Post("/sessions/{sessionId}/exit-agent", c.exitAgent)
	r.Post("/sessions/{sessionId}/resume-agent", c.resumeAgent)
	r.Get("/sessions/{sessionId}/interface-transition", c.interfaceTransitionStatus)
	r.Post("/sessions/{sessionId}/interface-transition", c.startInterfaceTransition)
	r.Delete("/sessions/{sessionId}/interface-transition", c.cancelInterfaceTransition)
	r.Put("/sessions/{sessionId}/interface-transition/{transitionId}/notice-acknowledgement", c.acknowledgeInterfaceTransitionNotice)
	r.Post("/sessions/{sessionId}/kill", c.kill)
	r.Post("/sessions/{sessionId}/rollback", c.rollback)
	r.Post("/sessions/{sessionId}/send", c.send)
	r.Post("/sessions/{sessionId}/activity", c.activity)
	r.Get("/sessions/{sessionId}/cleared-context-note", c.clearedContextNote)
	r.Post("/sessions/{sessionId}/pin", c.pin)
	r.Delete("/sessions/{sessionId}/pin", c.unpin)
	r.Get("/orchestrators", c.listOrchestrators)
	r.Post("/orchestrators", c.spawnOrchestrator)
	r.Post("/orchestrators/delegate", c.delegateTask)
	r.Post("/orchestrators/end", c.endLane)
	r.Post("/orchestrators/{id}/handoff", c.handoffOrchestrator)
	r.Post("/projects/{id}/tasks/prepare", c.prepareTask)
	r.Delete("/task-preparations/{token}", c.cancelTaskPreparation)
	r.Get("/orchestrators/{id}", c.getOrchestrator)
}

// RegisterStreams mounts long-lived session streams outside the REST timeout
// middleware. Worktree and artifact notifications remain active only while a
// client is actually viewing that session's files or artifacts.
func (c *SessionsController) RegisterStreams(r chi.Router) {
	r.Get("/sessions/{sessionId}/workspace/events", c.streamWorkspaceChanges)
	r.Get("/sessions/{sessionId}/artifact-events", c.streamArtifactChanges)
}

func sessionID(r *http.Request) domain.SessionID {
	return domain.SessionID(chi.URLParam(r, "sessionId"))
}
