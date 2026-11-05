package systeminstall

import (
	"context"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Start begins the install for target, or returns the already-running Job if
// one is in flight (idempotent — it never starts a second concurrent run of
// the same target). target must be one of the fixed known values; anything else
// is a caller bug and returns an error the controller turns into a 400.
func (s *Service) Start(ctx context.Context, target Target) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	if !Valid(target) {
		return Job{}, fmt.Errorf("systeminstall: unknown target %q", target)
	}
	if IsAgentTarget(target) {
		return s.StartAgent(ctx, target, "")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if job, ok := s.jobs[target]; ok && job.Status == StatusRunning {
		return *job, nil
	}

	plan := s.resolvePlan(target)
	command := displayCommand(plan)
	now := time.Now()
	if plan.Unsupported {
		job := &Job{
			Target:     target,
			Status:     StatusUnsupported,
			Command:    command,
			Error:      plan.Reason,
			Notice:     plan.Notice,
			StartedAt:  &now,
			FinishedAt: &now,
		}
		s.jobs[target] = job
		return *job, nil
	}

	job := &Job{
		Target:    target,
		Status:    StatusRunning,
		Command:   command,
		Notice:    plan.Notice,
		StartedAt: &now,
	}
	s.jobs[target] = job
	if s.stopping {
		job.Status = StatusInterrupted
		job.Error = "daemon shutdown interrupted the install"
		job.FinishedAt = &now
		return *job, nil
	}
	s.workers.Add(1)

	go func() { //nolint:gosec // bounded daemon-owned worker intentionally outlives the request.
		defer s.workers.Done()
		s.run(s.backgroundContext, plan.Command, job)
	}()

	return *job, nil
}

// StartAgent begins a harness install using one server-owned method ID. An
// empty method chooses the first currently viable method for compatibility
// with older clients.
func (s *Service) StartAgent(ctx context.Context, target Target, method string) (Job, error) {
	return s.StartAgentOperation(ctx, target, method, AgentOperationInstall, "")
}

// StartAgentOperation begins a harness operation using one server-owned
// method ID and operation. An empty method chooses the recommended method
// for an install or reinstall and the method that owns the installed copy for
// an update. expected is the release the user approved for an update, or "".
// An update runs alongside live sessions, which restart on the new release
// once idle; see gateRunningSessions.
func (s *Service) StartAgentOperation(ctx context.Context, target Target, method string, operation AgentOperation, expected string) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	if !operation.valid() {
		return Job{}, fmt.Errorf("%w: unknown install operation %q", ErrInstallMethod, operation)
	}
	if expected != "" {
		if _, ok := parseUpdateVersion(expected); !ok || operation != AgentOperationUpdate {
			return Job{}, ErrUpdateVersion
		}
	}
	if !IsAgentTarget(target) {
		return Job{}, fmt.Errorf("systeminstall: unknown harness target %q", target)
	}
	var releaseHarness func()
	requiresHarnessGate := operation == AgentOperationReinstall || operation == AgentOperationUpdate
	if requiresHarnessGate {
		s.mu.Lock()
		if current, ok := s.jobs[target]; ok && activeStatus(current.Status) {
			s.mu.Unlock()
			return Job{}, ErrInstallActive
		}
		s.mu.Unlock()
		gate := s.harnessGate(domain.AgentHarness(target))
		if !gate.TryLock() {
			return Job{}, &HarnessActiveError{Target: target, Starting: true, Level: UpdateWhileRunningBlocked}
		}
		s.setHarnessChanging(target, true)
		releaseHarness = func() {
			s.setHarnessChanging(target, false)
			gate.Unlock()
		}
		defer func() {
			if releaseHarness != nil {
				releaseHarness()
			}
		}()
		if operation != AgentOperationUpdate {
			// Only an update can run alongside sessions; anything else waits.
			if _, err := s.gateRunningSessions(ctx, target, operation, Plan{}); err != nil {
				return Job{}, err
			}
		}
	}

	var plan Plan
	var err error
	planner, plannerErr := s.newRequestPlanner(ctx)
	if plannerErr != nil {
		return Job{}, plannerErr
	}
	if method == "" {
		plans := planner.agentMethodPlans(target, operation)
		plan = plans[recommendedPlanIndex(plans)]
		if operation == AgentOperationUpdate {
			if owner, ok := s.owningUpdatePlan(ctx, target, plans); ok {
				plan = owner
			}
		}
	} else {
		plan, err = planner.resolveAgentMethod(target, method, operation)
		if err != nil {
			return Job{}, err
		}
	}

	var baseline *installedBaseline
	if operation == AgentOperationUpdate {
		if baseline, err = s.confirmInstalledOwner(ctx, target, plan); err != nil {
			return Job{}, err
		}
		if !plan.managed {
			// A managed update installs into Tallpine's own directory, so the
			// installed copy's prefix and its Node do not apply.
			plannedPrefix := plan.PackagePrefix
			if baseline != nil && baseline.path != "" {
				plan = targetInstalledCopy(s.goos, plan, target, baseline.path)
			}
			plan = s.validateNPMUpdateTarget(ctx, plan, plannedPrefix)
		}
	}
	if operation == AgentOperationUpdate && expected != "" {
		if baseline == nil {
			baseline = &installedBaseline{target: target, scheme: versionSchemeFor(target)}
		}
		// An update never moves the installed copy backwards.
		if versionAtMost(baseline.scheme, expected, baseline.version) {
			return Job{}, ErrUpdateVersion
		}
		// Pin the version the user approved into this job's immutable baseline.
		// Concurrent advisory refreshes cannot erase this verification floor.
		baseline.expected = expected
	}
	if operation == AgentOperationUpdate && baseline != nil {
		// A cached latest at or below the version just probed is stale (the
		// harness was updated elsewhere); pinning it would move it backwards.
		if baseline.latest != "" && versionAtMost(baseline.scheme, baseline.latest, baseline.version) {
			baseline.latest = ""
		}
		// Install the release the user approved when it is one the advisory
		// knows exists, else the one the advisory announced. The client value
		// never reaches the command unless it lies between installed and latest.
		pin := baseline.latest
		if baseline.expected != "" && baseline.version != "" && versionAtMost(baseline.scheme, baseline.expected, baseline.latest) {
			pin = baseline.expected
		}
		if pin != "" {
			// The job succeeds once it reaches the release it installs, even
			// if a newer one was announced meanwhile.
			baseline.latest = pin
		}
		plan = pinVendorUpdate(plan, pin)
	}
	if operation == AgentOperationUpdate {
		// Sessions are checked once the plan is final: whether an update may run
		// alongside them depends on the method that owns the installed copy.
		withSessions, err := s.gateRunningSessions(ctx, target, operation, plan)
		var active *HarnessActiveError
		switch {
		case errors.Is(err, errUpdateWhenIdle) && !plan.Unsupported:
			// Wait for idle sessions without holding the harness meanwhile.
			return s.startIdleUpdate(ctx, plan, baseline)
		case errors.As(err, &active):
		}
		if err != nil {
			return Job{}, err
		}
		// effectiveUpdateLevel takes s.mu itself, so it runs before the lock.
		level := s.effectiveUpdateLevel(plan.UpdateWhileRunning)
		s.mu.Lock()
		if s.updateLevels == nil {
			s.updateLevels = make(map[Target]UpdateWhileRunning)
		}
		s.updateLevels[target] = level
		s.mu.Unlock()
		plan.keepPreviousRelease = withSessions
	}

	now := time.Now().UTC()
	if plan.Unsupported && operation != AgentOperationInstall {
		// A refused update or reinstall changes nothing, so it is reported to
		// the caller without replacing the record of the installation's last job.
		return Job{
			Target: target, Status: StatusUnsupported, Method: plan.Method,
			Command: displayCommand(plan), ExpectedDestination: plan.ExpectedDestination,
			Error: plan.Reason, Notice: plan.Notice, StartedAt: &now, FinishedAt: &now, UpdatedAt: &now,
		}, nil
	}
	s.mu.Lock()
	if current, ok := s.jobs[target]; ok && activeStatus(current.Status) {
		s.mu.Unlock()
		return Job{}, ErrInstallActive
	}
	previous, hadPrevious := s.jobs[target]
	status := StatusInstalling
	finishedAt := (*time.Time)(nil)
	if plan.Unsupported {
		status = StatusUnsupported
		finishedAt = &now
	}
	job := &Job{
		Target: target, Status: status, Method: plan.Method,
		Command: displayCommand(plan), ExpectedDestination: plan.ExpectedDestination,
		Error: plan.Reason, Notice: plan.Notice, StartedAt: &now, FinishedAt: finishedAt, UpdatedAt: &now,
	}
	s.jobs[target] = job
	s.mu.Unlock()

	if err := s.persistJob(ctx, *job); err != nil {
		s.mu.Lock()
		if s.jobs[target] == job {
			if hadPrevious {
				s.jobs[target] = previous
			} else {
				delete(s.jobs, target)
			}
		}
		s.mu.Unlock()
		return Job{}, err
	}
	if status == StatusUnsupported {
		return *job, nil
	}

	initial := *job
	if !s.beginWorker() {
		s.finishAgentJob(job, StatusInterrupted, "", "daemon shutdown interrupted the install", "")
		return initial, nil
	}
	workerRelease := releaseHarness
	releaseHarness = nil
	go func() { //nolint:gosec // bounded daemon-owned worker intentionally outlives the request.
		defer s.workers.Done()
		if workerRelease != nil {
			defer workerRelease()
		}
		s.runAgentOperation(s.backgroundContext, plan, operation, job, baseline)
	}()
	return initial, nil
}
