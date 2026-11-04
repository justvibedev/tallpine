package systeminstall

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// runAgentVerification re-probes the harness after a job. For an update, the
// baseline from before the command decides whether the binary sessions run
// actually changed; a zero exit status alone is not proof. updaterReport is
// the update command's last line: self-updaters can exit 0 without replacing
// the binary ("already up to date", "update manually", "restart to finish"),
// and a failed update quotes it so the user sees which.
func (s *Service) runAgentVerification(ctx context.Context, job *Job, baseline *installedBaseline, updaterReport string) {
	if s.verifier == nil {
		s.finishAgentJob(job, StatusFailed, "", "adapter-backed install verifier is not configured", "")
		return
	}
	result, err := s.verifier.Verify(ctx, job.Target)
	if ctx.Err() == context.Canceled {
		s.finishAgentJob(job, StatusInterrupted, result.Output, "daemon shutdown interrupted verification", result.ResolvedPath)
		return
	}
	if err != nil {
		s.finishAgentJob(job, StatusFailed, result.Output, err.Error(), result.ResolvedPath)
		return
	}
	output := result.Output
	failure, note := updateOutcome(baseline, result)
	if note != "" {
		output = strings.TrimRight(output, "\n") + "\n" + note
	}
	if failure != "" {
		if updaterReport != "" {
			failure = fmt.Sprintf("%s The updater said: %q", failure, updaterReport)
		}
		s.finishAgentJob(job, StatusFailed, output, failure, result.ResolvedPath)
		return
	}
	if baseline != nil {
		// Only updates carry a baseline. Mark the sessions it left behind
		// before reporting success, while launches still wait on this update.
		if installed, ok := baseline.scheme.find(result.Output); ok {
			s.noteHarnessUpdated(job.Target, installed.display)
		}
	}
	s.finishAgentJob(job, StatusSucceeded, output, "", result.ResolvedPath)
}

func (s *Service) transitionAgentJob(job *Job, status Status, output, errorMessage, resolvedPath string) error {
	now := time.Now().UTC()
	s.mu.Lock()
	job.Status = status
	job.Output = combineOutput(job.Output, output)
	job.Error = errorMessage
	if resolvedPath != "" {
		job.ExpectedDestination = resolvedPath
	}
	job.UpdatedAt = &now
	snapshot := *job
	s.mu.Unlock()
	return s.persistJobBestEffort(snapshot)
}

func (s *Service) finishAgentJob(job *Job, status Status, output, errorMessage, resolvedPath string) {
	now := time.Now().UTC()
	s.mu.Lock()
	job.Status = status
	job.Output = combineOutput(job.Output, output)
	job.Error = errorMessage
	if resolvedPath != "" {
		job.ExpectedDestination = resolvedPath
	}
	job.FinishedAt = &now
	job.UpdatedAt = &now
	delete(s.updateLevels, job.Target)
	delete(s.updateAdvisories, job.Target)
	delete(s.updateAdvisoryCalls, job.Target)
	// The next advisory re-derives the owner of whatever binary now runs.
	delete(s.derivedOwners, job.Target)
	snapshot := *job
	callback := s.onSucceeded
	target := job.Target
	s.mu.Unlock()
	if err := s.persistJobBestEffort(snapshot); err != nil {
		s.mu.Lock()
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("persist terminal operation state: %v", err)
		s.mu.Unlock()
		return
	}
	if status == StatusSucceeded && callback != nil {
		callback(target)
	}
}

func (s *Service) persistJobBestEffort(job Job) error {
	timeout := s.persistenceTimeout
	if timeout <= 0 {
		timeout = defaultPersistenceTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return s.persistJob(ctx, job)
}

func (s *Service) persistJob(ctx context.Context, job Job) error {
	if s.jobStore == nil {
		return nil
	}
	if job.StartedAt == nil {
		return fmt.Errorf("systeminstall: persist job without startedAt")
	}
	updatedAt := *job.StartedAt
	if job.UpdatedAt != nil {
		updatedAt = *job.UpdatedAt
	}
	return s.jobStore.UpsertAgentInstallJob(ctx, ports.AgentInstallJobRecord{
		Target: string(job.Target), Status: string(job.Status), Method: job.Method,
		Command: job.Command, ExpectedDestination: job.ExpectedDestination,
		Output: job.Output, Error: job.Error, StartedAt: *job.StartedAt,
		FinishedAt: job.FinishedAt, UpdatedAt: updatedAt,
	})
}

func jobFromRecord(record ports.AgentInstallJobRecord) Job {
	startedAt := record.StartedAt
	updatedAt := record.UpdatedAt
	return Job{
		Target: Target(record.Target), Status: Status(record.Status), Method: record.Method,
		Command: record.Command, ExpectedDestination: record.ExpectedDestination,
		Output: record.Output, Error: record.Error, StartedAt: &startedAt,
		FinishedAt: record.FinishedAt, UpdatedAt: &updatedAt,
	}
}

func activeStatus(status Status) bool {
	return status == StatusRunning || status == StatusWaitingForIdle || status == StatusInstalling || status == StatusVerifying
}

func combineOutput(existing, next string) string {
	out := &capturedOutput{max: maxOutputBytes}
	_, _ = out.Write([]byte(existing))
	if existing != "" && next != "" && !strings.HasSuffix(existing, "\n") {
		_, _ = out.Write([]byte("\n"))
	}
	_, _ = out.Write([]byte(next))
	return out.String()
}
