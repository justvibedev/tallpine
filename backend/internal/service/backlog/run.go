package backlog

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// RunService owns backlog run mode: the durable run, its plan and per-item
// status, the rules that keep them sane, and the trigger message. How to plan
// and what to build stays with the orchestrator; this holds the state it reads
// and writes. Every method answers "off" unless the TALLPINE_BACKLOG_RUN switch is on.
type RunService struct {
	store   RunStore
	notify  Notifier
	enabled bool
	plans   *PlanService
	gates   sync.Map // run id -> *planGate
	newID   func() string
	now     func() time.Time
}

// RunDeps configures a RunService.
type RunDeps struct {
	Store   RunStore
	Notify  Notifier
	Enabled bool
	// Plans, when set, makes the run ask for plans (B205).
	Plans *PlanService
	NewID func() string
	Now   func() time.Time
}

// NewRunService constructs the run service.
func NewRunService(d RunDeps) *RunService {
	if d.NewID == nil {
		d.NewID = func() string { return "blr_" + uuid.NewString() }
	}
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	s := &RunService{store: d.Store, notify: d.Notify, enabled: d.Enabled, plans: d.Plans, newID: d.NewID, now: d.Now}
	if d.Plans != nil {
		d.Plans.SetObserver(s.onPlanChange)
	}
	return s
}

// Enabled reports whether run mode is switched on.
func (s *RunService) Enabled() bool { return s.enabled }

// Start creates a run. A human request (the Run button) posts exactly one
// message to the project's orchestrator; if that cannot be delivered the run is
// cancelled so nothing waits for a planner that never heard. An orchestrator
// creating its own run (the owner asked in chat) sends nothing.
func (s *RunService) Start(ctx context.Context, projectID domain.ProjectID, actor Actor, in RunStartInput) (RunView, error) {
	if err := s.requireEnabled(); err != nil {
		return RunView{}, err
	}
	if actor.Kind != domain.BacklogOriginHuman && actor.Kind != domain.BacklogOriginOrchestrator {
		return RunView{}, apierr.Forbidden("BACKLOG_RUN_FORBIDDEN", "Only the owner or the orchestrator can start a run")
	}
	selection, err := s.validateSelection(ctx, projectID, in)
	if err != nil {
		return RunView{}, mapRunError(err)
	}
	if in.Scope == "" || !in.Scope.Valid() {
		return RunView{}, mapRunError(invalidRun("scope must be local, pr or pr-merge"))
	}
	now := s.now().UTC()
	run, err := s.store.CreateBacklogRun(ctx, domain.BacklogRun{
		ID: s.newID(), ProjectID: projectID, Scope: in.Scope, SelectionKind: in.Kind, Selection: selection,
		Status: domain.BacklogRunPlanning, RequestedBy: actor.Kind, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return RunView{}, mapRunError(err)
	}
	if actor.Kind == domain.BacklogOriginHuman {
		if err := s.notifyOrchestrator(ctx, projectID, runRequestedText(run)); err != nil {
			s.cancelUndelivered(ctx, run)
			if errors.Is(err, ErrNoOrchestrator) {
				return RunView{}, apierr.Conflict("BACKLOG_RUN_NO_ORCHESTRATOR", "The project has no running orchestrator to plan the run", nil)
			}
			return RunView{}, err
		}
	}
	return s.view(ctx, run)
}

func (s *RunService) notifyOrchestrator(ctx context.Context, projectID domain.ProjectID, text string) error {
	if s.notify == nil {
		return ErrNoOrchestrator
	}
	return s.notify.NotifyOrchestrator(ctx, projectID, text)
}

func (s *RunService) cancelUndelivered(ctx context.Context, run domain.BacklogRun) {
	now := s.now().UTC()
	_, _, _ = s.store.UpdateBacklogRunState(ctx, run.ID, now, func(r *domain.BacklogRun, _ []domain.BacklogRunItem, _ int) error {
		r.Summary = "Cancelled: the orchestrator could not be reached."
		finishRun(r, now)
		return nil
	})
}

func (s *RunService) validateSelection(ctx context.Context, projectID domain.ProjectID, in RunStartInput) ([]string, error) {
	if !in.Kind.Valid() {
		return nil, invalidRun("selection must be all, refs or parent")
	}
	if in.Kind == domain.BacklogRunSelectAll {
		return nil, nil
	}
	if len(in.Refs) == 0 || (in.Kind == domain.BacklogRunSelectParent && len(in.Refs) != 1) {
		return nil, invalidRun("selection %s needs %s", in.Kind, map[bool]string{true: "exactly one parent ref", false: "at least one ref"}[in.Kind == domain.BacklogRunSelectParent])
	}
	out := make([]string, 0, len(in.Refs))
	for _, ref := range in.Refs {
		item, err := s.resolveRunItem(ctx, projectID, ref)
		if err != nil {
			return nil, err
		}
		if in.Kind == domain.BacklogRunSelectParent {
			if n, err := s.store.CountBacklogChildren(ctx, item.ID); err != nil || n == 0 {
				if err != nil {
					return nil, err
				}
				return nil, invalidRun("%s has no children to run", item.Ref())
			}
		}
		out = append(out, item.Ref())
	}
	return out, nil
}

// Get returns one run with its plan, display statuses and usage.
func (s *RunService) Get(ctx context.Context, projectID domain.ProjectID, ref string) (RunView, error) {
	if err := s.requireEnabled(); err != nil {
		return RunView{}, err
	}
	run, err := s.getRun(ctx, projectID, ref)
	if err != nil {
		return RunView{}, err
	}
	return s.view(ctx, run)
}

// List returns a project's runs, newest first, without their plans.
func (s *RunService) List(ctx context.Context, projectID domain.ProjectID, openOnly bool, limit int) ([]domain.BacklogRun, error) {
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	return s.store.ListBacklogRuns(ctx, projectID, openOnly, limit)
}

func (s *RunService) getRun(ctx context.Context, projectID domain.ProjectID, ref string) (domain.BacklogRun, error) {
	seq, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(ref), "R"), "r"), 10, 64)
	if err != nil || seq < 1 {
		return domain.BacklogRun{}, apierr.Invalid("INVALID_BACKLOG_RUN_REF", "Run ref must look like R3", nil)
	}
	run, ok, err := s.store.GetBacklogRun(ctx, projectID, seq)
	if err != nil {
		return domain.BacklogRun{}, err
	}
	if !ok {
		return domain.BacklogRun{}, mapRunError(domain.ErrBacklogRunNotFound)
	}
	return run, nil
}

func mapRunError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrBacklogRunNotFound):
		return apierr.NotFound("BACKLOG_RUN_NOT_FOUND", "Unknown backlog run")
	case errors.Is(err, domain.ErrBacklogRunOpen):
		return apierr.Conflict("BACKLOG_RUN_OPEN", "The project already has an open run; stop it or wait for it to finish", nil)
	case errors.Is(err, domain.ErrBacklogRunWorkerCap):
		return apierr.Conflict("BACKLOG_RUN_WORKER_CAP", cleanMsg(err), nil)
	case errors.Is(err, domain.ErrBacklogRunTransition):
		return apierr.Conflict("BACKLOG_RUN_TRANSITION", cleanMsg(err), nil)
	case errors.Is(err, domain.ErrBacklogRunInvalid):
		return apierr.Invalid("INVALID_BACKLOG_RUN", cleanMsg(err), nil)
	}
	return mapError(err)
}
