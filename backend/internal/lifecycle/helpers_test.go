package lifecycle

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

var ctx = context.Background()

type fakeStore struct {
	sessions   map[domain.SessionID]domain.SessionRecord
	projects   map[string]domain.ProjectRecord
	prs        map[domain.SessionID][]domain.PullRequest
	reviews    map[string][]domain.PullRequestReview
	comments   map[string][]domain.PullRequestComment
	prPolicies map[string]bool
	signatures map[string]string

	listPRsErr        error
	listReviewsErr    error
	signatureWriteErr error
	signatureWrites   int
	chatSpawnErr      error
	chatSpawnCalls    []domain.ConversationBranch
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		sessions:   map[domain.SessionID]domain.SessionRecord{},
		projects:   map[string]domain.ProjectRecord{},
		prs:        map[domain.SessionID][]domain.PullRequest{},
		reviews:    map[string][]domain.PullRequestReview{},
		comments:   map[string][]domain.PullRequestComment{},
		prPolicies: map[string]bool{},
		signatures: map[string]string{},
	}
}

func (f *fakeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	r, ok := f.sessions[id]
	return r, ok, nil
}

func (f *fakeStore) ListPRsBySession(_ context.Context, id domain.SessionID) ([]domain.PullRequest, error) {
	if f.listPRsErr != nil {
		return nil, f.listPRsErr
	}
	return f.prs[id], nil
}

func (f *fakeStore) GetPR(_ context.Context, prURL string) (domain.PullRequest, bool, error) {
	for _, prs := range f.prs {
		for _, pr := range prs {
			if pr.URL != prURL {
				continue
			}
			if policy, ok := f.prPolicies[prURL]; ok {
				pr.AutoInjectCI = policy
			} else {
				pr.AutoInjectCI = true
			}
			return pr, true, nil
		}
	}
	policy, explicitlySet := f.prPolicies[prURL]
	if !explicitlySet {
		policy = true
	}
	return domain.PullRequest{URL: prURL, AutoInjectCI: policy}, true, nil
}

func (f *fakeStore) ListPRReviews(_ context.Context, prURL string) ([]domain.PullRequestReview, error) {
	if f.listReviewsErr != nil {
		return nil, f.listReviewsErr
	}
	return append([]domain.PullRequestReview(nil), f.reviews[prURL]...), nil
}

func (f *fakeStore) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	rec, ok := f.projects[id]
	return rec, ok, nil
}

func (f *fakeStore) ListPRComments(_ context.Context, prURL string) ([]domain.PullRequestComment, error) {
	return append([]domain.PullRequestComment(nil), f.comments[prURL]...), nil
}

func (f *fakeStore) ListSessions(_ context.Context, project domain.ProjectID) ([]domain.SessionRecord, error) {
	var out []domain.SessionRecord
	for _, rec := range f.sessions {
		if rec.ProjectID == project {
			out = append(out, rec)
		}
	}
	return out, nil
}

func (f *fakeStore) UpdateSession(_ context.Context, rec domain.SessionRecord) error {
	rec.Revision = f.sessions[rec.ID].Revision + 1
	f.sessions[rec.ID] = rec
	return nil
}

func (f *fakeStore) UpdateSessionArtifactOutput(_ context.Context, id domain.SessionID, artifactDir string, outputType domain.SessionOutputType) (bool, error) {
	rec, ok := f.sessions[id]
	if !ok {
		return false, nil
	}
	rec.Metadata.ArtifactDir = artifactDir
	rec.OutputType = outputType
	f.sessions[id] = rec
	return true, nil
}

func (f *fakeStore) UpdateSessionFromActivitySignal(_ context.Context, rec domain.SessionRecord, expected int64) (bool, error) {
	if f.sessions[rec.ID].Revision != expected {
		return false, nil
	}
	rec.Revision = expected + 1
	f.sessions[rec.ID] = rec
	return true, nil
}

type activityRevisionConflictStore struct {
	*fakeStore
	conflictNext bool
}

func (f *activityRevisionConflictStore) UpdateSessionFromActivitySignal(
	ctx context.Context,
	rec domain.SessionRecord,
	expectedRevision int64,
) (bool, error) {
	if f.conflictNext {
		f.conflictNext = false
		current := f.sessions[rec.ID]
		current.Revision = expectedRevision + 1
		f.sessions[rec.ID] = current
		return false, nil
	}
	return f.fakeStore.UpdateSessionFromActivitySignal(ctx, rec, expectedRevision)
}

func (f *fakeStore) CommitChatSpawn(
	_ context.Context,
	rec domain.SessionRecord,
	boundary domain.ConversationBranch,
) error {
	if f.chatSpawnErr != nil {
		return f.chatSpawnErr
	}
	f.chatSpawnCalls = append(f.chatSpawnCalls, boundary)
	rec.Revision = f.sessions[rec.ID].Revision + 1
	f.sessions[rec.ID] = rec
	return nil
}

func (f *fakeStore) CommitSessionControllerEpoch(
	_ context.Context,
	id domain.SessionID,
	source, target domain.SessionMode,
	nativeConversationID string,
	now time.Time,
) (bool, error) {
	return f.changeSessionControllerEpoch(id, source, target, nativeConversationID, now, false)
}

func (f *fakeStore) RestoreSessionControllerEpoch(
	_ context.Context,
	id domain.SessionID,
	source, target domain.SessionMode,
	nativeConversationID string,
	now time.Time,
) (bool, error) {
	return f.changeSessionControllerEpoch(id, source, target, nativeConversationID, now, true)
}

func (f *fakeStore) changeSessionControllerEpoch(
	id domain.SessionID,
	source, target domain.SessionMode,
	nativeConversationID string,
	now time.Time,
	restore bool,
) (bool, error) {
	rec, ok := f.sessions[id]
	if !ok || rec.IsTerminated || domain.NormalizeSessionMode(rec.Mode) != source {
		return false, nil
	}
	rec.Mode = target
	rec.Metadata.RuntimeHandleID = ""
	rec.Metadata.RuntimeLaunchID = ""
	rec.Metadata.AgentSessionID = nativeConversationID
	rec.Metadata.AgentSessionIDLaunchID = ""
	rec.Metadata.ProviderConversationID = nativeConversationID
	rec.Metadata.ControllerGeneration = ""
	if !restore && target == domain.SessionModeTUI {
		rec.Metadata.LatestUserPrompt = ""
		rec.Metadata.LatestAssistantUpdate = ""
		rec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointEmpty
		rec.Metadata.ConversationCheckpointGeneration = ""
		rec.Metadata.ConversationCheckpointNativeID = ""
	}
	rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}
	rec.UpdatedAt = now
	rec.Revision++
	f.sessions[id] = rec
	return true, nil
}

func (f *fakeStore) GetPRLastNudgeSignature(_ context.Context, prURL string) (string, error) {
	return f.signatures[prURL], nil
}

func (f *fakeStore) UpdatePRLastNudgeSignature(_ context.Context, prURL, payload string) error {
	if f.signatureWriteErr != nil {
		return f.signatureWriteErr
	}
	if f.signatures == nil {
		f.signatures = map[string]string{}
	}
	f.signatures[prURL] = payload
	f.signatureWrites++
	return nil
}

type fakeMessenger struct {
	msgs []string
	ids  []domain.SessionID
	err  error
}

type fakeCompletionTerminator struct {
	calls int
	err   error
}

func (f *fakeCompletionTerminator) Kill(_ context.Context, _ domain.SessionID) (bool, error) {
	f.calls++
	return true, f.err
}

type fixedSessionOperationGate bool

func (g fixedSessionOperationGate) SessionMutationInProgress(domain.SessionID) bool {
	return bool(g)
}

type fixedLifecycleInputLease bool

func (l fixedLifecycleInputLease) AcquireSessionInput(domain.SessionID) (func(), bool) {
	if !l {
		return nil, false
	}
	return func() {}, true
}

func (f *fakeMessenger) Send(_ context.Context, id domain.SessionID, msg string) error {
	if f.err != nil {
		return f.err
	}
	f.msgs = append(f.msgs, msg)
	f.ids = append(f.ids, id)
	return nil
}

func newManager(opts ...Option) (*Manager, *fakeStore, *fakeMessenger) {
	st := newFakeStore()
	msg := &fakeMessenger{}
	return New(st, msg, opts...), st, msg
}

func working(id domain.SessionID) domain.SessionRecord {
	return domain.SessionRecord{
		ID: id, ProjectID: "mer",
		Activity:         domain.Activity{State: domain.ActivityActive, LastActivityAt: time.Now()},
		AutoInjectReview: true,
		FirstSignalAt:    time.Now(),
	}
}

// idleWorker seeds a worker between turns, the only state that receives review
// comments; a worker mid-turn has them deferred to the turn's end.
func idleWorker(id domain.SessionID) domain.SessionRecord {
	rec := working(id)
	rec.Activity.State = domain.ActivityIdle
	return rec
}

// exited seeds a session whose agent has genuinely QUIESCED (ActivityExited) —
// the agent came down and is provably resting/idle, not mid-climb. This is the
// fixture the merged-PR termination contract is meant to be exercised against:
// flag-termination of a session is legitimate only when the agent has actually
// stopped working (quiesced), NOT while it is still ActivityActive (#2879).
// Here the merged lane's sessionComplete must still terminate an agent that
// merged its PR and then genuinely exited.
func exited(id domain.SessionID) domain.SessionRecord {
	rec := working(id)
	rec.Activity = domain.Activity{State: domain.ActivityExited, LastActivityAt: time.Now()}
	return rec
}

type fakeUsageFinalizer struct {
	store           *fakeStore
	calls           int
	sawTerminated   bool
	launchID        string
	sessionRevision int64
	err             error
	onFinalize      func(domain.SessionID, string, int64) error
}

func (f *fakeUsageFinalizer) FinalizeSession(
	_ context.Context,
	id domain.SessionID,
	launchID string,
	sessionRevision int64,
) error {
	f.calls++
	f.sawTerminated = f.store.sessions[id].IsTerminated
	f.launchID = launchID
	f.sessionRevision = sessionRevision
	if f.onFinalize != nil {
		return f.onFinalize(id, launchID, sessionRevision)
	}
	return f.err
}

type fakeUsageLifecycle struct {
	fakeUsageFinalizer
	reactivateCalls  int
	reactivateID     domain.SessionID
	reactivateLaunch string
	sawLive          bool
}

func (f *fakeUsageLifecycle) ReactivateSession(
	_ context.Context,
	id domain.SessionID,
	launchID string,
) error {
	f.reactivateCalls++
	f.reactivateID = id
	f.reactivateLaunch = launchID
	f.sawLive = !f.store.sessions[id].IsTerminated
	return nil
}

type fakeNotificationSink struct {
	intents     []ports.NotificationIntent
	resolutions []ports.NotificationResolution
	err         error
}

func (f *fakeNotificationSink) Notify(_ context.Context, intent ports.NotificationIntent) error {
	f.intents = append(f.intents, intent)
	return f.err
}

func (f *fakeNotificationSink) Resolve(_ context.Context, res ports.NotificationResolution) error {
	f.resolutions = append(f.resolutions, res)
	return f.err
}

// blockOnNthGetStore wraps fakeStore and flips a session to ActivityBlocked on
// the Nth GetSession call, reproducing the reactions TOCTOU: the handler's
// entry guard (1st read) sees the session working, but a permission hook stores
// blocked before sendOnce's just-in-time re-read (2nd read).
type blockOnNthGetStore struct {
	*fakeStore
	id     domain.SessionID
	reads  int
	flipAt int
}

func (s *blockOnNthGetStore) GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	s.reads++
	if s.reads == s.flipAt {
		if rec, ok := s.sessions[s.id]; ok {
			rec.Activity.State = domain.ActivityBlocked
			s.sessions[s.id] = rec
		}
	}
	return s.fakeStore.GetSession(ctx, id)
}

// fakeLifecycleContainerReaper is a minimal ports.ContainerReaper test double.
type fakeLifecycleContainerReaper struct {
	sessions []domain.SessionID
	removed  int
	err      error
}

func (f *fakeLifecycleContainerReaper) ReapSessionContainers(_ context.Context, id domain.SessionID) (int, error) {
	f.sessions = append(f.sessions, id)
	return f.removed, f.err
}

// fakeProjectConfigLoader is a minimal projectConfigLoader test double.
type fakeProjectConfigLoader struct {
	projects map[string]domain.ProjectRecord
	err      error
}

func (f *fakeProjectConfigLoader) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	if f.err != nil {
		return domain.ProjectRecord{}, false, f.err
	}
	rec, ok := f.projects[id]
	return rec, ok, nil
}

func newManagerWithContainerReaper(cr ports.ContainerReaper, pl projectConfigLoader) (*Manager, *fakeStore, *fakeMessenger) {
	st := newFakeStore()
	msg := &fakeMessenger{}
	m := New(st, msg, WithContainerReaper(cr, pl))
	return m, st, msg
}
