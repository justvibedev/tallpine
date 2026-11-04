package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

// Spawn creates a session and returns the API-facing read model plus
// ephemeral prompt size measurements.
func (s *Service) Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error) {
	if rec, found, err := s.replayClientRequest(ctx, cfg.ClientRequestID, cfg.ClientRequestHash); err != nil {
		return domain.Session{}, 0, 0, err
	} else if found {
		sess, err := s.toSession(ctx, rec)
		return sess, 0, 0, err
	}
	if cfg.ClientRequestID != "" && cfg.Kind != domain.KindWorker {
		return domain.Session{}, 0, 0, apierr.Invalid("CLIENT_REQUEST_WORKER_REQUIRED", "clientRequestId is supported for worker sessions only", nil)
	}
	if cfg.ProjectID == "" && cfg.Kind != domain.KindWorker {
		return domain.Session{}, 0, 0, apierr.Invalid("STANDALONE_WORKER_REQUIRED", "Standalone sessions must be workers", nil)
	}
	if cfg.Kind == domain.KindOrchestrator {
		cfg.Async = true
		unlock := s.lockOrchestratorProject(cfg.ProjectID)
		defer unlock()

		existing, err := s.activeOrchestrators(ctx, cfg.ProjectID, cfg.LaneItemID)
		if err != nil {
			return domain.Session{}, 0, 0, err
		}
		if len(existing) > 0 {
			if cfg.AutomationRunID != nil {
				for _, candidate := range existing {
					if candidate.AutomationRunID != nil && *candidate.AutomationRunID == *cfg.AutomationRunID {
						return candidate, 0, 0, nil
					}
				}
				return domain.Session{}, 0, 0, apierr.Conflict(
					"ORCHESTRATOR_ALREADY_ACTIVE",
					"Another orchestrator is already active for this project",
					nil,
				)
			}
			return newestSession(existing), 0, 0, nil
		}
	}
	return s.spawn(ctx, cfg)
}

func (s *Service) spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error) {
	var project domain.ProjectRecord
	var err error
	if cfg.ProjectID != "" {
		project, err = s.requireProject(ctx, cfg.ProjectID)
		if err != nil {
			return domain.Session{}, 0, 0, err
		}
	} else {
		if cfg.IssueID != "" || strings.TrimSpace(cfg.Branch) != "" {
			return domain.Session{}, 0, 0, apierr.Invalid("STANDALONE_PROJECT_FEATURE_UNSUPPORTED", "Standalone sessions do not support issues or branches", nil)
		}
		if cfg.Harness == "" {
			return domain.Session{}, 0, 0, apierr.Invalid("HARNESS_REQUIRED", "harness is required for a standalone session", nil)
		}
	}
	if s.agentReadiness != nil && cfg.Harness != "" {
		readiness, err := s.agentReadiness.EnsureAgentReadiness(ctx, string(cfg.Harness), domain.AgentReadinessPurposeLaunch)
		if err != nil {
			return domain.Session{}, 0, 0, err
		}
		if readiness.Installation.State == domain.AgentInstallationNotInstalled {
			return domain.Session{}, 0, 0, apierr.Invalid("AGENT_BINARY_NOT_FOUND", "The selected agent harness is not installed", map[string]any{"agentId": cfg.Harness})
		}
		if cfg.Harness == domain.HarnessCodex &&
			readiness.Authentication.State == domain.AgentAuthenticationUnauthorized &&
			readiness.Authentication.Freshness == domain.AgentReadinessFresh &&
			(s.runsOnComputerLogin == nil || s.runsOnComputerLogin(ctx, cfg.Harness, cfg.AgentAccountID)) {
			return domain.Session{}, 0, 0, apierr.Conflict("CODEX_ACCOUNT_AUTH_UNVERIFIED", "Add or sign in to a Codex account in Settings before starting a Codex session", nil)
		}
	}
	cfg = s.withIssueContext(ctx, cfg, project)
	rec, promptBytes, systemPromptBytes, err := s.spawnSession(ctx, cfg, project)
	if err != nil {
		s.invalidateAgentReadinessAfterLaunchFailure(cfg.Harness, err)
		apiErr := toSpawnAPIError(err)
		s.recordSpawnOutcome(ctx, cfg, domain.SessionRecord{}, actionStatusForError(err), apiErr)
		return domain.Session{}, 0, 0, apiErr
	}
	s.recordSpawnOutcome(ctx, cfg, rec, domain.ActivityStatusCompleted, nil)
	sess, err := s.toSession(ctx, rec)
	if err != nil {
		return domain.Session{}, 0, 0, err
	}
	return sess, promptBytes, systemPromptBytes, nil
}

// spawnSession is the only path from the service into manager.Spawn.
// Client-request replays return before reaching it.
func (s *Service) spawnSession(ctx context.Context, cfg ports.SpawnConfig, project domain.ProjectRecord) (domain.SessionRecord, int, int, error) {
	// CLI and orchestrator workers use this same naming policy as desktop tasks.
	if !cfg.NameFromPrompt && cfg.Kind == domain.KindWorker && project.ID != "" &&
		project.Kind.WithDefault() != domain.ProjectKindScratch && strings.TrimSpace(cfg.Branch) == "" && strings.TrimSpace(cfg.Prompt) != "" {
		cfg.NameFromPrompt = true
		cfg.KeepDisplayName = cfg.DisplayName != ""
	}
	return s.manager.Spawn(ctx, cfg)
}

func (s *Service) invalidateAgentReadinessAfterLaunchFailure(harness domain.AgentHarness, err error) {
	if s.agentReadiness == nil || harness == "" {
		return
	}
	id := string(harness)
	invalidated := false
	if errors.Is(err, ports.ErrAgentBinaryNotFound) {
		s.agentReadiness.InvalidateAgentInstallation(id)
		invalidated = true
	}
	if errors.Is(err, ports.ErrChatAuthRequired) {
		s.agentReadiness.InvalidateAgentAuthentication(id)
		invalidated = true
	}
	if invalidated {
		s.agentReadiness.RecheckAgent(id)
	}
}

// requireProject verifies the project is registered before any spawn write
// touches the session store, so an unknown projectId surfaces as a typed 404
// rather than an opaque 500 with an orphan terminated row left behind.
func (s *Service) requireProject(ctx context.Context, id domain.ProjectID) (domain.ProjectRecord, error) {
	if id == "" {
		return domain.ProjectRecord{}, apierr.Invalid("PROJECT_ID_REQUIRED", "projectId is required", nil)
	}
	if s.store == nil {
		return domain.ProjectRecord{ID: string(id)}, nil
	}
	rec, ok, err := s.store.GetProject(ctx, string(id))
	if err != nil {
		return domain.ProjectRecord{}, fmt.Errorf("get project %s: %w", id, err)
	}
	if !ok {
		return domain.ProjectRecord{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project. Register it with `tallpine project add`")
	}
	return rec, nil
}

// SpawnOrchestrator spawns an orchestrator session for a project. When clean is
// true it first tears down any active orchestrator(s) for that project so the new
// one is the only live coordinator. When clean is false it is idempotent: if an
// active orchestrator already exists it is returned as-is. A business rule that
// belongs here, not in the HTTP controller.
func (s *Service) SpawnOrchestrator(
	ctx context.Context,
	projectID domain.ProjectID,
	clean bool,
	requestedMode domain.SessionMode,
	approval domain.PermissionMode,
) (domain.Session, error) {
	return s.SpawnLaneOrchestrator(ctx, projectID, clean, requestedMode, approval, "")
}

// SpawnLaneOrchestrator is SpawnOrchestrator for one lane: laneItemID is the
// backlog item id the orchestrator owns ("" = the project's default one).
func (s *Service) SpawnLaneOrchestrator(
	ctx context.Context,
	projectID domain.ProjectID,
	clean bool,
	requestedMode domain.SessionMode,
	approval domain.PermissionMode,
	laneItemID string,
) (domain.Session, error) {
	project, err := s.requireProject(ctx, projectID)
	if err != nil {
		return domain.Session{}, err
	}
	return s.spawnOrchestrator(ctx, project, clean, requestedMode, approval, orchestratorSpawnOptions{LaneItemID: laneItemID})
}

// SpawnLaneOrchestratorBrief is SpawnLaneOrchestrator that also hands the new
// orchestrator prompt as its first user turn, the way a spawn prompt reaches a
// worker: the spawn delivers it once the chat controller is live, unframed.
// created reports whether this call started the orchestrator; when it returned
// a running one, the prompt was not delivered and the caller decides what to do.
func (s *Service) SpawnLaneOrchestratorBrief(
	ctx context.Context,
	projectID domain.ProjectID,
	clean bool,
	requestedMode domain.SessionMode,
	approval domain.PermissionMode,
	laneItemID, prompt string,
) (sess domain.Session, created bool, err error) {
	project, err := s.requireProject(ctx, projectID)
	if err != nil {
		return domain.Session{}, false, err
	}
	sess, err = s.spawnOrchestrator(ctx, project, clean, requestedMode, approval,
		orchestratorSpawnOptions{LaneItemID: laneItemID, Prompt: prompt, Created: &created})
	return sess, created, err
}

// orchestratorSpawnOptions are the knobs only the handoff path sets.
type orchestratorSpawnOptions struct {
	// Created, when set, is set to true if the call started a new session.
	Created *bool
	// Prompt is the new orchestrator's first user message.
	Prompt string
	// SkipRetireNotice leaves the retired orchestrator unmessaged: the handoff
	// was started by that orchestrator (or after it wrote its note).
	SkipRetireNotice bool
	// LaneItemID is the backlog item id of the lane the orchestrator owns;
	// empty is the project's default orchestrator. Clean replacement and the
	// idempotent "already running" check only look at this lane.
	LaneItemID string
}

func (s *Service) spawnOrchestrator(
	ctx context.Context,
	project domain.ProjectRecord,
	clean bool,
	requestedMode domain.SessionMode,
	approval domain.PermissionMode,
	opts orchestratorSpawnOptions,
) (domain.Session, error) {
	projectID := domain.ProjectID(project.ID)
	// A start runs the project's Setup and a clean replacement first runs
	// the retired orchestrator's Cleanup, both possibly longer than one REST
	// request; the lock may also wait for another replacement. The request's
	// budget covers them from here. The bounded context is not passed on:
	// the spawn extends the budget again, from its own start, and a deadline
	// set here would cap it.
	budget := sessionmanager.SetupBudget(project)
	if clean {
		budget += sessionmanager.ReplacementCleanupBudget(project)
	}
	_, cancelBudget := ports.ExtendSpawnRequestBudget(ctx, budget)
	defer cancelBudget()
	unlock := s.lockOrchestratorProject(projectID)
	defer unlock()
	return s.spawnOrchestratorLocked(ctx, project, clean, requestedMode, approval, opts)
}

// spawnOrchestratorLocked is spawnOrchestrator for a caller that already holds
// the project's orchestrator lock.
func (s *Service) spawnOrchestratorLocked(
	ctx context.Context,
	project domain.ProjectRecord,
	clean bool,
	requestedMode domain.SessionMode,
	approval domain.PermissionMode,
	opts orchestratorSpawnOptions,
) (domain.Session, error) {
	projectID := domain.ProjectID(project.ID)
	mode := requestedMode
	if clean {
		existing, err := s.activeOrchestrators(ctx, projectID, opts.LaneItemID)
		if err != nil {
			return domain.Session{}, err
		}
		if len(existing) > 1 {
			// Each retirement runs its own Cleanup.
			_, cancelRetireBudget := ports.ExtendSpawnRequestBudget(ctx,
				sessionmanager.SetupBudget(project)+time.Duration(len(existing))*sessionmanager.ReplacementCleanupBudget(project))
			defer cancelRetireBudget()
		}
		if len(existing) > 0 && mode == "" {
			// Clean replacement preserves the controller contract of the
			// orchestrator being replaced only when the caller did not make an
			// explicit choice. The global default still must not silently flip an
			// existing project's coordinator, but an explicit replacement mode is
			// authoritative.
			mode = newestSession(existing).Mode
		}
		retireCtx := context.WithoutCancel(ctx)
		release, err := s.holdLaneForReplacement(retireCtx, projectID, opts.LaneItemID)
		if err != nil {
			return domain.Session{}, err
		}
		defer release()
		for _, orch := range existing {
			if !opts.SkipRetireNotice {
				_ = s.sendRetireNotice(retireCtx, orch.ID, opts.LaneItemID)
			}
			if err := s.manager.RetireForReplacement(retireCtx, orch.ID); err != nil {
				return domain.Session{}, toAPIError(err)
			}
		}
	} else {
		existing, err := s.activeOrchestrators(ctx, projectID, opts.LaneItemID)
		if err != nil {
			return domain.Session{}, err
		}
		live := existing[:0:0]
		for _, o := range existing {
			if o.Activity.State != domain.ActivityExited {
				live = append(live, o)
			}
		}
		if len(live) > 0 {
			return newestSession(live), nil
		}
		// Every match exited (e.g. its async start failed): reusing it would send
		// the prompt to a dead controller, so retire it and start a fresh one.
		if len(existing) > 0 {
			release, err := s.holdLaneForReplacement(context.WithoutCancel(ctx), projectID, opts.LaneItemID)
			if err != nil {
				return domain.Session{}, err
			}
			defer release()
			for _, orch := range existing {
				if err := s.manager.RetireForReplacement(context.WithoutCancel(ctx), orch.ID); err != nil {
					return domain.Session{}, toAPIError(err)
				}
			}
		}
	}
	if opts.Created != nil {
		*opts.Created = true
	}
	sess, _, _, err := s.spawn(ctx, ports.SpawnConfig{
		ProjectID:       projectID,
		Kind:            domain.KindOrchestrator,
		Async:           true,
		RequestedMode:   mode,
		LaneItemID:      opts.LaneItemID,
		Prompt:          opts.Prompt,
		ParentSessionID: AgentLaneOf(ctx).StartedBy,
		AgentConfig: ports.AgentConfig{
			Permissions: approval,
			Model:       AgentLaneOf(ctx).Model,
		},
	})
	if err != nil {
		return domain.Session{}, err
	}
	if err := s.verifyOrchestratorReplacement(project, sess, opts.LaneItemID); err != nil {
		return domain.Session{}, err
	}
	return sess, nil
}

// activeOrchestrators lists the live orchestrators of one lane ("" = the
// project's un-laned default orchestrators).
func (s *Service) activeOrchestrators(ctx context.Context, projectID domain.ProjectID, lane string) ([]domain.Session, error) {
	active := true
	all, err := s.List(ctx, ListFilter{ProjectID: projectID, Active: &active, OrchestratorOnly: true})
	if err != nil {
		return nil, err
	}
	ls, ok := s.store.(ports.LaneReader)
	if !ok {
		return all, nil
	}
	lanes, err := ls.ListSessionLanes(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := all[:0:0]
	for _, sess := range all {
		if lanes[sess.ID] == lane {
			out = append(out, sess)
		}
	}
	return out, nil
}

const orchestratorRetireNoticeTail = " Stop coordinating new work now; a fresh orchestrator will take over in a new workspace."

// retireNotice words the notice for the orchestrator being replaced: the
// project's default orchestrator, or the lane orchestrator named by its lane.
func (s *Service) retireNotice(ctx context.Context, laneItemID string) string {
	if laneItemID == "" {
		return "Tallpine is replacing this project orchestrator." + orchestratorRetireNoticeTail
	}
	name := laneItemID
	if bs, ok := s.store.(interface {
		BacklogRefsByID(context.Context, []string) (map[string]string, error)
	}); ok {
		if refs, err := bs.BacklogRefsByID(ctx, []string{laneItemID}); err == nil && refs[laneItemID] != "" {
			name = refs[laneItemID]
		}
	}
	return "Tallpine is replacing the orchestrator of lane " + name + "." + orchestratorRetireNoticeTail
}

func (s *Service) sendRetireNotice(ctx context.Context, id domain.SessionID, laneItemID string) error {
	if err := s.manager.Send(ctx, id, s.retireNotice(ctx, laneItemID), nil); err != nil {
		return fmt.Errorf("send retire notice to %s: %w", id, err)
	}
	return nil
}

func (s *Service) verifyOrchestratorReplacement(project domain.ProjectRecord, sess domain.Session, laneItemID string) error {
	if sess.IsTerminated {
		return fmt.Errorf("orchestrator replacement verification failed: new session %s is terminated", sess.ID)
	}
	if sess.Kind != domain.KindOrchestrator {
		return fmt.Errorf("orchestrator replacement verification failed: new session %s has kind %q", sess.ID, sess.Kind)
	}
	expected, _ := project.Config.Orchestrator.Harness.OrFallback()
	if expected != "" && sess.Harness != expected {
		return fmt.Errorf("orchestrator replacement verification failed: new session %s uses harness %q, want %q", sess.ID, sess.Harness, expected)
	}
	expectedBranch := sessionmanager.LaneBranch(sessionmanager.DefaultOrchestratorBranch(serviceSessionPrefix(project), s.dataDir), domain.KindOrchestrator, laneItemID)
	if sess.Metadata.Branch != "" && sess.Metadata.Branch != expectedBranch {
		return fmt.Errorf("orchestrator replacement verification failed: new session %s uses branch %q, want %q", sess.ID, sess.Metadata.Branch, expectedBranch)
	}
	return nil
}

func serviceSessionPrefix(project domain.ProjectRecord) string {
	if p := strings.TrimSpace(project.Config.SessionPrefix); p != "" {
		return p
	}
	id := project.ID
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func newestSession(sessions []domain.Session) domain.Session {
	newest := sessions[0]
	for _, sess := range sessions[1:] {
		if sessionNewer(sess.SessionRecord, newest.SessionRecord) {
			newest = sess
		}
	}
	return newest
}

func sessionNewer(a, b domain.SessionRecord) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.After(b.CreatedAt)
	}
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	return string(a.ID) > string(b.ID)
}

func (s *Service) lockOrchestratorProject(projectID domain.ProjectID) func() {
	s.orchestratorLocksMu.Lock()
	if s.orchestratorLocks == nil {
		s.orchestratorLocks = make(map[domain.ProjectID]*sync.Mutex)
	}
	mu := s.orchestratorLocks[projectID]
	if mu == nil {
		mu = &sync.Mutex{}
		s.orchestratorLocks[projectID] = mu
	}
	s.orchestratorLocksMu.Unlock()

	mu.Lock()
	return mu.Unlock
}

func (s *Service) replayClientRequest(ctx context.Context, id, hash string) (domain.SessionRecord, bool, error) {
	if id == "" || s.store == nil {
		return domain.SessionRecord{}, false, nil
	}
	rec, found, err := s.store.GetSessionByClientRequestID(ctx, id)
	if err != nil || !found {
		return rec, found, err
	}
	if rec.ClientRequestHash != hash {
		return domain.SessionRecord{}, false, toSpawnAPIError(sessionmanager.ErrClientRequestConflict)
	}
	if !rec.ClientRequestCommitted {
		return domain.SessionRecord{}, false, toSpawnAPIError(sessionmanager.ErrClientRequestIncomplete)
	}
	return rec, true, nil
}
