package domain

import (
	"strings"
	"time"
)

// OrchestratorWaitTimeout is how long a worker's `tallpine report --needs-input` may
// wait on its orchestrator before the wait escalates to the human.
const OrchestratorWaitTimeout = 15 * time.Minute

// SynthesizedReportPrefix opens the note of a daemon-authored report. Those
// describe an agent-level wait (login, approval, question) that a human may
// need to answer, so they never wait on the orchestrator.
const SynthesizedReportPrefix = "[auto]"

// NeedsInputAudience says who a session's needs-input state is waiting on.
type NeedsInputAudience string

// NeedsInputAudience values. Empty means the session is not in a report-driven
// needs-input wait.
const (
	NeedsInputNone         NeedsInputAudience = ""
	NeedsInputOrchestrator NeedsInputAudience = "orchestrator"
	NeedsInputHuman        NeedsInputAudience = "human"
)

// NeedsInputFacts are the inputs to DeriveNeedsInputAudience.
type NeedsInputFacts struct {
	Activity ActivityState
	// ReportAt and ReportNote describe the worker's newest needs_input report;
	// ReportAt is zero when it has none.
	ReportAt   time.Time
	ReportNote string
	// OrchestratorLive reports whether the worker's project has a live orchestrator.
	OrchestratorLive bool
	// InteractionAt is when the orchestrator (or a human) last directed the
	// session; PromptAt is the newest human prompt.
	InteractionAt time.Time
	PromptAt      time.Time
	Now           time.Time
}

// DeriveNeedsInputAudience is the single rule for whether a needs-input state
// needs the human or only the orchestrator. Every client reads its result from
// the session DTO instead of re-deriving it.
func DeriveNeedsInputAudience(f NeedsInputFacts) NeedsInputAudience {
	reportWait := !f.ReportAt.IsZero()
	if reportWait && (f.InteractionAt.After(f.ReportAt) || f.PromptAt.After(f.ReportAt)) {
		reportWait = false // the orchestrator acted, or a new turn began
	}
	if reportWait && f.Activity == ActivityActive {
		reportWait = false // mid-turn: not waiting right now
	}
	// Every waiting_input or blocked reading is an agent-level wait (an approval,
	// a question, a login, a TUI permission-request or notification hook) that a
	// human may need to answer; an agent that merely finished its turn reads idle.
	if f.Activity.NeedsInput() {
		return NeedsInputHuman
	}
	if !reportWait {
		return NeedsInputNone
	}
	switch {
	case strings.HasPrefix(f.ReportNote, SynthesizedReportPrefix):
		return NeedsInputHuman // agent-level wait filed by the daemon
	case !f.OrchestratorLive:
		return NeedsInputHuman
	case f.Now.Sub(f.ReportAt) >= OrchestratorWaitTimeout:
		return NeedsInputHuman
	}
	return NeedsInputOrchestrator
}

// NewNeedsInputFacts assembles the facts for a worker. report is the worker's
// open needs_input report (the zero record when it has none).
func NewNeedsInputFacts(rec SessionRecord, report ReportRecord, orchestratorLive bool, now time.Time) NeedsInputFacts {
	return NeedsInputFacts{
		Activity:         rec.Activity.State,
		ReportAt:         report.CreatedAt,
		ReportNote:       report.Note,
		OrchestratorLive: orchestratorLive,
		InteractionAt:    rec.Metadata.LatestInteractionAt,
		PromptAt:         rec.Metadata.LatestUserPromptAt,
		Now:              now,
	}
}

// NeedsInputEscalationSourceKeyPrefix opens the source key of the notification
// sent when an orchestrator-bound wait escalates to the human. The key carries
// the report id, so a restart cannot send the same escalation twice.
const NeedsInputEscalationSourceKeyPrefix = "needs-input-escalation:"
