package turnnotify

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// B799: a chat worker that ends its turn on "Waiting for that run to finish."
// while its own background test run is still going is not idle. Its durable
// turn_ended must wait for the job (and a short wake-up window) instead of
// telling the orchestrator it went idle.
func TestChatTurnEndedHeldWhileOwnJobRuns(t *testing.T) {
	h := newDurableHarness(t, domain.SessionModeChat)
	jobs := &fakeJobs{running: 1}
	h.o.Jobs = jobs
	h.complete("Waiting for that run to finish.")
	h.now = h.now.Add(h.o.NudgePoll)
	if next := h.o.deliverPending(context.Background()); next > h.o.NudgePoll {
		t.Fatalf("next check in %v, want at most %v while the job runs", next, h.o.NudgePoll)
	}
	if n := len(h.autoReports()); n != 0 {
		t.Fatalf("reports while the worker's job runs = %d, want 0", n)
	}

	// The job ended but the worker has not started its wake-up turn yet.
	jobs.running = 0
	h.now = h.now.Add(h.o.IdleDelay / 2)
	h.o.deliverPending(context.Background())
	if n := len(h.autoReports()); n != 0 {
		t.Fatalf("reports inside the post-job window = %d, want 0", n)
	}

	// Nothing woke the worker after the job: the orchestrator must hear it.
	h.now = h.now.Add(h.o.IdleDelay)
	h.o.deliverPending(context.Background())
	if n := len(h.autoReports()); n != 1 {
		t.Fatalf("reports after the job ended and the worker stayed silent = %d, want 1", n)
	}
}

// The job's completion wakes the worker: that new turn retires the held row,
// so no stale "went idle" notice is ever delivered.
func TestChatTurnEndedHeldJobWakeRetiresNotice(t *testing.T) {
	h := newDurableHarness(t, domain.SessionModeChat)
	jobs := &fakeJobs{running: 1}
	h.o.Jobs = jobs
	h.complete("Waiting for the validation and EditMode run.")
	h.now = h.now.Add(h.o.NudgePoll)
	h.o.deliverPending(context.Background())
	jobs.running = 0
	h.start("turn-2")
	h.now = h.now.Add(2 * h.o.IdleDelay)
	h.o.deliverPending(context.Background())
	if n := len(h.autoReports()); n != 0 {
		t.Fatalf("reports after the job woke the worker = %d, want 0", n)
	}
	if len(h.o.jobHolds) != 0 {
		t.Fatalf("hold state kept for a retired row: %v", h.o.jobHolds)
	}
}

// A job that never ends (a dev server) holds the notice at most NudgeMaxWait.
func TestChatTurnEndedJobHoldIsBounded(t *testing.T) {
	h := newDurableHarness(t, domain.SessionModeChat)
	h.o.Jobs = &fakeJobs{running: 1}
	h.complete("Server is up; waiting for the owner.")
	h.now = h.now.Add(h.o.NudgeMaxWait)
	h.o.deliverPending(context.Background())
	if n := len(h.autoReports()); n != 1 {
		t.Fatalf("reports after NudgeMaxWait with a job still running = %d, want 1", n)
	}
}
