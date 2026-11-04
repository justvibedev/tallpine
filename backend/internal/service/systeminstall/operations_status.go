package systeminstall

import (
	"context"
	"fmt"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// owningUpdatePlan picks the update plan whose method owns the binary
// sessions run, for an update request that names no method. The latest
// advisory's maintenance method is preferred; otherwise each available
// method is checked against the resolved binary. ok is false when no method
// is confirmed, and the caller's plan is then refused by confirmInstalledOwner.
func (s *Service) owningUpdatePlan(ctx context.Context, target Target, plans []Plan) (Plan, bool) {
	s.mu.Lock()
	advisory, hasAdvisory := s.updateAdvisories[target]
	s.mu.Unlock()
	if hasAdvisory && advisory.MaintenanceMethod != "" {
		for _, plan := range plans {
			if plan.Method == advisory.MaintenanceMethod && !plan.Unsupported {
				return plan, true
			}
		}
	}
	if s.verifier == nil {
		return Plan{}, false
	}
	verified, _ := s.verifier.Verify(ctx, target)
	if verified.ResolvedPath == "" {
		return Plan{}, false
	}
	for _, plan := range plans {
		if !plan.Unsupported && s.methodOwnsBinary(ctx, plan, verified.ResolvedPath) {
			return plan, true
		}
	}
	return Plan{}, false
}

func (s *Service) harnessGate(harness domain.AgentHarness) *sync.RWMutex {
	s.harnessGatesMu.Lock()
	defer s.harnessGatesMu.Unlock()
	if s.harnessGates == nil {
		s.harnessGates = make(map[domain.AgentHarness]*sync.RWMutex)
	}
	gate := s.harnessGates[harness]
	if gate == nil {
		gate = &sync.RWMutex{}
		s.harnessGates[harness] = gate
	}
	return gate
}

// TryBeginHarnessUse prevents session launches from racing replacement of the
// selected harness executable. The returned release must be called after launch.
// A launch also notices a binary replaced outside Tallpine and drops its stale
// update advisory.
func (s *Service) TryBeginHarnessUse(harness domain.AgentHarness) (func(), bool) {
	gate := s.harnessGate(harness)
	if !gate.TryRLock() && !s.waitForHarnessChange(Target(harness), gate) {
		return nil, false
	}
	if IsAgentTarget(Target(harness)) {
		s.forgetReplacedBinary(Target(harness))
	}
	return gate.RUnlock, true
}

// Status returns the current or last known Job for target. A target that has
// never been started returns a plan preview: Idle when the daemon can run it,
// or Unsupported with a manual command/reason when it cannot. An error is
// returned only when target is not a known install target.
func (s *Service) Status(ctx context.Context, target Target) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	if !Valid(target) {
		return Job{}, fmt.Errorf("systeminstall: unknown target %q", target)
	}
	s.mu.Lock()
	inMemory, ok := s.jobs[target]
	if ok {
		snapshot := *inMemory
		s.mu.Unlock()
		return snapshot, nil
	}
	s.mu.Unlock()
	if IsAgentTarget(target) && s.jobStore != nil {
		record, ok, err := s.jobStore.GetAgentInstallJob(ctx, string(target))
		if err != nil {
			return Job{}, err
		}
		if ok {
			return jobFromRecord(record), nil
		}
	}

	var plan Plan
	if IsAgentTarget(target) {
		planner, err := s.newRequestPlanner(ctx)
		if err != nil {
			return Job{}, err
		}
		plans := planner.agentMethodPlans(target, AgentOperationInstall)
		plan = plans[recommendedPlanIndex(plans)]
	} else {
		plan = s.resolvePlan(target)
	}
	status := StatusIdle
	if plan.Unsupported {
		status = StatusUnsupported
	}
	return Job{
		Target:  target,
		Status:  status,
		Command: displayCommand(plan),
		Error:   plan.Reason,
		Notice:  plan.Notice,
	}, nil
}

// AgentJobs returns the latest durable job for every harness that has one.
func (s *Service) AgentJobs(ctx context.Context) ([]Job, error) {
	if s.jobStore != nil {
		records, err := s.jobStore.ListAgentInstallJobs(ctx)
		if err != nil {
			return nil, err
		}
		jobs := make([]Job, 0, len(records))
		indexes := make(map[Target]int, len(records))
		for _, record := range records {
			job := jobFromRecord(record)
			indexes[job.Target] = len(jobs)
			jobs = append(jobs, job)
		}
		s.mu.Lock()
		for target, job := range s.jobs {
			if IsAgentTarget(target) {
				if index, ok := indexes[target]; ok {
					jobs[index] = *job
				} else {
					indexes[target] = len(jobs)
					jobs = append(jobs, *job)
				}
			}
		}
		s.mu.Unlock()
		return jobs, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := make([]Job, 0, len(s.jobs))
	for target, job := range s.jobs {
		if IsAgentTarget(target) {
			jobs = append(jobs, *job)
		}
	}
	return jobs, nil
}

// Recover marks unfinished durable jobs interrupted and hydrates the in-memory
// coordination map before the HTTP server starts accepting requests.
func (s *Service) Recover(ctx context.Context) error {
	if err := s.ReconcileManagedReleases(ctx); err != nil {
		return err
	}
	if s.jobStore == nil {
		return nil
	}
	if err := s.jobStore.InterruptActiveAgentInstallJobs(ctx, time.Now().UTC()); err != nil {
		return err
	}
	records, err := s.jobStore.ListAgentInstallJobs(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range records {
		job := jobFromRecord(record)
		s.jobs[job.Target] = &job
	}
	return nil
}

// Verify starts adapter-backed verification without rerunning an installer.
func (s *Service) Verify(ctx context.Context, target Target) (Job, error) {
	if !IsAgentTarget(target) {
		return Job{}, fmt.Errorf("systeminstall: unknown harness target %q", target)
	}
	s.mu.Lock()
	if current, ok := s.jobs[target]; ok && activeStatus(current.Status) {
		s.mu.Unlock()
		return Job{}, ErrInstallActive
	}
	previous, hadPrevious := s.jobs[target]
	now := time.Now().UTC()
	job := &Job{Target: target, Status: StatusVerifying, StartedAt: &now, UpdatedAt: &now}
	if current, ok := s.jobs[target]; ok {
		job.Method = current.Method
		job.Command = current.Command
		job.ExpectedDestination = current.ExpectedDestination
		job.Output = current.Output
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
	initial := *job
	if !s.beginWorker() {
		s.finishAgentJob(job, StatusInterrupted, "", "daemon shutdown interrupted verification", "")
		return initial, nil
	}
	go func() { //nolint:gosec // bounded daemon-owned worker intentionally outlives the request.
		defer s.workers.Done()
		s.runAgentVerification(s.backgroundContext, job, nil, "")
	}()
	return initial, nil
}

func (s *Service) beginWorker() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return false
	}
	s.workers.Add(1)
	return true
}

// Close cancels and drains daemon-owned harness installation work.
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	if !s.stopping {
		s.stopping = true
		s.stop()
	}
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("systeminstall: drain workers: %w", ctx.Err())
	}
}
