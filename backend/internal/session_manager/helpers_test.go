package sessionmanager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

var ctx = context.Background()

type fakeStore struct {
	upsertWTHook                       func(domain.SessionWorktreeRecord) error // optional per-row failure injection
	lanes                              map[domain.SessionID]domain.SessionLane
	policyMu                           sync.Mutex
	policyRecords                      map[domain.SessionID][]byte
	exitMu                             sync.Mutex
	exitReasons                        map[domain.SessionID]domain.AgentExitReason
	cleanupMu                          sync.Mutex
	cleanupFacts                       map[domain.SessionID]domain.SessionCleanupRecord
	sessions                           map[domain.SessionID]domain.SessionRecord
	pr                                 map[domain.SessionID]domain.PRFacts
	projects                           map[string]domain.ProjectRecord
	conversations                      map[domain.SessionID]domain.ConversationRecord
	workspaceRepo                      map[string][]domain.WorkspaceRepoRecord
	num                                int
	deleteErr                          error
	upsertWTErr                        error
	listAllErr                         error
	getProjectErr                      error
	getSessionErr                      error
	updateSessionErr                   error
	hibernationCASConflicts            int
	updateBrowserCapabilityVerifierErr error
	deletePrepErr                      error

	commitClientRequestErr error
	createClientRequestErr error
	promoteTaskErr         error
	// worktrees maps session ID to its saved worktree rows (shutdown-saved marker).
	worktrees map[domain.SessionID][]domain.SessionWorktreeRecord
	// sharedLog, when non-nil, receives an ordered call entry for each
	// UpsertSessionWorktree invocation so ordering tests can compare across fakes.
	sharedLog *[]string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		sessions:      map[domain.SessionID]domain.SessionRecord{},
		pr:            map[domain.SessionID]domain.PRFacts{},
		projects:      map[string]domain.ProjectRecord{},
		conversations: map[domain.SessionID]domain.ConversationRecord{},
		workspaceRepo: map[string][]domain.WorkspaceRepoRecord{},
		worktrees:     map[domain.SessionID][]domain.SessionWorktreeRecord{},
	}
}
func (f *fakeStore) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	if f.getProjectErr != nil {
		return domain.ProjectRecord{}, false, f.getProjectErr
	}
	r, ok := f.projects[id]
	return r, ok, nil
}
func (f *fakeStore) ListWorkspaceRepos(_ context.Context, projectID string) ([]domain.WorkspaceRepoRecord, error) {
	return f.workspaceRepo[projectID], nil
}
func (f *fakeStore) CreateSession(_ context.Context, rec domain.SessionRecord) (domain.SessionRecord, error) {
	f.num++
	prefix := string(rec.ProjectID)
	if prefix == "" {
		prefix = "standalone"
	}
	rec.ID = domain.SessionID(fmt.Sprintf("%s-%d", prefix, f.num))
	f.sessions[rec.ID] = rec
	if rec.Lane != (domain.SessionLane{}) {
		if f.lanes == nil {
			f.lanes = map[domain.SessionID]domain.SessionLane{}
		}
		f.lanes[rec.ID] = rec.Lane
	}
	return rec, nil
}
func (f *fakeStore) CreateAutomationSession(ctx context.Context, rec domain.SessionRecord) (domain.SessionRecord, bool, error) {
	for _, existing := range f.sessions {
		if existing.AutomationRunID != nil && rec.AutomationRunID != nil && *existing.AutomationRunID == *rec.AutomationRunID {
			return existing, false, nil
		}
	}
	created, err := f.CreateSession(ctx, rec)
	return created, err == nil, err
}
func (f *fakeStore) CreateClientRequestSession(ctx context.Context, rec domain.SessionRecord) (domain.SessionRecord, bool, error) {
	if f.createClientRequestErr != nil {
		err := f.createClientRequestErr
		f.createClientRequestErr = nil
		return domain.SessionRecord{}, false, err
	}
	if existing, found, err := f.GetSessionByClientRequestID(ctx, rec.ClientRequestID); err != nil || found {
		return existing, false, err
	}
	created, err := f.CreateSession(ctx, rec)
	return created, err == nil, err
}
func (f *fakeStore) GetSessionByClientRequestID(_ context.Context, id string) (domain.SessionRecord, bool, error) {
	for _, rec := range f.sessions {
		if id != "" && rec.ClientRequestID == id {
			return rec, true, nil
		}
	}
	return domain.SessionRecord{}, false, nil
}
func (f *fakeStore) CommitClientRequestSession(_ context.Context, id domain.SessionID) error {
	if f.commitClientRequestErr != nil {
		return f.commitClientRequestErr
	}
	rec := f.sessions[id]
	rec.ClientRequestCommitted = true
	f.sessions[id] = rec
	return nil
}
func (f *fakeStore) RenameSessionIfDisplayName(_ context.Context, id domain.SessionID, current, name string, at time.Time) (bool, error) {
	rec, ok := f.sessions[id]
	if !ok || rec.DisplayName != current {
		return false, nil
	}
	rec.DisplayName, rec.UpdatedAt = name, at
	f.sessions[id] = rec
	return true, nil
}

func (f *fakeStore) UpdateSession(_ context.Context, rec domain.SessionRecord) error {
	if f.updateSessionErr != nil {
		return f.updateSessionErr
	}
	// Like the real store, a full-row update never writes the title,
	// artifact_dir, session_output_type, runtime_released_launch_id or branch
	// naming facts; only their narrow writes do. Nor does it put back a
	// renamed task's temporary branch.
	if existing, ok := f.sessions[rec.ID]; ok {
		rec.DisplayName = existing.DisplayName
		rec.Metadata.ArtifactDir = existing.Metadata.ArtifactDir
		rec.OutputType = existing.OutputType
		rec.Metadata.RuntimeReleasedLaunchID = existing.Metadata.RuntimeReleasedLaunchID
		rec.BranchNaming, rec.TemporaryBranch = existing.BranchNaming, existing.TemporaryBranch
		if existing.TemporaryBranch != "" && rec.Metadata.Branch == existing.TemporaryBranch {
			rec.Metadata.Branch = existing.Metadata.Branch
		}
	}
	f.sessions[rec.ID] = rec
	return nil
}
func (f *fakeStore) SetSessionHibernated(_ context.Context, id domain.SessionID, revision int64, at *time.Time) (bool, error) {
	rec, ok := f.sessions[id]
	if !ok || rec.Revision != revision || rec.IsTerminated {
		return false, nil
	}
	if f.hibernationCASConflicts > 0 {
		f.hibernationCASConflicts--
		rec.Revision++
		f.sessions[id] = rec
		return false, nil
	}
	rec.HibernatedAt = at
	rec.Revision++
	f.sessions[id] = rec
	return true, nil
}
func (f *fakeStore) UpdateSessionArtifactOutput(_ context.Context, id domain.SessionID, artifactDir string, outputType domain.SessionOutputType) (bool, error) {
	if f.updateSessionErr != nil {
		return false, f.updateSessionErr
	}
	rec, ok := f.sessions[id]
	if !ok {
		return false, nil
	}
	rec.Metadata.ArtifactDir = artifactDir
	rec.OutputType = outputType
	f.sessions[id] = rec
	return true, nil
}
func (f *fakeStore) SetSessionRuntimeReleased(_ context.Context, id domain.SessionID, launchID string) (bool, error) {
	rec, ok := f.sessions[id]
	if !ok || rec.Metadata.RuntimeLaunchID != launchID {
		return false, nil
	}
	rec.Metadata.RuntimeReleasedLaunchID = launchID
	f.sessions[id] = rec
	return true, nil
}
func (f *fakeStore) UpdateSessionModel(_ context.Context, id domain.SessionID, model string) (bool, error) {
	if f.updateSessionErr != nil {
		return false, f.updateSessionErr
	}
	rec, ok := f.sessions[id]
	if !ok {
		return false, nil
	}
	rec.Metadata.Model = model
	f.sessions[id] = rec
	return true, nil
}

func (f *fakeStore) SetSessionProvisionedWorkspace(_ context.Context, id domain.SessionID, branch, workspacePath, workspaceRepoPath string, now time.Time) (bool, error) {
	rec, ok := f.sessions[id]
	canPublish := rec.ProvisionState == domain.SessionProvisionProvisioning && !rec.IsTerminated ||
		rec.ProvisionState == domain.SessionProvisionFailed && (rec.Metadata.WorkspacePath == "" || rec.Metadata.WorkspacePath == workspacePath)
	if !ok || !canPublish {
		return false, nil
	}
	rec.Metadata.Branch = branch
	rec.Metadata.WorkspacePath = workspacePath
	rec.Metadata.WorkspaceRepoPath = workspaceRepoPath
	rec.UpdatedAt = now
	f.sessions[id] = rec
	return true, nil
}

func (f *fakeStore) SetSessionTaskBranch(_ context.Context, id domain.SessionID, expectedBranch, branch string, repoBranches map[string]string, now time.Time) (bool, error) {
	rec, ok := f.sessions[id]
	if !ok || rec.Metadata.Branch != expectedBranch {
		return false, nil
	}
	if branch != expectedBranch {
		rec.TemporaryBranch = expectedBranch
	}
	rec.Metadata.Branch, rec.BranchNaming = branch, false
	rec.UpdatedAt = now
	f.sessions[id] = rec
	for i, row := range f.worktrees[id] {
		if repoBranch, ok := repoBranches[row.RepoName]; ok {
			f.worktrees[id][i].Branch = repoBranch
		}
	}
	return true, nil
}

func (f *fakeStore) SetSessionBranchNaming(_ context.Context, id domain.SessionID, naming bool) error {
	if rec, ok := f.sessions[id]; ok {
		rec.BranchNaming = naming
		f.sessions[id] = rec
	}
	return nil
}

func (f *fakeStore) ClearSessionBranchNaming(context.Context) error {
	for id, rec := range f.sessions {
		rec.BranchNaming = false
		f.sessions[id] = rec
	}
	return nil
}

func (f *fakeStore) SetTaskPreparationBase(_ context.Context, id domain.SessionID, baseSHA, baseRef string) (bool, error) {
	rec, ok := f.sessions[id]
	if !ok || !rec.IsTaskPreparation || rec.IsTerminated || rec.ProvisionState != domain.SessionProvisionProvisioning {
		return false, nil
	}
	rec.Metadata.DiffBaseSHA = baseSHA
	rec.Metadata.DiffBaseRef = baseRef
	f.sessions[id] = rec
	return true, nil
}

func (f *fakeStore) SetSessionProvisionState(_ context.Context, id domain.SessionID, state domain.SessionProvisionState, message string, now time.Time) (bool, error) {
	rec, ok := f.sessions[id]
	if !ok {
		return false, nil
	}
	rec.ProvisionState = state
	rec.ProvisionError = message
	rec.UpdatedAt = now
	f.sessions[id] = rec
	return true, nil
}

func (f *fakeStore) SetSessionProvisionSteps(_ context.Context, id domain.SessionID, steps []domain.SessionProvisionStep, now time.Time) error {
	rec, ok := f.sessions[id]
	if !ok {
		return nil
	}
	rec.ProvisionSteps = append([]domain.SessionProvisionStep(nil), steps...)
	rec.UpdatedAt = now
	f.sessions[id] = rec
	return nil
}

func (f *fakeStore) PromoteTaskPreparation(_ context.Context, id domain.SessionID, rec domain.SessionRecord) (bool, error) {
	if f.promoteTaskErr != nil {
		err := f.promoteTaskErr
		f.promoteTaskErr = nil
		return false, err
	}
	current, ok := f.sessions[id]
	if !ok || !current.IsTaskPreparation {
		return false, nil
	}
	rec.ID = id
	rec.Metadata.Branch = current.Metadata.Branch
	rec.Metadata.WorkspacePath = current.Metadata.WorkspacePath
	rec.Metadata.WorkspaceRepoPath = current.Metadata.WorkspaceRepoPath
	f.sessions[id] = rec
	return true, nil
}

func (f *fakeStore) DeleteTaskPreparation(_ context.Context, id domain.SessionID) (bool, error) {
	if f.deletePrepErr != nil {
		return false, f.deletePrepErr
	}
	rec, ok := f.sessions[id]
	if !ok || !rec.IsTaskPreparation {
		return false, nil
	}
	delete(f.sessions, id)
	delete(f.worktrees, id)
	return true, nil
}

func (f *fakeStore) UpdateBrowserCapabilityVerifier(_ context.Context, id domain.SessionID, expected domain.SessionControllerOwner, verifier string) (bool, error) {
	if f.updateBrowserCapabilityVerifierErr != nil {
		return false, f.updateBrowserCapabilityVerifierErr
	}
	rec, ok := f.sessions[id]
	if !ok || rec.ControllerOwner() != expected {
		return false, nil
	}
	rec.Metadata.BrowserCapabilityVerifier = verifier
	f.sessions[id] = rec
	return true, nil
}
func (f *fakeStore) RecordSessionLatestUserPrompt(_ context.Context, id domain.SessionID, prompt string, updatedAt time.Time) (bool, error) {
	rec, ok := f.sessions[id]
	if !ok || rec.IsTerminated || rec.UpdatedAt.After(updatedAt) {
		return false, nil
	}
	rec.Metadata.LatestUserPrompt = prompt
	rec.Metadata.LatestUserPromptAt = updatedAt
	rec.Metadata.LatestAssistantUpdate = ""
	rec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointLegacy
	rec.Metadata.ConversationCheckpointGeneration = ""
	rec.Metadata.ConversationCheckpointNativeID = ""
	rec.UpdatedAt = updatedAt
	f.sessions[id] = rec
	return true, nil
}
func (f *fakeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	if f.getSessionErr != nil {
		return domain.SessionRecord{}, false, f.getSessionErr
	}
	r, ok := f.sessions[id]
	return r, ok, nil
}
func (f *fakeStore) ConversationForSession(_ context.Context, id domain.SessionID) (domain.ConversationRecord, error) {
	conversation, ok := f.conversations[id]
	if !ok {
		return domain.ConversationRecord{}, domain.ErrNoConversation
	}
	return conversation, nil
}
func (f *fakeStore) ListSessions(_ context.Context, p domain.ProjectID) ([]domain.SessionRecord, error) {
	var out []domain.SessionRecord
	for _, r := range f.sessions {
		if r.ProjectID == p {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakeStore) ListAllSessions(context.Context) ([]domain.SessionRecord, error) {
	if f.listAllErr != nil {
		return nil, f.listAllErr
	}
	var out []domain.SessionRecord
	for _, r := range f.sessions {
		out = append(out, r)
	}
	return out, nil
}
func (f *fakeStore) ListLiveSessions(context.Context) ([]domain.SessionRecord, error) {
	if f.listAllErr != nil {
		return nil, f.listAllErr
	}
	var out []domain.SessionRecord
	for _, r := range f.sessions {
		if !r.IsTerminated {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakeStore) ListChatHibernationCandidates(ctx context.Context) ([]domain.SessionID, error) {
	records, err := f.ListAllSessions(ctx)
	if err != nil {
		return nil, err
	}
	var ids []domain.SessionID
	for _, rec := range records {
		if rec.EligibleForChatHibernation() {
			ids = append(ids, rec.ID)
		}
	}
	return ids, nil
}
func (f *fakeStore) DeleteSession(_ context.Context, id domain.SessionID) (bool, error) {
	if f.deleteErr != nil {
		return false, f.deleteErr
	}
	rec, ok := f.sessions[id]
	if !ok {
		return false, nil
	}
	// Mirror the sqlite gate: only delete rows still in seed state.
	if rec.IsTerminated || rec.Metadata.WorkspacePath != "" || rec.Metadata.RuntimeHandleID != "" || rec.Metadata.AgentSessionID != "" || rec.Metadata.Prompt != "" ||
		rec.Metadata.LatestUserPrompt != "" || rec.Metadata.LatestAssistantUpdate != "" || rec.Metadata.NativeTranscriptPath != "" {
		return false, nil
	}
	delete(f.sessions, id)
	return true, nil
}
func (f *fakeStore) GetDisplayPRFactsForSession(_ context.Context, id domain.SessionID) (domain.PRFacts, bool, error) {
	if pr := f.pr[id]; pr.URL != "" {
		return pr, true, nil
	}
	return domain.PRFacts{}, false, nil
}
func (f *fakeStore) UpsertSessionWorktree(_ context.Context, row domain.SessionWorktreeRecord) error {
	if f.upsertWTHook != nil {
		if err := f.upsertWTHook(row); err != nil {
			return err
		}
	}
	if f.upsertWTErr != nil {
		return f.upsertWTErr
	}
	if f.sharedLog != nil {
		*f.sharedLog = append(*f.sharedLog, "UpsertSessionWorktree:"+string(row.SessionID))
	}
	rows := f.worktrees[row.SessionID]
	for i, r := range rows {
		if r.RepoName == row.RepoName {
			rows[i] = row
			f.worktrees[row.SessionID] = rows
			return nil
		}
	}
	f.worktrees[row.SessionID] = append(rows, row)
	return nil
}
func (f *fakeStore) ListSessionWorktrees(_ context.Context, id domain.SessionID) ([]domain.SessionWorktreeRecord, error) {
	return f.worktrees[id], nil
}
func (f *fakeStore) DeleteSessionWorktrees(_ context.Context, id domain.SessionID) error {
	if f.sharedLog != nil {
		*f.sharedLog = append(*f.sharedLog, "DeleteSessionWorktrees:"+string(id))
	}
	delete(f.worktrees, id)
	return nil
}

type fakeLCM struct {
	store          *fakeStore
	completed      int
	prepared       []string
	cancelled      []string
	chatBoundaries []domain.ConversationBranch
	// terminated counts MarkTerminated calls per session id.
	terminated map[domain.SessionID]int
}

func (l *fakeLCM) PrepareLaunch(id domain.SessionID, launchID string) error {
	l.prepared = append(l.prepared, string(id)+":"+launchID)
	return nil
}
func (l *fakeLCM) CancelLaunch(id domain.SessionID, launchID string) {
	l.cancelled = append(l.cancelled, string(id)+":"+launchID)
}
func (l *fakeLCM) ReleaseLaunch(id domain.SessionID, launchID string) {
	l.cancelled = append(l.cancelled, string(id)+":"+launchID)
}
func (l *fakeLCM) MarkSpawned(_ context.Context, id domain.SessionID, metadata domain.SessionMetadata) error {
	l.completed++
	rec := l.store.sessions[id]
	rec.IsTerminated = false
	rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: time.Now()}
	rec.FirstSignalAt = time.Now()
	rec.Metadata = preserveCheckpointOnFakeMarkSpawned(rec.Metadata, metadata)
	l.store.sessions[id] = rec
	return nil
}

func (l *fakeLCM) MarkChatReconnected(_ context.Context, id domain.SessionID, metadata domain.SessionMetadata) error {
	rec := l.store.sessions[id]
	rec.Metadata = metadata
	if rec.AutomationRunID != nil {
		rec.AutomationLaunchCompleted = true
	}
	l.store.sessions[id] = rec
	return nil
}

func (l *fakeLCM) MarkChatSpawned(
	ctx context.Context,
	id domain.SessionID,
	metadata domain.SessionMetadata,
	boundary domain.ConversationBranch,
) error {
	l.chatBoundaries = append(l.chatBoundaries, boundary)
	return l.MarkSpawned(ctx, id, metadata)
}

// Production Lifecycle Manager merges launch handles into the existing row;
// launch metadata never replaces replay checkpoint provenance. Keep the shared
// session-manager fake faithful to that ownership boundary.
func preserveCheckpointOnFakeMarkSpawned(
	base, launched domain.SessionMetadata,
) domain.SessionMetadata {
	if launched.LatestUserPrompt == "" {
		launched.LatestUserPrompt = base.LatestUserPrompt
	}
	if launched.LatestAssistantUpdate == "" {
		launched.LatestAssistantUpdate = base.LatestAssistantUpdate
	}
	launched.ConversationCheckpointState = base.ConversationCheckpointState
	launched.ConversationCheckpointGeneration = base.ConversationCheckpointGeneration
	launched.ConversationCheckpointNativeID = base.ConversationCheckpointNativeID
	launched.ConversationCheckpointUnsettled = base.ConversationCheckpointUnsettled
	return launched
}

func (l *fakeLCM) ApplyActivitySignal(_ context.Context, id domain.SessionID, signal ports.ActivitySignal) error {
	rec, ok := l.store.sessions[id]
	if !ok {
		return ports.ErrSessionNotFound
	}
	if rec.IsTerminated || !signal.Valid {
		return nil
	}
	if signal.ExpectedRevision != nil && rec.Revision != *signal.ExpectedRevision {
		return nil
	}
	if signal.LaunchID != "" && signal.LaunchID != rec.Metadata.RuntimeLaunchID {
		return nil
	}
	if signal.ControllerGeneration != "" &&
		(domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat ||
			signal.ControllerGeneration != rec.Metadata.ControllerGeneration) {
		return nil
	}
	rec.Activity = domain.Activity{State: signal.State, LastActivityAt: time.Now()}
	l.store.sessions[id] = rec
	return nil
}

type contendedActivityLCM struct {
	*fakeLCM
	calls  int
	before func(call int, id domain.SessionID, signal ports.ActivitySignal)
}

func (l *contendedActivityLCM) ApplyActivitySignal(ctx context.Context, id domain.SessionID, signal ports.ActivitySignal) error {
	l.calls++
	if l.before != nil {
		l.before(l.calls, id, signal)
	}
	return l.fakeLCM.ApplyActivitySignal(ctx, id, signal)
}

func (l *fakeLCM) CommitControllerEpoch(
	_ context.Context,
	id domain.SessionID,
	source, target domain.SessionMode,
	nativeConversationID string,
	_ bool,
) (bool, error) {
	return l.changeControllerEpoch(id, source, target, nativeConversationID, false)
}

func (l *fakeLCM) RestoreControllerEpoch(
	_ context.Context,
	id domain.SessionID,
	source, target domain.SessionMode,
	nativeConversationID string,
	_ bool,
) (bool, error) {
	return l.changeControllerEpoch(id, source, target, nativeConversationID, true)
}

func (l *fakeLCM) changeControllerEpoch(
	id domain.SessionID,
	source, target domain.SessionMode,
	nativeConversationID string,
	restore bool,
) (bool, error) {
	rec, ok := l.store.sessions[id]
	if !ok || rec.IsTerminated || domain.NormalizeSessionMode(rec.Mode) != source {
		return false, nil
	}
	rec.Mode = target
	rec.Metadata.RuntimeHandleID = ""
	rec.Metadata.RuntimeLaunchID = ""
	rec.Metadata.AgentSessionID = nativeConversationID
	rec.Metadata.ProviderConversationID = nativeConversationID
	rec.Metadata.ControllerGeneration = ""
	if !restore && target == domain.SessionModeTUI {
		rec.Metadata.LatestUserPrompt = ""
		rec.Metadata.LatestAssistantUpdate = ""
		rec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointEmpty
		rec.Metadata.ConversationCheckpointGeneration = ""
		rec.Metadata.ConversationCheckpointNativeID = ""
		rec.Metadata.ConversationCheckpointUnsettled = false
	}
	rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: time.Now()}
	l.store.sessions[id] = rec
	return true, nil
}

// MarkTerminated mirrors the real lifecycle.Manager, which refuses to write on
// a dead context. The fake used to ignore ctx entirely, which hid the fact that
// Kill was recording terminal intent on its own expiring teardown budget.
func (l *fakeLCM) MarkTerminated(ctx context.Context, id domain.SessionID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if l.terminated == nil {
		l.terminated = map[domain.SessionID]int{}
	}
	l.terminated[id]++
	rec := l.store.sessions[id]
	rec.IsTerminated = true
	rec.Activity = domain.Activity{State: domain.ActivityExited, LastActivityAt: time.Now()}
	l.store.sessions[id] = rec
	return nil
}

type fakeRuntime struct {
	createErr          error
	createErrSequence  []error
	createIDs          []string
	destroyErr         error
	destroyErrSequence []error
	onDestroy          func(call int, handle ports.RuntimeHandle)
	created, destroyed int
	lastCfg            ports.RuntimeConfig
	outputs            []string
	outputCalls        int
	outputErr          error
	interruptErr       error
	interrupts         []string
	onInterrupt        func(ports.RuntimeHandle)
	// aliveByHandle maps a RuntimeHandle.ID to its liveness; missing = false.
	aliveByHandle           map[string]bool
	aliveErr                error
	supervisedErr           error
	supervisedAliveOverride *bool
	supervisedSequence      []bool
	// exitedLaunches marks launches whose managed agent child is gone even
	// though the pane (aliveByHandle) survives, as the strict probe reports it.
	exitedLaunches map[string]bool
	destroyedIDs   []string
	fencedResult   ports.FencedProbeResult
	fencedRefs     []ports.FencedRuntimeRef
}

func (r *fakeRuntime) Interrupt(_ context.Context, handle ports.RuntimeHandle) error {
	r.interrupts = append(r.interrupts, handle.ID)
	if r.onInterrupt != nil {
		r.onInterrupt(handle)
	}
	return r.interruptErr
}

type fakePreviewLifecycle struct {
	stopped []domain.SessionID
	err     error
}

type fakeBrowserLifecycle struct {
	destroyed []domain.SessionID
	err       error
}

func (f *fakeBrowserLifecycle) DestroySession(_ context.Context, id domain.SessionID) error {
	f.destroyed = append(f.destroyed, id)
	return f.err
}

func (f *fakePreviewLifecycle) StopSession(_ context.Context, id domain.SessionID) error {
	f.stopped = append(f.stopped, id)
	return f.err
}

type fakeRestartRuntime struct {
	*fakeRuntime
	restarted     int
	restartHandle ports.RuntimeHandle
	restartErr    error
	onRestart     func()
}

func (r *fakeRestartRuntime) Restart(_ context.Context, handle ports.RuntimeHandle, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	if r.onRestart != nil {
		r.onRestart()
	}
	r.restarted++
	r.restartHandle = handle
	r.lastCfg = cfg
	if r.restartErr != nil {
		return ports.RuntimeHandle{}, r.restartErr
	}
	if r.aliveByHandle == nil {
		r.aliveByHandle = map[string]bool{}
	}
	r.aliveByHandle[handle.ID] = true
	return handle, nil
}

func (r *fakeRestartRuntime) Destroy(ctx context.Context, handle ports.RuntimeHandle) error {
	if r.onRestart != nil {
		r.onRestart()
	}
	return r.fakeRuntime.Destroy(ctx, handle)
}

type blockingRestartRuntime struct {
	*fakeRuntime
	entered chan struct{}
	release chan struct{}
}

type blockingAliveRuntime struct {
	*fakeRuntime
	entered chan domain.SessionID
	release chan struct{}
}

func (r *blockingAliveRuntime) IsAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error) {
	select {
	case r.entered <- domain.SessionID(handle.ID):
	case <-ctx.Done():
		return false, ctx.Err()
	}
	select {
	case <-r.release:
		return true, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (r *blockingRestartRuntime) Restart(_ context.Context, handle ports.RuntimeHandle, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	r.lastCfg = cfg
	close(r.entered)
	<-r.release
	return handle, nil
}

func (r *blockingRestartRuntime) Destroy(ctx context.Context, handle ports.RuntimeHandle) error {
	close(r.entered)
	<-r.release
	return r.fakeRuntime.Destroy(ctx, handle)
}

func (r *fakeRuntime) Create(_ context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	createErr := r.createErr
	if len(r.createErrSequence) > 0 {
		createErr = r.createErrSequence[0]
		r.createErrSequence = r.createErrSequence[1:]
	}
	if createErr != nil {
		return ports.RuntimeHandle{}, createErr
	}
	r.lastCfg = cfg
	r.created++
	handleID := "h1"
	if len(r.createIDs) > 0 {
		handleID = r.createIDs[0]
		r.createIDs = r.createIDs[1:]
	}
	handle := ports.RuntimeHandle{ID: handleID}
	if r.aliveByHandle == nil {
		r.aliveByHandle = map[string]bool{}
	}
	r.aliveByHandle[handle.ID] = true
	return handle, nil
}
func (r *fakeRuntime) Destroy(_ context.Context, handle ports.RuntimeHandle) error {
	call := r.destroyed
	r.destroyed++
	r.destroyedIDs = append(r.destroyedIDs, handle.ID)
	if r.onDestroy != nil {
		r.onDestroy(call, handle)
	}
	destroyErr := r.destroyErr
	if call < len(r.destroyErrSequence) {
		destroyErr = r.destroyErrSequence[call]
	}
	if destroyErr != nil {
		return destroyErr
	}
	if r.aliveByHandle != nil {
		r.aliveByHandle[handle.ID] = false
	}
	return nil
}
func (r *fakeRuntime) base() *fakeRuntime { return r }
func (r *fakeRuntime) IsAlive(_ context.Context, handle ports.RuntimeHandle) (bool, error) {
	if r.aliveErr != nil {
		return false, r.aliveErr
	}
	return r.aliveByHandle[handle.ID], nil
}
func (r *fakeRuntime) IsSupervisedProcessAlive(_ context.Context, handle ports.RuntimeHandle, _ ports.SupervisedProcessRef) (bool, error) {
	if r.supervisedErr != nil {
		return false, r.supervisedErr
	}
	if r.supervisedAliveOverride != nil {
		return *r.supervisedAliveOverride, nil
	}
	return r.aliveByHandle[handle.ID], nil
}
func (r *fakeRuntime) IsExactSupervisedProcessAlive(ctx context.Context, handle ports.RuntimeHandle, ref ports.SupervisedProcessRef) (bool, error) {
	if r.exitedLaunches[ref.LaunchID] {
		return false, nil
	}
	if len(r.supervisedSequence) > 0 {
		alive := r.supervisedSequence[0]
		r.supervisedSequence = r.supervisedSequence[1:]
		return alive, nil
	}
	return r.IsSupervisedProcessAlive(ctx, handle, ref)
}
func (r *fakeRuntime) ProbeFencedRuntime(ctx context.Context, ref ports.FencedRuntimeRef) ports.FencedProbeResult {
	r.fencedRefs = append(r.fencedRefs, ref)
	if r.fencedResult.Liveness != "" {
		return r.fencedResult
	}
	if r.aliveErr != nil || r.supervisedErr != nil {
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonProbeFailed}
	}
	alive, err := r.IsExactSupervisedProcessAlive(ctx, ref.Handle, ports.SupervisedProcessRef{
		SessionID: ref.SessionID,
		LaunchID:  ref.Generation,
	})
	if err != nil {
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonProbeFailed}
	}
	if alive {
		return ports.FencedProbeResult{Liveness: ports.FencedAlive, Reason: ports.FencedReasonExactMatch}
	}
	return ports.FencedProbeResult{Liveness: ports.FencedDead, Reason: ports.FencedReasonExactAbsent}
}
func (r *fakeRuntime) GetOutput(_ context.Context, _ ports.RuntimeHandle, _ int) (string, error) {
	r.outputCalls++
	if r.outputErr != nil {
		return "", r.outputErr
	}
	if len(r.outputs) == 0 {
		return "", nil
	}
	out := r.outputs[0]
	if len(r.outputs) > 1 {
		r.outputs = r.outputs[1:]
	}
	return out, nil
}
func (r *fakeRuntime) GetStyledOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error) {
	if r.outputErr != nil {
		return "", r.outputErr
	}
	return "", nil
}

type fakeAgent struct{}

func (fakeAgent) GetConfigSpec(context.Context) (ports.ConfigSpec, error) {
	return ports.ConfigSpec{}, nil
}
func (fakeAgent) GetLaunchCommand(context.Context, ports.LaunchConfig) ([]string, error) {
	return []string{"launch"}, nil
}
func (fakeAgent) GetPromptDeliveryStrategy(context.Context, ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	return ports.PromptDeliveryInCommand, nil
}
func (fakeAgent) GetAgentHooks(context.Context, ports.WorkspaceHookConfig) error { return nil }
func (fakeAgent) GetRestoreCommand(_ context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if id := cfg.Session.Metadata[ports.MetadataKeyAgentSessionID]; id != "" {
		return []string{"resume", id}, true, nil
	}
	return nil, false, nil
}
func (fakeAgent) SessionInfo(context.Context, ports.SessionRef) (ports.SessionInfo, bool, error) {
	return ports.SessionInfo{}, false, nil
}

type nativeTerminatingAgent struct {
	fakeAgent
	wantID    string
	err       error
	calls     int
	sharedLog *[]string
}

func (a *nativeTerminatingAgent) TerminateNativeSession(_ context.Context, session ports.SessionRef) error {
	a.calls++
	if a.sharedLog != nil {
		*a.sharedLog = append(*a.sharedLog, "TerminateNativeSession")
	}
	if got := session.Metadata[ports.MetadataKeyAgentSessionID]; got != a.wantID {
		return fmt.Errorf("native id = %q, want %q", got, a.wantID)
	}
	return a.err
}

type launchArgvAgent struct {
	fakeAgent
	argv []string
}

type runtimePreparingAgent struct {
	launchArgvAgent
	got ports.WorkspaceHookConfig
}

func (a *runtimePreparingAgent) PrepareRuntimeLaunch(_ context.Context, cfg ports.WorkspaceHookConfig) error {
	a.got = cfg
	if cfg.Env != nil {
		cfg.Env["PROVIDER_RUNTIME_PREPARED"] = "1"
	}
	return nil
}

func (a launchArgvAgent) GetLaunchCommand(context.Context, ports.LaunchConfig) ([]string, error) {
	return a.argv, nil
}

func (a launchArgvAgent) GetRestoreCommand(context.Context, ports.RestoreConfig) ([]string, bool, error) {
	return a.argv, true, nil
}

type supervisedLaunchAgent struct{ launchArgvAgent }

func (supervisedLaunchAgent) ExitDetectionMode() ports.AgentExitDetectionMode {
	return ports.AgentExitDetectionSupervisor
}

// fakeAgents resolves every harness to the same fakeAgent.
type fakeAgents struct{}

func (fakeAgents) Agent(domain.AgentHarness) (ports.Agent, bool) { return fakeAgent{}, true }

// recordingAgent captures the LaunchConfig it is handed so a test can assert the
// session manager resolved and forwarded a project's agent config.
type recordingAgent struct {
	fakeAgent
	lastConfig   ports.AgentConfig
	lastLaunch   ports.LaunchConfig
	lastRestore  ports.RestoreConfig
	launchCalls  int
	restoreCalls int
}

type launchAuthAgent struct {
	*recordingAgent
	status     ports.AgentAuthStatus
	workingDir string
	env        map[string]string
}

func (a *launchAuthAgent) ValidateLaunchAuth(_ context.Context, workingDir string, env map[string]string) (ports.AgentAuthStatus, error) {
	a.workingDir = workingDir
	a.env = maps.Clone(env)
	return a.status, nil
}

func (a *recordingAgent) GetLaunchCommand(_ context.Context, cfg ports.LaunchConfig) ([]string, error) {
	a.launchCalls++
	a.lastConfig = cfg.Config
	a.lastLaunch = cfg
	return []string{"launch"}, nil
}

func (a *recordingAgent) GetRestoreCommand(_ context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	a.restoreCalls++
	a.lastConfig = cfg.Config
	a.lastRestore = cfg
	// Mirror real adapters: with no native agent-session id to resume, signal
	// "cannot restore" so the manager falls back to a fresh launch.
	if cfg.Session.Metadata[ports.MetadataKeyAgentSessionID] == "" {
		return nil, false, nil
	}
	return []string{"resume"}, true, nil
}

type afterStartAgent struct {
	*recordingAgent
}

type selectingAfterStartAgent struct{ afterStartAgent }

type selectingInteractiveAgent struct{ selectingAfterStartAgent }

func (selectingInteractiveAgent) PrepareNativeRestore(context.Context, ports.RestoreConfig) (ports.NativeRestoreInitialization, error) {
	return nil, ports.ErrNativeHistoryUnavailable
}

type selectingRuntime struct {
	fakeRuntime
	viewport string
}

func (r *selectingRuntime) GetStyledOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	return r.viewport, nil
}

func (selectingAfterStartAgent) AfterStartCommand(_ context.Context, cfg ports.LaunchConfig) (string, error) {
	return "/model " + cfg.Config.Model, nil
}

func (selectingAfterStartAgent) AfterStartCommandApplied(cfg ports.LaunchConfig, output string) bool {
	return strings.Contains(output, "model → "+cfg.Config.Model)
}

func (a afterStartAgent) GetPromptDeliveryStrategy(context.Context, ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	return ports.PromptDeliveryAfterStart, nil
}

type readinessAgent struct {
	afterStartAgent
	hints ports.PromptReadinessHints
}

func (a readinessAgent) PromptReadinessHints(context.Context, ports.LaunchConfig) (ports.PromptReadinessHints, error) {
	return a.hints, nil
}

type composedAfterStartAgent struct {
	afterStartAgent
	buildCalls int
}

func (a *composedAfterStartAgent) BuildAfterStartPrompt(_ context.Context, cfg ports.LaunchConfig) (string, error) {
	a.buildCalls++
	return "STANDING:\n" + cfg.SystemPrompt + "\nTASK:\n" + cfg.Prompt, nil
}

type promptStrategyErrorAgent struct {
	*recordingAgent
	err error
}

func (a promptStrategyErrorAgent) GetPromptDeliveryStrategy(context.Context, ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	return "", a.err
}

type singleAgent struct{ agent ports.Agent }

func (s singleAgent) Agent(domain.AgentHarness) (ports.Agent, bool) { return s.agent, true }

type scratchHookAgent struct {
	fakeAgent
}

func (a *scratchHookAgent) GetAgentHooks(_ context.Context, cfg ports.WorkspaceHookConfig) error {
	dir := filepath.Join(cfg.WorkspacePath, ".claude")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "settings.local.json"), []byte("{}"), 0o600)
}

func (a *scratchHookAgent) GetLaunchCommand(context.Context, ports.LaunchConfig) ([]string, error) {
	return []string{"missing-agent"}, nil
}

type scratchHookAfterStartAgent struct {
	scratchHookAgent
}

func (a *scratchHookAfterStartAgent) GetLaunchCommand(context.Context, ports.LaunchConfig) ([]string, error) {
	return []string{"agent"}, nil
}

func (a *scratchHookAfterStartAgent) GetPromptDeliveryStrategy(context.Context, ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	return ports.PromptDeliveryAfterStart, nil
}

type nonEmptyWorkspace struct {
	ports.Workspace
}

func (w *nonEmptyWorkspace) Create(ctx context.Context, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, error) {
	info, err := w.Workspace.Create(ctx, cfg)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	if err := os.WriteFile(filepath.Join(info.Path, "provisioned.txt"), []byte("preserve"), 0o600); err != nil {
		return ports.WorkspaceInfo{}, err
	}
	return info, nil
}

type maxSessionNumStore struct {
	*fakeStore
}

func (s *maxSessionNumStore) CreateSession(_ context.Context, rec domain.SessionRecord) (domain.SessionRecord, error) {
	next := 1
	prefix := string(rec.ProjectID) + "-"
	for id := range s.sessions {
		if !strings.HasPrefix(string(id), prefix) {
			continue
		}
		raw := strings.TrimPrefix(string(id), prefix)
		var num int
		if _, err := fmt.Sscanf(raw, "%d", &num); err == nil && num >= next {
			next = num + 1
		}
	}
	rec.ID = domain.SessionID(fmt.Sprintf("%s-%d", rec.ProjectID, next))
	s.sessions[rec.ID] = rec
	return rec, nil
}

type cleaningAgent struct {
	fakeAgent
	cleanupCalls   int
	cleanupConfigs []ports.WorkspaceHookConfig
	sharedLog      *[]string
}

func (a *cleaningAgent) CleanupWorkspace(_ context.Context, cfg ports.WorkspaceHookConfig) error {
	a.cleanupCalls++
	a.cleanupConfigs = append(a.cleanupConfigs, cfg)
	if a.sharedLog != nil {
		*a.sharedLog = append(*a.sharedLog, "CleanupWorkspace:"+cfg.WorkspacePath)
	}
	return nil
}

type hookErrorCleaningAgent struct {
	cleaningAgent
	hookErr error
}

func (a *hookErrorCleaningAgent) GetAgentHooks(context.Context, ports.WorkspaceHookConfig) error {
	return a.hookErr
}

type envAugmentingAgent struct {
	fakeAgent
	key   string
	value string
}

func (a envAugmentingAgent) AugmentRuntimeEnv(env map[string]string, dataDir string) {
	env[a.key] = filepath.Join(dataDir, a.value)
}

func blockedDataDir(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func requireNoPromptDir(t *testing.T, dataDir string, id domain.SessionID) {
	t.Helper()
	path := filepath.Join(dataDir, "prompts", string(id))
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("prompt dir %s still exists", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat prompt dir %s: %v", path, err)
	}
}

// alwaysResumeAgent mimics Claude Code: it pins a deterministic session id, so
// GetRestoreCommand can resume any session even with no captured agentSessionId
// and no prompt.
type alwaysResumeAgent struct{ fakeAgent }

func (alwaysResumeAgent) GetRestoreCommand(_ context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	return []string{"resume", cfg.Session.ID}, true, nil
}

// missingAgents resolves no harness, simulating a typo'd or unregistered agent.
type missingAgents struct{}

func (missingAgents) Agent(domain.AgentHarness) (ports.Agent, bool) { return nil, false }

type fakeWorkspace struct {
	// restoreHook, when set, runs at the top of Restore, so a test can pause a
	// restore before its worktree is attached.
	restoreHook func()
	createErr   error
	createCount int
	destroyErr  error
	destroyed   int
	// destroyReclaim, when set, is the reclaim outcome DestroyReclaim reports.
	destroyReclaim ports.WorkspaceReclaim
	// destroyReclaimByPath overrides destroyReclaim for one workspace path, so a
	// multi-repo test can have one repo still on disk while another is already
	// gone — the state a partly cleaned workspace project is actually in.
	destroyReclaimByPath map[string]ports.WorkspaceReclaim
	// destroyCtxErr records ctx.Err() as seen by Destroy, so a test can prove
	// teardown does not inherit a caller's cancellation.
	destroyCtxErr error
	// destroyHook runs at the top of Destroy, so a test can make teardown burn
	// real time against Kill's budget.
	destroyHook func()
	fetchErr    error
	fetches     []fetchDefaultBranchCall
	resolves    []resolveDefaultBranchCall
	resolved    map[string]ports.WorkspaceDefaultBranch
	fetchFunc   func(context.Context, string, ports.WorkspaceDefaultBranch) error
	// createRepoPath, when set, is returned as the RepoPath of a single-repo
	// Create so tests can assert it survives the spawn->teardown metadata round
	// trip (production Create resolves this path; the zero default keeps every
	// other test's behavior unchanged).
	createRepoPath string
	// baseRef is the authoritative ref returned with single-repo workspaces.
	// When empty, an explicit BaseBranch is echoed to match the real adapter.
	baseRef string
	// lastDestroyInfo records the WorkspaceInfo passed to the most recent Destroy
	// so tests can assert teardown fed it the persisted repo path.
	lastDestroyInfo   ports.WorkspaceInfo
	lastCfg           ports.WorkspaceConfig
	restoreConfigs    []ports.WorkspaceConfig
	projectErr        error
	projectDestroyed  int
	lastProjectCfg    ports.WorkspaceProjectConfig
	projectCreateInfo ports.WorkspaceProjectInfo
	// path, when set, is returned as the workspace path so provisioning tests
	// can point at a real temp directory.
	path string
	// stashRef is returned by StashUncommitted (empty means clean worktree).
	stashRef        string
	stashErr        error
	applyErr        error
	forceDestroyErr error
	// stashCalls counts StashUncommitted invocations.
	stashCalls int
	stashHook  func()
	// excludePatterns records patterns passed to AddExclude; addExcludeErr, when
	// set, is returned so best-effort handling can be exercised.
	excludePatterns []string
	addExcludeErr   error
	// calls records the sequence of workspace method calls for ordering assertions.
	calls []string
	// sharedLog, when non-nil, receives entries alongside calls so ordering
	// tests can compare workspace calls against store calls in one sequence.
	sharedLog *[]string
}

type fetchDefaultBranchCall struct {
	repoPath string
	remote   string
	branch   string
	baseRef  string
}

type resolveDefaultBranchCall struct {
	repoPath         string
	configuredBranch string
}

func (w *fakeWorkspace) ResolveDefaultBranch(_ context.Context, repoPath, configuredBranch string) (ports.WorkspaceDefaultBranch, error) {
	w.resolves = append(w.resolves, resolveDefaultBranchCall{repoPath: repoPath, configuredBranch: configuredBranch})
	if target, ok := w.resolved[repoPath]; ok {
		return target, nil
	}
	branch := configuredBranch
	if branch == "" {
		branch = "main"
	}
	return ports.WorkspaceDefaultBranch{
		Remote:  "origin",
		Branch:  branch,
		BaseRef: "refs/remotes/origin/" + branch,
	}, nil
}

func (w *fakeWorkspace) FetchDefaultBranch(ctx context.Context, repoPath string, target ports.WorkspaceDefaultBranch) error {
	w.fetches = append(w.fetches, fetchDefaultBranchCall{repoPath: repoPath, remote: target.Remote, branch: target.Branch, baseRef: target.BaseRef})
	if w.sharedLog != nil {
		*w.sharedLog = append(*w.sharedLog, "FetchDefaultBranch:"+target.Remote+"/"+target.Branch)
	}
	if w.fetchFunc != nil {
		return w.fetchFunc(ctx, repoPath, target)
	}
	return w.fetchErr
}

func (w *fakeWorkspace) Create(_ context.Context, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, error) {
	w.createCount++
	if w.createErr != nil {
		return ports.WorkspaceInfo{}, w.createErr
	}
	if w.sharedLog != nil {
		*w.sharedLog = append(*w.sharedLog, "Create")
	}
	w.lastCfg = cfg
	path := w.path
	if path == "" {
		path = "/ws/" + string(cfg.SessionID)
	}
	baseRef := w.baseRef
	if baseRef == "" {
		baseRef = cfg.BaseBranch
	}
	return ports.WorkspaceInfo{Path: path, Branch: cfg.Branch, BaseRef: baseRef, SessionID: cfg.SessionID, ProjectID: cfg.ProjectID, RepoPath: w.createRepoPath}, nil
}
func (w *fakeWorkspace) CreateWorkspaceProject(_ context.Context, cfg ports.WorkspaceProjectConfig) (ports.WorkspaceProjectInfo, error) {
	if w.projectErr != nil {
		return ports.WorkspaceProjectInfo{}, w.projectErr
	}
	w.lastProjectCfg = cfg
	if len(w.projectCreateInfo.Worktrees) > 0 {
		return w.projectCreateInfo, nil
	}
	rootPath := w.path
	if rootPath == "" {
		rootPath = "/ws/" + string(cfg.SessionID)
	}
	branch := cfg.Branch
	rootBaseRef := cfg.BaseBranch
	if rootBaseRef == "" {
		rootBaseRef = "refs/remotes/origin/main"
	}
	root := ports.WorkspaceInfo{Path: rootPath, Branch: branch, BaseRef: rootBaseRef, SessionID: cfg.SessionID, ProjectID: cfg.ProjectID}
	out := ports.WorkspaceProjectInfo{
		Root: root,
		Worktrees: []ports.WorkspaceRepoInfo{{
			RepoName:  domain.RootWorkspaceRepoName,
			RepoPath:  cfg.RootRepoPath,
			Path:      rootPath,
			Branch:    branch,
			BaseSHA:   "root-base",
			BaseRef:   rootBaseRef,
			SessionID: cfg.SessionID,
			ProjectID: cfg.ProjectID,
		}},
	}
	for _, repo := range cfg.Repos {
		out.Worktrees = append(out.Worktrees, ports.WorkspaceRepoInfo{
			RepoName:     repo.Name,
			RepoPath:     repo.RepoPath,
			Path:         filepath.Join(rootPath, filepath.FromSlash(repo.RelativePath)),
			Branch:       branch,
			BaseSHA:      repo.Name + "-base",
			BaseRef:      "refs/remotes/origin/" + repo.Name,
			SessionID:    cfg.SessionID,
			ProjectID:    cfg.ProjectID,
			RelativePath: repo.RelativePath,
		})
	}
	return out, nil
}
func (w *fakeWorkspace) Destroy(ctx context.Context, info ports.WorkspaceInfo) error {
	w.lastDestroyInfo = info
	if w.destroyHook != nil {
		w.destroyHook()
	}
	w.destroyCtxErr = ctx.Err()
	if info.RepoPath != "" {
		entry := "Destroy:" + fakeWorkspaceRepoName(info)
		w.calls = append(w.calls, entry)
		if w.sharedLog != nil {
			*w.sharedLog = append(*w.sharedLog, entry)
		}
	}
	w.destroyed++
	return w.destroyErr
}

// DestroyReclaim makes the fake report reclaim outcomes like the real git
// worktree adapter. destroyReclaim is empty in every pre-existing test, which
// keeps them on the "a completed teardown released something" default.
func (w *fakeWorkspace) DestroyReclaim(ctx context.Context, info ports.WorkspaceInfo) (ports.WorkspaceReclaim, error) {
	err := w.Destroy(ctx, info)
	reclaim := w.destroyReclaim
	if override, ok := w.destroyReclaimByPath[info.Path]; ok {
		reclaim = override
	}
	if reclaim == "" {
		return ports.WorkspaceReclaimRemoved, err
	}
	return reclaim, err
}
func (w *fakeWorkspace) DestroyWorkspaceProject(context.Context, ports.WorkspaceProjectInfo) error {
	w.projectDestroyed++
	return w.destroyErr
}
func (w *fakeWorkspace) Restore(ctx context.Context, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, error) {
	if w.restoreHook != nil {
		w.restoreHook()
	}
	w.lastCfg = cfg
	w.restoreConfigs = append(w.restoreConfigs, cfg)
	if cfg.RepoPath != "" {
		entry := "Restore:" + fakeWorkspaceRepoName(ports.WorkspaceInfo{
			Path:      cfg.Path,
			SessionID: cfg.SessionID,
			RepoPath:  cfg.RepoPath,
		})
		w.calls = append(w.calls, entry)
		return ports.WorkspaceInfo{Path: cfg.Path, Branch: cfg.Branch, BaseRef: cfg.BaseRef, SessionID: cfg.SessionID, ProjectID: cfg.ProjectID, RepoPath: cfg.RepoPath}, nil
	}
	return w.Create(ctx, cfg)
}
func (w *fakeWorkspace) ForceDestroy(_ context.Context, info ports.WorkspaceInfo) error {
	entry := "ForceDestroy:" + string(info.SessionID)
	if info.RepoPath != "" {
		entry = "ForceDestroy:" + fakeWorkspaceRepoName(info)
	}
	w.calls = append(w.calls, entry)
	if w.sharedLog != nil {
		*w.sharedLog = append(*w.sharedLog, entry)
	}
	return w.forceDestroyErr
}
func (w *fakeWorkspace) StashUncommitted(_ context.Context, info ports.WorkspaceInfo) (string, error) {
	if w.stashHook != nil {
		w.stashHook()
	}
	w.stashCalls++
	entry := "StashUncommitted:" + string(info.SessionID)
	if info.RepoPath != "" {
		entry = "StashUncommitted:" + fakeWorkspaceRepoName(info)
	}
	w.calls = append(w.calls, entry)
	if w.sharedLog != nil {
		*w.sharedLog = append(*w.sharedLog, entry)
	}
	if w.stashErr != nil || w.stashRef == "" || info.RepoPath == "" {
		return w.stashRef, w.stashErr
	}
	return w.stashRef + "/" + fakeWorkspaceRepoName(info), nil
}
func (w *fakeWorkspace) ApplyPreserved(_ context.Context, info ports.WorkspaceInfo, ref string) error {
	entry := "ApplyPreserved:" + string(info.SessionID)
	if info.RepoPath != "" {
		entry = "ApplyPreserved:" + fakeWorkspaceRepoName(info) + ":" + ref
	}
	w.calls = append(w.calls, entry)
	return w.applyErr
}

func (w *fakeWorkspace) AddExclude(_ context.Context, info ports.WorkspaceInfo, patterns ...string) error {
	w.calls = append(w.calls, "AddExclude:"+string(info.SessionID))
	w.excludePatterns = append(w.excludePatterns, patterns...)
	return w.addExcludeErr
}

type loggingDestroyWorkspace struct {
	fakeWorkspace
	sharedLog *[]string
}

func (w *loggingDestroyWorkspace) Destroy(ctx context.Context, info ports.WorkspaceInfo) error {
	if w.sharedLog != nil {
		*w.sharedLog = append(*w.sharedLog, "Destroy:"+info.Path)
	}
	return w.fakeWorkspace.Destroy(ctx, info)
}

func fakeWorkspaceRepoName(info ports.WorkspaceInfo) string {
	if filepath.Base(info.Path) == string(info.SessionID) {
		return domain.RootWorkspaceRepoName
	}
	return filepath.Base(info.Path)
}

type fakeMessenger struct {
	msgs   []string
	err    error
	errFor func(domain.SessionID, string) error
	onSend func(domain.SessionID, string)
}

func (m *fakeMessenger) Send(_ context.Context, id domain.SessionID, msg string) error {
	m.msgs = append(m.msgs, msg)
	if m.onSend != nil {
		m.onSend(id, msg)
	}
	if m.errFor != nil {
		return m.errFor(id, msg)
	}
	return m.err
}

func newManager() (*Manager, *fakeStore, *fakeRuntime, *fakeWorkspace) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{}
	ws := &fakeWorkspace{}
	// Stub lookPath so the pre-launch agent-binary check passes; the fakeAgent
	// returns argv ["launch"] which is not a real binary on PATH.
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: rt, Agents: fakeAgents{}, Workspace: ws, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})
	return m, st, rt, ws
}
func testRoleAgents() domain.ProjectConfig {
	return domain.ProjectConfig{
		Worker:       domain.RoleOverride{Harness: domain.HarnessClaudeCode},
		Orchestrator: domain.RoleOverride{Harness: domain.HarnessClaudeCode},
	}
}

func newManagerGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runManagerGit(t, dir, "init")
	runManagerGit(t, dir, "config", "user.email", "tallpine@example.com")
	runManagerGit(t, dir, "config", "user.name", "Tallpine Tests")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runManagerGit(t, dir, "add", ".")
	runManagerGit(t, dir, "commit", "-m", "initial")
	runManagerGit(t, dir, "branch", "-M", "main")
	return dir
}

func runManagerGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git -C %s %s: %v\n%s", dir, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func seedTerminal(st *fakeStore, id domain.SessionID, meta domain.SessionMetadata) {
	st.sessions[id] = domain.SessionRecord{ID: id, ProjectID: "mer", Metadata: meta, IsTerminated: true, Activity: domain.Activity{State: domain.ActivityExited}}
}
func mkLive(id domain.SessionID) domain.SessionRecord {
	return domain.SessionRecord{ID: id, ProjectID: "mer", Metadata: domain.SessionMetadata{WorkspacePath: "/ws/" + string(id), RuntimeHandleID: "h1"}, Activity: domain.Activity{State: domain.ActivityActive}}
}

type rejectingHarnessUseGate struct {
	harness domain.AgentHarness
}

func (g *rejectingHarnessUseGate) TryBeginHarnessUse(harness domain.AgentHarness) (func(), bool) {
	g.harness = harness
	return nil, false
}

func newExitedResumeManager(t *testing.T, runtime runtimeController, agent ports.Agent) (*Manager, *fakeStore, *fakeWorkspace) {
	t.Helper()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessCodex,
		Activity:  domain.Activity{State: domain.ActivityExited},
		Metadata: domain.SessionMetadata{
			WorkspacePath:   "/ws/mer-1",
			Branch:          "tp/mer-1",
			RuntimeHandleID: "tmux-mer-1",
			RuntimeLaunchID: "launch-old",
			AgentSessionID:  "agent-x",
			Prompt:          "continue the task",
		},
	}
	ws := &fakeWorkspace{}
	m := New(Deps{
		Runtime: runtime, Agents: singleAgent{agent: agent}, Workspace: ws, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		DataDir:     t.TempDir(),
		LookPath:    func(string) (string, error) { return "/bin/true", nil },
		Executable:  func() (string, error) { return "/opt/tallpine", nil },
		NewLaunchID: func() string { return "launch-new" },
	})
	// The exited fixture's pane may survive, but its launched agent has exited.
	if fake, ok := runtime.(interface{ base() *fakeRuntime }); ok {
		if fake.base().exitedLaunches == nil {
			fake.base().exitedLaunches = map[string]bool{}
		}
		fake.base().exitedLaunches["launch-old"] = true
	}
	return m, st, ws
}

type deadlineConsumingRuntime struct {
	*fakeRuntime
}

func (r *deadlineConsumingRuntime) Destroy(ctx context.Context, handle ports.RuntimeHandle) error {
	r.destroyed++
	r.destroyedIDs = append(r.destroyedIDs, handle.ID)
	<-ctx.Done()
	return ctx.Err()
}

type cancelOnDeliverMessenger struct {
	cancel context.CancelFunc
}

func (m *cancelOnDeliverMessenger) Send(context.Context, domain.SessionID, string) error {
	m.cancel()
	return context.DeadlineExceeded
}

type cancelingCreateWorkspace struct {
	*fakeWorkspace
	cancel context.CancelFunc
}

func (w *cancelingCreateWorkspace) Create(context.Context, ports.WorkspaceConfig) (ports.WorkspaceInfo, error) {
	w.cancel()
	return ports.WorkspaceInfo{}, context.DeadlineExceeded
}

type contextAwareDeleteStore struct {
	*fakeStore
	deleteContextErr error
}

func (s *contextAwareDeleteStore) DeleteSession(ctx context.Context, id domain.SessionID) (bool, error) {
	s.deleteContextErr = ctx.Err()
	if s.deleteContextErr != nil {
		return false, s.deleteContextErr
	}
	return s.fakeStore.DeleteSession(ctx, id)
}

// terminatedOnReReadStore wraps fakeStore and reports the spawned session as
// terminated every time GetSession is called AFTER CreateSession, so the guard
// re-reads a terminated row and suppresses the after-start prompt delivery.
type terminatedOnReReadStore struct {
	*fakeStore
	spawned domain.SessionID
	saw     bool
}

func (s *terminatedOnReReadStore) CreateSession(ctx context.Context, rec domain.SessionRecord) (domain.SessionRecord, error) {
	out, err := s.fakeStore.CreateSession(ctx, rec)
	// fakeStore assigns the "{project}-{n}" id inside CreateSession, so capture
	// it from the returned record.
	s.spawned = out.ID
	s.saw = true
	return out, err
}

func (s *terminatedOnReReadStore) GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	// Once spawn created the row, surface it as terminated so the guard's
	// just-in-time re-read sees a dead session and suppresses the write.
	if s.saw && id == s.spawned {
		rec := s.sessions[id]
		rec.IsTerminated = true
		rec.Activity.State = domain.ActivityExited
		return rec, true, nil
	}
	return s.fakeStore.GetSession(ctx, id)
}

type possibleRuntimeEffect struct {
	err     error
	handle  ports.RuntimeHandle
	effect  ports.RuntimeEffectOutcome
	cleanup ports.RuntimeCleanupOutcome
}

func (e possibleRuntimeEffect) Error() string                               { return e.err.Error() }
func (e possibleRuntimeEffect) Unwrap() error                               { return e.err }
func (e possibleRuntimeEffect) PossibleHandle() ports.RuntimeHandle         { return e.handle }
func (e possibleRuntimeEffect) EffectOutcome() ports.RuntimeEffectOutcome   { return e.effect }
func (e possibleRuntimeEffect) CleanupOutcome() ports.RuntimeCleanupOutcome { return e.cleanup }

// TestKill_TerminatesIncompleteHandle: a session whose runtime handle or
// workspace path is missing is still terminated — the destroy steps are
// skipped, but the session moves to terminal state so it can be cleaned up
// from the dashboard.
// fakeShellTerminalCloser stands in for shellterm.Service in ordering tests:
// it records which sessions it was asked to begin/end teardown for, and —
// when sharedLog is set — appends into the same call sequence a fakeWorkspace
// (and fakeStore) logs into, so a test can assert shell terminals are drained
// BEFORE the worktree they point at is torn down, and the gate is released
// AFTER.
type fakeShellTerminalCloser struct {
	began     []domain.SessionID
	ended     []domain.SessionID
	err       error
	sharedLog *[]string
}

func (f *fakeShellTerminalCloser) BeginSessionTeardown(_ context.Context, id domain.SessionID) (func(), error) {
	f.began = append(f.began, id)
	if f.err != nil {
		return nil, f.err
	}
	if f.sharedLog != nil {
		*f.sharedLog = append(*f.sharedLog, "BeginSessionTeardown:"+string(id))
	}
	return func() {
		f.ended = append(f.ended, id)
		if f.sharedLog != nil {
			*f.sharedLog = append(*f.sharedLog, "EndSessionTeardown:"+string(id))
		}
	}, nil
}

type fakeReviewerTerminator struct {
	calls         []domain.SessionID
	bodies        []string
	teardownCalls []domain.SessionID
	restoreCalls  []domain.SessionID
	err           error
	teardownErr   error
	restoreErr    error
	sharedLog     *[]string
}

func (f *fakeReviewerTerminator) TerminateReviewer(_ context.Context, id domain.SessionID, body string) error {
	f.calls = append(f.calls, id)
	f.bodies = append(f.bodies, body)
	if f.sharedLog != nil {
		*f.sharedLog = append(*f.sharedLog, "TerminateReviewer:"+string(id))
	}
	return f.err
}

func (f *fakeReviewerTerminator) TeardownReviewerTerminal(_ context.Context, id domain.SessionID) error {
	f.teardownCalls = append(f.teardownCalls, id)
	if f.sharedLog != nil {
		*f.sharedLog = append(*f.sharedLog, "TeardownReviewerTerminal:"+string(id))
	}
	return f.teardownErr
}

func (f *fakeReviewerTerminator) RestoreReviewer(_ context.Context, id domain.SessionID) error {
	f.restoreCalls = append(f.restoreCalls, id)
	if f.sharedLog != nil {
		*f.sharedLog = append(*f.sharedLog, "RestoreReviewer:"+string(id))
	}
	return f.restoreErr
}

// lostConversationAgent reserves a native id but reports that no conversation
// was ever persisted behind it — a session torn down before its first turn, or
// one whose switch away failed after the source had already stopped.
type lostConversationAgent struct{ *recordingAgent }

func (lostConversationAgent) NativeConversationExists(
	context.Context, ports.SessionRef, string, map[string]string,
) (bool, error) {
	return false, nil
}

type lostComposedAfterStartAgent struct{ *composedAfterStartAgent }

func (lostComposedAfterStartAgent) NativeConversationExists(
	context.Context, ports.SessionRef, string, map[string]string,
) (bool, error) {
	return false, nil
}

type lostDerivedConversationAgent struct {
	fakeAgent
	probedID string
}

func (*lostDerivedConversationAgent) GetRestoreCommand(
	context.Context, ports.RestoreConfig,
) ([]string, bool, error) {
	return []string{"resume", "derived-but-empty"}, true, nil
}

func (*lostDerivedConversationAgent) NativeConversationID(
	context.Context, ports.SessionRef, domain.SessionMode, string,
) (string, bool, error) {
	return "derived-but-empty", true, nil
}

func (a *lostDerivedConversationAgent) NativeConversationExists(
	_ context.Context, _ ports.SessionRef, conversationID string, _ map[string]string,
) (bool, error) {
	a.probedID = conversationID
	return false, nil
}

type preLaunchRecordingAgent struct {
	*recordingAgent
	preLaunch ports.LaunchConfig
}

func (a *preLaunchRecordingAgent) PreLaunch(_ context.Context, cfg ports.LaunchConfig) error {
	a.preLaunch = cfg
	a.preLaunch.Env = maps.Clone(cfg.Env)
	return nil
}

// pathPinManager builds a manager whose Executable dep is stubbed, plus a
// buffer capturing its log output, for the hook PATH pin tests.
func pathPinManager(executable func() (string, error)) (*Manager, *fakeStore, *fakeRuntime, *bytes.Buffer) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{}
	logBuf := &bytes.Buffer{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{
		Runtime: rt, Agents: fakeAgents{}, Workspace: &fakeWorkspace{}, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: lookPath, Executable: executable,
		Logger: slog.New(slog.NewTextHandler(logBuf, nil)),
	})
	return m, st, rt, logBuf
}

// newLifecycleManager builds a manager wired with a recording workspace fake
// for the shutdown lifecycle tests.
func newLifecycleManager() (*Manager, *fakeStore, *fakeRuntime, *fakeWorkspace) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{}
	ws := &fakeWorkspace{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{
		Runtime:   rt,
		Agents:    fakeAgents{},
		Workspace: ws,
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		LookPath:  lookPath,
	})
	return m, st, rt, ws
}

func seedNativeWorkspaceProject(st *fakeStore, id domain.SessionID, kind domain.SessionKind) domain.SessionRecord {
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repos/mer", Kind: domain.ProjectKindWorkspace, Config: testRoleAgents()}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{
		ProjectID: "mer", Name: "api", RelativePath: "api",
	}}
	rec := domain.SessionRecord{
		ID: id, ProjectID: "mer", Kind: kind,
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/" + string(id), Branch: "tp/" + string(id),
			RuntimeHandleID: "runtime-" + string(id), AgentSessionID: "native-7",
		},
		Activity: domain.Activity{State: domain.ActivityActive},
	}
	st.sessions[id] = rec
	st.worktrees[id] = []domain.SessionWorktreeRecord{
		{SessionID: id, RepoName: domain.RootWorkspaceRepoName, Branch: rec.Metadata.Branch, WorktreePath: rec.Metadata.WorkspacePath, State: "active"},
		{SessionID: id, RepoName: "api", Branch: rec.Metadata.Branch, WorktreePath: rec.Metadata.WorkspacePath + "/api", State: "active"},
	}
	return rec
}

// signalingAgent is a fakeAgent that advertises BOTH a prompt-submit and a
// blocked activity signal, so Manager.Send runs confirmActive for its harness
// (see ports.SubmitActivitySignaler and ports.BlockedActivitySignaler).
type signalingAgent struct{ fakeAgent }

func (signalingAgent) EmitsSubmitActivity() bool  { return true }
func (signalingAgent) EmitsBlockedActivity() bool { return true }

type semanticSignalingAgent struct{ signalingAgent }

func (semanticSignalingAgent) EmitsSemanticMessageAcceptance() bool { return true }

type startupReadySignalingAgent struct{ fakeAgent }

func (startupReadySignalingAgent) FirstSignalProvesInputReady() bool { return true }

// submitOnlyAgent advertises a prompt-submit signal but NOT a blocked one — a
// harness like goose/opencode/agy that submits yet installs no permission hook.
// confirmActive must refuse to nudge it (it could Enter into a decision the
// harness cannot report).
type submitOnlyAgent struct{ fakeAgent }

func (submitOnlyAgent) EmitsSubmitActivity() bool  { return true }
func (submitOnlyAgent) EmitsBlockedActivity() bool { return false }

// newSendTestManager builds a Manager wired for Send confirmation tests with
// fast (millisecond) confirmation timings so no test waits real seconds. The
// returned messenger records every Send; the store is mutable so a test can
// flip Activity.State between polls.
func newSendTestManager(t *testing.T, agent ports.Agent, messenger ports.AgentMessenger, st *fakeStore) *Manager {
	t.Helper()
	rt := &fakeRuntime{}
	ws := &fakeWorkspace{}
	lcm := &fakeLCM{store: st}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent}, Workspace: ws, Store: st,
		Messenger: messenger, Lifecycle: lcm, LookPath: lookPath,
	})
	// Shrink the confirmation budget so the loop runs in milliseconds, not
	// seconds. m.sendConfirm is unexported; tests live in this package.
	m.sendConfirm = sendConfirmConfig{
		pollInterval:    time.Millisecond,
		attemptDeadline: 2 * time.Millisecond,
		maxAttempts:     3,
	}
	return m
}

// pastStartupGate marks a TUI session as having received its first hook
// callback so send-path tests exercise post-startup behavior.
func pastStartupGate(rec domain.SessionRecord) domain.SessionRecord {
	if rec.FirstSignalAt.IsZero() {
		rec.FirstSignalAt = time.Now()
	}
	return rec
}

type activityProjectionBarrierStore struct {
	*sqlite.Store
	updateEntered chan struct{}
	releaseUpdate chan struct{}
	blockOnce     sync.Once
	barrierMu     sync.Mutex
	skipUpdates   int
}

func (s *activityProjectionBarrierStore) UpdateSessionFromActivitySignal(
	ctx context.Context,
	rec domain.SessionRecord,
	expectedRevision int64,
) (bool, error) {
	s.barrierMu.Lock()
	if s.skipUpdates > 0 {
		s.skipUpdates--
		s.barrierMu.Unlock()
		return s.Store.UpdateSessionFromActivitySignal(ctx, rec, expectedRevision)
	}
	blocked := false
	s.blockOnce.Do(func() {
		blocked = true
		close(s.updateEntered)
	})
	s.barrierMu.Unlock()
	if blocked {
		select {
		case <-s.releaseUpdate:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	return s.Store.UpdateSessionFromActivitySignal(ctx, rec, expectedRevision)
}

// blockOnSendMessenger records sends and flips the session to ActivityBlocked
// right after the initial message is delivered, simulating a prompt that
// immediately triggers a tool-permission dialog.
type blockOnSendMessenger struct {
	msgs      []string
	sessionID domain.SessionID
	store     *fakeStore
}

func (m *blockOnSendMessenger) Send(_ context.Context, _ domain.SessionID, msg string) error {
	m.msgs = append(m.msgs, msg)
	if rec, ok := m.store.sessions[m.sessionID]; ok {
		rec.Activity.State = domain.ActivityBlocked
		m.store.sessions[m.sessionID] = rec
	}
	return nil
}

// blockAfterFirstReadStore wraps fakeStore and flips the session to
// ActivityBlocked on the FOURTH GetSession call, so with attemptDeadline 0 the
// first read to observe blocked is confirmActive's just-in-time pre-nudge
// re-read (reads #1-#3 are Deliver's pre-paste read, Send's harness lookup,
// and waitForActive's single poll — see TestSend_NoNudgeWhenBlockedAppearsBeforeNudge).
type blockAfterFirstReadStore struct {
	*fakeStore
	id    domain.SessionID
	reads int
}

func (s *blockAfterFirstReadStore) GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	s.reads++
	if s.reads >= 4 {
		if rec, ok := s.sessions[s.id]; ok {
			rec.Activity.State = domain.ActivityBlocked
			s.sessions[s.id] = rec
		}
	}
	return s.fakeStore.GetSession(ctx, id)
}

// flipOnNudgeMessenger records sends like fakeMessenger and additionally flips a
// session to ActivityActive the first time it receives an Enter-only nudge (an
// empty message), simulating the agent accepting the prompt after the retry.
type flipOnNudgeMessenger struct {
	msgs      []string
	sessionID domain.SessionID
	store     *fakeStore
	flipped   bool
}

func (m *flipOnNudgeMessenger) Send(_ context.Context, _ domain.SessionID, msg string) error {
	m.msgs = append(m.msgs, msg)
	if msg == "" && !m.flipped {
		rec, ok := m.store.sessions[m.sessionID]
		if ok {
			rec.Activity.State = domain.ActivityActive
			m.store.sessions[m.sessionID] = rec
		}
		m.flipped = true
	}
	return nil
}

// advancePromptOnNudgeMessenger models an Enter-only nudge whose prompt is
// accepted (UserPromptSubmit advances LatestUserPromptAt) while the turn
// completes within the poll gap, so Activity never settles on Active.
type advancePromptOnNudgeMessenger struct {
	msgs      []string
	sessionID domain.SessionID
	store     *fakeStore
	at        time.Time
	advanced  bool
}

func (m *advancePromptOnNudgeMessenger) Send(_ context.Context, _ domain.SessionID, msg string) error {
	m.msgs = append(m.msgs, msg)
	if msg == "" && !m.advanced {
		if rec, ok := m.store.sessions[m.sessionID]; ok {
			rec.Metadata.LatestUserPromptAt = m.at
			m.store.sessions[m.sessionID] = rec
		}
		m.advanced = true
	}
	return nil
}

type interactionCountingStore struct {
	*fakeStore
	calls int
}

func (s *interactionCountingStore) RecordSessionInteraction(context.Context, domain.SessionID, string, time.Time) error {
	s.calls++
	return nil
}

func (f *fakeStore) UpsertSessionCleanupFacts(_ context.Context, rec domain.SessionCleanupRecord) error {
	f.cleanupMu.Lock()
	defer f.cleanupMu.Unlock()
	if f.cleanupFacts == nil {
		f.cleanupFacts = make(map[domain.SessionID]domain.SessionCleanupRecord)
	}
	f.cleanupFacts[rec.SessionID] = rec
	return nil
}
func (f *fakeStore) GetSessionCleanupFacts(_ context.Context, id domain.SessionID) (domain.SessionCleanupRecord, bool, error) {
	f.cleanupMu.Lock()
	defer f.cleanupMu.Unlock()
	rec, ok := f.cleanupFacts[id]
	return rec, ok, nil
}

func (s *fakeStore) SetSessionAgentExitReason(_ context.Context, id domain.SessionID, reason domain.AgentExitReason) error {
	s.exitMu.Lock()
	defer s.exitMu.Unlock()
	if s.exitReasons == nil {
		s.exitReasons = map[domain.SessionID]domain.AgentExitReason{}
	}
	if reason == domain.AgentExitUnknown {
		delete(s.exitReasons, id)
	} else {
		s.exitReasons[id] = reason
	}
	return nil
}

func (s *fakeStore) SessionAgentExitReason(_ context.Context, id domain.SessionID) (domain.AgentExitReason, error) {
	s.exitMu.Lock()
	defer s.exitMu.Unlock()
	return s.exitReasons[id], nil
}
