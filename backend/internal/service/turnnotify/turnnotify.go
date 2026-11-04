// Package turnnotify synthesizes worker-to-orchestrator reports from agent
// turn facts the daemon already receives: hook signals from Terminal UI
// sessions and the Chat controller's turn lifecycle. A worker that ends its
// turn and then goes quiet without running `tallpine report`, or whose agent session
// ends, would otherwise never tell its orchestrator anything.
package turnnotify

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
)

// ChatQuietDelay is the quiet window of a synthesized chat turn_ended.
const ChatQuietDelay = 2 * time.Minute

// DefaultIdleDelay is how long a worker must stay idle after a turn ends,
// without a new prompt or a tallpine report, before the orchestrator is told.
var DefaultIdleDelay = domain.TurnFallbackDelay

const (
	// MaxExcerptCharacters bounds the quoted last assistant message.
	MaxExcerptCharacters = domain.TurnFallbackExcerptCharacters

	// KindTurnEnded labels the report message so the orchestrator can tell
	// synthesized notifications from worker-authored ones.
	KindTurnEnded = "turn_ended"
	// KindUsageLimited labels the report for a turn stopped by the provider's
	// usage limit, which resumes by itself after the reset.
	KindUsageLimited = "usage_limited"
	// KindExited labels the synthesized report for a worker that exited.
	KindExited = "exited"
	// KindStartFailed labels the synthesized report for a worker whose
	// background start gave up before its agent ran.
	KindStartFailed = "start_failed"
	// KindAuthRequired labels the needs-input report for a worker whose agent
	// login expired.
	KindAuthRequired = "auth_required"
	// KindInputRequired labels the needs-input report for a worker whose agent
	// waits on an approval or a question.
	KindInputRequired = "input_required"

	// Chat controller lifecycle events (service/chat). They carry the
	// controller generation instead of a runtime launch id.
	chatTurnStarted   = "chat.turn.started"
	chatTurnCompleted = "chat.turn.completed"
	// chatReauth is the provider refusing the account (its login expired); the
	// session waits for input instead of ending a turn.
	chatReauth = "chat.account.reauth"
	// An approval or a question the agent put to the user; the matching
	// resolved event fires once nothing is pending any more.
	chatApprovalRequested = "chat.approval.requested"
	chatInputRequested    = "chat.input.requested"
	chatApprovalResolved  = "chat.approval.resolved"
	chatInputResolved     = "chat.input.resolved"
	// chatDisconnected is a lost attachment to a persistent provider, which is
	// not proof the agent stopped; a replacement controller may reattach.
	chatDisconnected = "chat.controller.disconnected"
)

// Applier is the activity pipeline being observed.
type Applier interface {
	ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) error
}

// Sessions resolves the worker's identity.
type Sessions interface {
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
}

// Reports is the report service's create boundary.
type Reports interface {
	Create(context.Context, reportsvc.CreateInput) (domain.ReportRecord, error)
}

type stopper interface{ Stop() bool }

// Deps configures an Observer. Next and Sessions are required; Reports is
// required too but may be bound after construction with SetReports.
type Deps struct {
	Fallbacks FallbackStore
	Next      Applier
	Sessions  Sessions
	Reports   Reports
	IdleDelay time.Duration
	Log       *slog.Logger
	// AfterFunc schedules f after d; tests inject a fake clock.
	AfterFunc func(d time.Duration, f func()) stopper

	// NudgeDelay is how long a worker must stay idle after its turn ends, with
	// no report filed and no background job left running, before Tallpine nudges
	// the worker itself. Zero, or a nil Nudger, disables nudging.
	NudgeDelay time.Duration
	// NudgePoll is how often a worker still running a background job is
	// checked again, and NudgeMaxWait how long that waiting lasts at most.
	NudgePoll    time.Duration
	NudgeMaxWait time.Duration
	Nudger       Nudger
	// Jobs counts the worker's running background jobs; nil assumes none.
	Jobs Jobs
	// Now reads the clock; tests inject a fake one.
	Now func() time.Time
}

type turn struct {
	launchGeneration string
	gen              uint64
	reported         bool
	finalReported    bool
	// approvedAt is when a review_approved report was filed; a turn that ends
	// within approvalCoverWindow and did nothing else only acknowledges it.
	approvedAt time.Time
	notified   bool
	timer      stopper
	last       string

	// Nudge state. nudged covers this turn; nudges counts nudges sent since
	// the worker last reported or was given work by someone else, and
	// nudgePending marks that the next turn start is the nudge's own.
	nudged       bool
	limitUntil   time.Time // the turn ended on a usage limit until then
	limitAuto    bool
	nudgeTimer   stopper
	waitingSince time.Time
	waitedOnJob  bool
	nudges       int
	nudgePending bool

	// authReported marks that this auth-required episode already told the
	// orchestrator; a completed turn (the account works again) ends it.
	authReported bool
	hold         holdState // B362: turn_ended held back for the worker's own job
	// inputReported marks that this wait on an approval or question already
	// told the orchestrator; resolving it (or ending the turn) ends it.
	inputReported bool
}

// Observer wraps an Applier and turns Stop/SessionEnd facts into reports.
type Observer struct {
	Deps
	mu     sync.Mutex
	turns  map[domain.SessionID]*turn
	closed bool
	wake   chan struct{}
}

// Close stops all pending notifications and ignores later signals. The daemon
// calls it before detaching Chat controllers at shutdown: a provider stopping
// because this daemon is going away is not the worker's agent session ending.
func (o *Observer) Close() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closed = true
	o.wakePending()
	for id, t := range o.turns {
		o.cancel(t)
		delete(o.turns, id)
	}
}

func (o *Observer) isClosed() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.closed
}

// SetReports binds the report boundary after construction. The Chat service
// needs the observer before the report service (which reads Chat artifacts)
// exists; nothing is sent before the idle delay, long after wiring finishes.
func (o *Observer) SetReports(r Reports) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.Reports = r
}

// New constructs an Observer.
func New(d Deps) *Observer {
	if d.IdleDelay <= 0 {
		d.IdleDelay = DefaultIdleDelay
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.AfterFunc == nil {
		d.AfterFunc = func(dur time.Duration, f func()) stopper { return time.AfterFunc(dur, f) }
	}
	if d.NudgePoll <= 0 {
		d.NudgePoll = DefaultNudgePoll
	}
	if d.NudgeMaxWait <= 0 {
		d.NudgeMaxWait = DefaultNudgeMaxWait
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Observer{Deps: d, turns: map[domain.SessionID]*turn{}, wake: make(chan struct{}, 1)}
}

// ApplyActivitySignal forwards to the wrapped pipeline and, if it accepted the
// signal, updates the per-turn notification state.
func (o *Observer) ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) error {
	if err := o.Next.ApplyActivitySignal(ctx, id, s); err != nil {
		return err
	}
	if s.SubagentID != "" || nonInteractive(s.NativeEntrypoint) || o.isClosed() {
		return nil
	}
	switch {
	case s.Event == chatDisconnected:
		return nil
	case s.Event == "session-end" || (s.Valid && s.State == domain.ActivityExited):
		o.sessionEnded(ctx, id, s, s.FinalAssistantMessage)
	case s.Event == "user-prompt-submit" || s.Event == chatTurnStarted:
		o.turnStarted(ctx, id, s)
	case s.Event == chatReauth:
		o.authRequired(ctx, id, s)
	case s.Event == chatApprovalRequested || s.Event == chatInputRequested:
		o.inputRequired(ctx, id, s)
	case s.Event == chatApprovalResolved || s.Event == chatInputResolved:
		o.inputResolved(ctx, id, s)
	case s.Event == chatTurnCompleted:
		o.inputResolved(ctx, id, s)
		o.authRecovered(ctx, id, s)
		o.turnEnded(ctx, id, s, s.FinalAssistantMessage)
	case s.Event == "stop":
		if s.RunningSubagentIDs != nil && len(*s.RunningSubagentIDs) > 0 {
			return nil // a later Stop ends the turn for real
		}
		o.turnEnded(ctx, id, s, s.FinalAssistantMessage)
	}
	return nil
}

// ReportFiled suppresses idle nudges; a terminal/actionable report also
// suppresses the final-result fallback for this turn. A report can arrive
// before the observer saw the turn start (the daemon restarted mid-turn), so
// it opens the turn's state rather than being dropped; the next turn start
// clears it.
func (o *Observer) ReportFiled(r domain.ReportRecord) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	generation := ""
	if rec, found, err := o.Sessions.GetSession(ctx, r.SessionID); err == nil && found {
		generation = notificationGeneration(rec)
	}
	o.communicated(r.SessionID, r.State == domain.ReportDone || r.State == domain.ReportNeedsInput || r.State == domain.ReportStuck, generation)
}

// approvalCoverWindow bounds how long after a review_approved report a turn
// end still counts as the worker merely acknowledging it.
const approvalCoverWindow = 3 * time.Minute

// ApprovalReported records that a review_approved report was filed for the
// worker. It sets only the cover and does not mark the turn reported, so
// nudges and other notices are unaffected. The next turn end inside
// approvalCoverWindow that filed no report or message is not reported again.
func (o *Observer) ApprovalReported(r domain.ReportRecord) {
	if !r.Synthesized || !strings.HasPrefix(r.ID, domain.ReviewApprovedReportIDPrefix) || o.isClosed() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	generation := ""
	if rec, found, err := o.Sessions.GetSession(ctx, r.SessionID); err == nil && found {
		generation = notificationGeneration(rec)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.turnForGenerationLocked(r.SessionID, generation).approvedAt = o.Now()
}

// MessageSent records that sender delivered a `tallpine send` message to target.
// A worker that messaged its own project's orchestrator has already told it
// what it is doing (often while it waits on a background watcher), so the
// turn counts as reported for idle nudges. A later final result still gets a
// fallback notice. Messages to anyone else leave the turn silent.
func (o *Observer) MessageSent(ctx context.Context, sender, target domain.SessionID) {
	if o.isClosed() {
		return
	}
	if sender != target {
		// Someone handed the worker new work: its turn is no echo of the approval.
		o.mu.Lock()
		if t := o.turns[target]; t != nil {
			t.approvedAt = time.Time{}
		}
		o.mu.Unlock()
	}
	if sender == "" || sender == target {
		return
	}
	from, ok, err := o.Sessions.GetSession(ctx, sender)
	if err != nil || !ok || from.Kind != domain.KindWorker {
		return
	}
	to, ok, err := o.Sessions.GetSession(ctx, target)
	if err != nil || !ok || to.Kind != domain.KindOrchestrator || to.ProjectID != from.ProjectID {
		return
	}
	o.communicated(sender, false, notificationGeneration(from))
}

func (o *Observer) communicated(id domain.SessionID, final bool, generation string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	t := o.turnForGenerationLocked(id, generation)
	t.reported, t.approvedAt = true, time.Time{} // more than acknowledging an earlier approval
	// Later communication cannot undo a final report in this turn.
	t.finalReported = t.finalReported || final
	t.nudges, t.nudgePending = 0, false
	o.cancel(t)
}

func nonInteractive(entrypoint string) bool {
	switch strings.TrimSpace(entrypoint) {
	case "sdk-cli", "sdk-ts", "sdk-py":
		return true
	}
	return false
}

// workerGeneration identifies the live worker generation if the signal
// would be accepted by the lifecycle manager. Next cannot say whether it
// dropped a fenced signal, so the same fences apply here: terminated sessions
// are ignored, a Terminal UI signal must name the current runtime launch and
// carry no controller generation, and a Chat signal must come from the
// current controller generation (provider hooks inherited by a Chat process
// carry none). Orchestrators and reviewers never produce synthesized reports.
func (o *Observer) workerGeneration(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) string {
	rec, ok, err := o.Sessions.GetSession(ctx, id)
	if err != nil || !ok || rec.Kind != domain.KindWorker || rec.IsTerminated {
		return ""
	}
	switch domain.NormalizeSessionMode(rec.Mode) {
	case domain.SessionModeTUI:
		if s.ControllerGeneration == "" && s.LaunchID == rec.Metadata.RuntimeLaunchID {
			return notificationGeneration(rec)
		}
	case domain.SessionModeChat:
		generation := strings.TrimSpace(s.ControllerGeneration)
		if generation != "" && generation == rec.Metadata.ControllerGeneration {
			return notificationGeneration(rec)
		}
	}
	return ""
}

func notificationGeneration(rec domain.SessionRecord) string {
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		return "chat:" + rec.Metadata.ControllerGeneration
	}
	return "tui:" + rec.Metadata.RuntimeLaunchID
}

// turnForGenerationLocked scopes retained suppression and timers to a launch.
// An unknown generation (a failed report lookup) cannot invalidate known state.
func (o *Observer) turnForGenerationLocked(id domain.SessionID, generation string) *turn {
	t := o.turns[id]
	if t == nil {
		t = &turn{}
		o.turns[id] = t
	}
	if generation != "" {
		if t.launchGeneration != "" && t.launchGeneration != generation {
			o.cancel(t)
			// Keep the timer fence monotonic so old callbacks cannot match B.
			*t = turn{gen: t.gen}
		}
		t.launchGeneration = generation
	}
	return t
}

// cancel must be called with o.mu held.
func (o *Observer) cancel(t *turn) {
	t.gen++
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	if t.nudgeTimer != nil {
		t.nudgeTimer.Stop()
		t.nudgeTimer = nil
	}
}

func (o *Observer) turnStarted(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) {
	generation := o.workerGeneration(ctx, id, s)
	if generation == "" {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	t := o.turnForGenerationLocked(id, generation)
	o.cancel(t)
	t.reported, t.notified, t.last = false, false, ""
	t.finalReported = false
	t.nudged, t.waitingSince, t.waitedOnJob = false, time.Time{}, false
	t.hold = holdState{}
	if t.nudgePending {
		// This turn answers Tallpine's own nudge: it must not re-arm another one.
		t.nudgePending = false
	} else {
		t.nudges = 0
	}
}

func (o *Observer) turnEnded(ctx context.Context, id domain.SessionID, s ports.ActivitySignal, last string) {
	generation := o.workerGeneration(ctx, id, s)
	if generation == "" {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	durable := o.durableSignal(ctx, id, s)
	t := o.turnForGenerationLocked(id, generation)
	if durable {
		o.wakePending()
	}
	// Chat turn ends are definite (ACP prompt completion) and each nudge is a
	// paid prompt, so only TUI workers get the idle-check nudge.
	nudge := !t.reported && !strings.HasPrefix(generation, "chat:") && o.nudging() && !t.nudged && t.nudges < maxNudgesPerEpisode
	if !t.approvedAt.IsZero() {
		covered := o.Now().Sub(t.approvedAt) <= approvalCoverWindow && s.UsageLimitPausedUntil.IsZero()
		t.approvedAt = time.Time{}
		if covered {
			return
		}
	}
	if t.finalReported || (t.reported && strings.TrimSpace(last) == "") || (t.notified && !nudge) {
		return
	}
	o.cancel(t)
	gen := t.gen
	if !t.notified {
		t.last, t.hold = strings.TrimSpace(last), holdState{}
		t.limitUntil, t.limitAuto = s.UsageLimitPausedUntil, s.UsageLimitAutoResume
		if !t.limitUntil.IsZero() {
			// The last message is earlier work, not why the turn stopped.
			t.last = ""
		}
		if !durable {
			t.timer = o.AfterFunc(o.IdleDelay, func() { o.idleElapsed(id, gen) })
		}
	}
	if nudge {
		t.waitingSince, t.waitedOnJob = time.Time{}, false
		t.nudgeTimer = o.AfterFunc(o.NudgeDelay, func() { o.nudgeDue(id, gen) })
	}
}

// authRequired tells the worker's orchestrator, once per episode, that the
// worker's agent cannot run until its login is renewed. The turn does not end
// (it waits for input), so no turn_ended follows and the orchestrator would
// otherwise never learn the worker is stuck.
func (o *Observer) authRequired(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) {
	generation := o.workerGeneration(ctx, id, s)
	if generation == "" {
		return
	}
	o.mu.Lock()
	t := o.turnForGenerationLocked(id, generation)
	if t.authReported {
		o.mu.Unlock()
		return
	}
	t.authReported = true
	o.mu.Unlock()
	o.send(id, KindAuthRequired, "")
}

// inputRequired tells the worker's orchestrator, once per waiting episode,
// that the worker's agent is blocked on an approval or a question. Neither
// ends the turn, so no turn_ended would follow.
func (o *Observer) inputRequired(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) {
	generation := o.workerGeneration(ctx, id, s)
	if generation == "" {
		return
	}
	o.mu.Lock()
	t := o.turnForGenerationLocked(id, generation)
	if t.inputReported {
		o.mu.Unlock()
		return
	}
	t.inputReported = true
	o.mu.Unlock()
	what := strings.TrimSpace(s.Waiting)
	if s.Event == chatApprovalRequested {
		what = approvalNotice(s, what)
	} else {
		what = questionNotice(s, what)
	}
	reportID := ""
	if rid := strings.TrimSpace(s.ApprovalRequestID); s.Event == chatApprovalRequested && rid != "" {
		// A restarted daemon replays the pending request into a fresh
		// observer; the stable id makes the report idempotent across that.
		reportID = fmt.Sprintf("auto-approval:%x", sha256.Sum256([]byte(string(id)+"\x00"+rid)))
	}
	o.sendWithID(id, KindInputRequired, what, reportID)
}

// approvalNotice is the text of an approval's needs-input report. The request id
// is never cut (a cut id matches nothing): it gets its full length first, then
// the reason takes whatever the report limit leaves after the id, the command
// and a reserve for the prefix. An id over maxRequestIDCharacters is not
// printed; the orchestrator is pointed at `session approvals` instead. The id
// comes first and the command last.
func approvalNotice(s ports.ActivitySignal, reason string) string {
	var lines []string
	idLen := 0
	if id := strings.TrimSpace(s.ApprovalRequestID); id != "" {
		if utf8.RuneCountInString(id) <= maxRequestIDCharacters {
			lines = append(lines, "Request id: "+id)
			idLen = utf8.RuneCountInString(id) + len("Request id: \n")
		} else {
			lines = append(lines, "Request id too long to print: list it with `"+brand.CLIName+" session approvals <session>`")
			idLen = 100
		}
	}
	tool := cmpOr(truncate(strings.TrimSpace(s.ToolName), maxToolNameCharacters), "a tool call")
	commandLine := "(no command: open the session to see what it would change)"
	if s.ApprovalCommand != "" {
		commandLine = "Command: " + s.ApprovalCommand
	}
	head := "approval for " + tool + ":"
	room := domain.MaxReportTextCharacters - prefixReserve - idLen - utf8.RuneCountInString(commandLine) - utf8.RuneCountInString(head) - 2
	room = min(room, maxReasonCharacters)
	if room >= minReasonCharacters {
		lines = append(lines, head+" "+cmpOr(truncate(reason, room), "no reason given"))
	} else {
		lines = append(lines, head)
	}
	return strings.Join(append(lines, commandLine), "\n")
}

func cmpOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// inputResolved ends a waiting episode so a later wait reports again.
func (o *Observer) inputResolved(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) {
	generation := o.workerGeneration(ctx, id, s)
	if generation == "" {
		return
	}
	o.mu.Lock()
	o.turnForGenerationLocked(id, generation).inputReported = false
	o.mu.Unlock()
}

// authRecovered ends an auth-required episode: a turn completed, so the
// account works again and a later failure is a new episode.
func (o *Observer) authRecovered(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) {
	generation := o.workerGeneration(ctx, id, s)
	if generation == "" {
		return
	}
	o.mu.Lock()
	o.turnForGenerationLocked(id, generation).authReported = false
	o.mu.Unlock()
}

func (o *Observer) idleElapsed(id domain.SessionID, gen uint64) {
	// A durable row may have been committed after the timer was armed.
	if o.durablyCovered(id) {
		return
	}
	// A late Stop hook of the previous turn can arm this timer after the next
	// prompt began; a worker that is working is not idle. A worker paused on its
	// user (waiting_input) still counts as idle here: a TUI agent that ends its
	// turn with a question reaches waiting_input before this timer fires and
	// has no other path to the orchestrator.
	if o.workerBusy(id) {
		return
	}
	o.mu.Lock()
	t := o.turns[id]
	if o.closed || t == nil || t.gen != gen || t.finalReported || t.notified {
		o.mu.Unlock()
		return
	}
	if o.holdForOwnJob(id, t, gen) {
		o.mu.Unlock()
		return
	}
	// The pending nudge check shares this generation, so it stays armed.
	t.notified, t.timer = true, nil
	last, kind := t.last, KindTurnEnded
	if !t.limitUntil.IsZero() {
		kind, last = KindUsageLimited, usageLimitDetail(t.limitUntil, t.limitAuto)
	}
	o.mu.Unlock()
	o.send(id, kind, last)
}

func (o *Observer) sessionEnded(ctx context.Context, id domain.SessionID, s ports.ActivitySignal, last string) {
	generation := o.workerGeneration(ctx, id, s)
	if generation == "" {
		return
	}
	intentional := s.IntentionalStop
	if s.LaunchID != "" {
		if rec, found, err := o.Sessions.GetSession(ctx, id); err == nil && found {
			intentional = intentional || rec.Metadata.RuntimeReleasedLaunchID == s.LaunchID
		}
	}
	// A committed completion owns this turn's result even after restart. Keep
	// its original deadline/text and retryable report intent instead of racing
	// it with a second exit report. Emitted/suppressed intents also own the result.
	durable := false
	if !intentional && o.Fallbacks != nil {
		var err error
		durable, err = o.Fallbacks.HasCurrentCompletedTurnFallback(ctx, id)
		if err != nil {
			o.Log.Warn("check exit completion notification", "session", id, "err", err)
			return
		}
	}
	o.mu.Lock()
	t := o.turnForGenerationLocked(id, generation)
	// An unexpected exit replaces the idle timer, but must still carry a
	// newer final result that earlier progress communication did not report.
	pendingFinal := t.timer != nil && t.last != "" && !t.finalReported
	skip := intentional || durable || t.finalReported || t.notified || (t.reported && !pendingFinal)
	o.cancel(t)
	t.notified = true
	if !skip && last == "" {
		last = t.last
	}
	o.mu.Unlock()
	if !skip {
		o.send(id, KindExited, last)
	}
}

// StartFailed tells the worker's orchestrator that the worker never started.
// The spawn already answered success, so without this the orchestrator waits
// on a worker that is not running. reason is the recorded start error.
func (o *Observer) StartFailed(id domain.SessionID, reason string) {
	if o.isClosed() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rec, ok, err := o.Sessions.GetSession(ctx, id)
	if err != nil || !ok || rec.Kind != domain.KindWorker || rec.IsTerminated {
		return
	}
	o.mu.Lock()
	if t := o.turns[id]; t != nil {
		o.cancel(t)
		delete(o.turns, id)
	}
	o.mu.Unlock()
	o.send(id, KindStartFailed, reason)
}

func (o *Observer) send(id domain.SessionID, kind, last string) {
	o.sendWithID(id, kind, last, "")
}

// sendWithID is send with an explicit durable report id (empty derives one for
// exit notices only).
func (o *Observer) sendWithID(id domain.SessionID, kind, last, reportID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rec, ok, err := o.Sessions.GetSession(ctx, id)
	if err != nil || !ok || rec.IsTerminated {
		return
	}
	o.mu.Lock()
	reports, closed := o.Reports, o.closed
	o.mu.Unlock()
	if closed {
		return
	}
	if reports == nil {
		o.Log.Warn("turn notification dropped: report service not bound", "session", id, "kind", kind)
		return
	}
	// A provider can emit both a stopped event and an end-of-stream signal.
	// Keep the same durable identity across retries of this launch's exit.
	if kind == KindExited {
		generation := rec.Metadata.RuntimeLaunchID
		if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
			generation = rec.Metadata.ControllerGeneration
		}
		if generation != "" {
			reportID = fmt.Sprintf("auto-exited:%x", sha256.Sum256([]byte(string(id)+"\x00"+generation)))
		}
	}
	// A plain turn end is an FYI: it rides the next real batch or waits out
	// the quiet window instead of waking an idle orchestrator (B192). A chat
	// turn end is definite, so its quiet window is short (B255): an unreported stop
	// surfaces within ChatQuietDelay without waking the orchestrator per turn.
	in := reportsvc.CreateInput{ID: reportID, SessionID: id, Message: Message(kind, rec, last), Immediate: true, Synthesized: true, Quiet: kind == KindTurnEnded || kind == KindUsageLimited}
	if (kind == KindTurnEnded || kind == KindUsageLimited) && domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		in.QuietFor = ChatQuietDelay
	}
	if kind == KindAuthRequired || kind == KindInputRequired {
		// Actionable: the worker cannot proceed until someone renews its login.
		in.State, in.Note, in.Message = domain.ReportNeedsInput, in.Message, ""
	}
	_, err = reports.Create(ctx, in)
	if err != nil && !errors.Is(err, domain.ErrDuplicateReport) {
		o.Log.Warn("turn notification failed", "session", id, "kind", kind, "err", err)
	}
}

// maxReasonCharacters bounds an approval's stated reason and
// maxRequestIDCharacters its request id. With the command (500) and the short
// prefix they fit MaxReportTextCharacters whole.
const (
	maxToolNameCharacters  = 40
	maxReasonCharacters    = 150
	minReasonCharacters    = 10
	maxRequestIDCharacters = 128
	// prefixReserve is what the report's fixed text (label, session id, hint)
	// may use before the waiting text.
	prefixReserve = 280
)

// safeBranchName is the shape of a branch name rendered verbatim in an [auto] line.
var safeBranchName = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)

// Message renders the synthesized report text within the report size limit.
func Message(kind string, rec domain.SessionRecord, last string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[auto] %s: ", kind)
	switch kind {
	case KindExited:
		b.WriteString("the worker's agent session ended")
	case KindUsageLimited:
		// last carries "<reset>[|auto]" here; it is never quoted as a message.
		until, auto, _ := strings.Cut(last, "|")
		fmt.Fprintf(&b, "the worker's turn was stopped by the provider's usage limit; it is paused until %s", until)
		if auto != "" {
			b.WriteString(" and resumes automatically then, no action needed")
		} else {
			b.WriteString("; it will NOT resume by itself, so message it or resume it after then")
		}
		last = ""
	case KindAuthRequired:
		fmt.Fprintf(&b, "the worker's agent (%s) needs login: its authentication expired, so it is waiting and cannot continue; run the harness login, then resume it with `%s session resume-agent %s`", rec.Harness, brand.CLIName, rec.ID)
	case KindInputRequired:
		fmt.Fprintf(&b, "the worker's agent is blocked on a prompt; answer it in session %s (or `%s session approve|deny <session> <requestId>` where the project allows it)", rec.ID, brand.CLIName)
	case KindStartFailed:
		fmt.Fprintf(&b, "the worker's agent failed to start and is not running; retry it with `%s session resume-agent %s` or kill it", brand.CLIName, rec.ID)
	default:
		b.WriteString("the worker's turn ended and it went idle; this is a turn result, not a claim that the task is complete")
	}
	// A prompt is decided from its command, not its branch; the branch would only
	// eat the budget the command needs.
	if rec.Metadata.Branch != "" && kind != KindInputRequired {
		if safeBranchName.MatchString(rec.Metadata.Branch) {
			fmt.Fprintf(&b, " (branch `%s`)", rec.Metadata.Branch)
		} else {
			// The worker controls its branch name; an odd one is data, not part
			// of Tallpine's own sentence.
			fmt.Fprintf(&b, " (branch %s)", domain.FrameUntrusted(string(rec.ID), truncate(rec.Metadata.Branch, 200)))
		}
	}
	b.WriteString(".")
	if last = strings.TrimSpace(last); last != "" {
		switch kind {
		case KindStartFailed:
			b.WriteString("\nError: ")
		case KindInputRequired:
			b.WriteString("\n")
			// Whatever the prefix leaves of the report limit: the command must
			// not be cut by a smaller excerpt cap.
			b.WriteString(framedWithin(rec, last, domain.MaxReportTextCharacters-utf8.RuneCountInString(b.String())))
			return truncate(b.String(), domain.MaxReportTextCharacters)
		default:
			b.WriteString("\nLast message: ")
		}
		b.WriteString(framedWithin(rec, truncate(last, MaxExcerptCharacters), domain.MaxReportTextCharacters-utf8.RuneCountInString(b.String())))
		// A label with nothing after it (no room for the frame) is noise.
		if out := b.String(); strings.HasSuffix(out, "\nLast message: ") || strings.HasSuffix(out, "\nError: ") {
			return truncate(strings.TrimRight(out, " "), domain.MaxReportTextCharacters)
		}
	}
	return truncate(b.String(), domain.MaxReportTextCharacters)
}

// workerBusy reports whether the session is mid-turn, so a stale turn_ended
// must not claim it went idle.
func (o *Observer) workerBusy(id domain.SessionID) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rec, ok, err := o.Sessions.GetSession(ctx, id)
	if err != nil || !ok {
		return false
	}
	return rec.Activity.State.IsBusy()
}

// usageLimitDetail is Message's detail for KindUsageLimited.
func usageLimitDetail(until time.Time, auto bool) string {
	out := until.UTC().Format(time.RFC3339)
	if auto {
		out += "|auto"
	}
	return out
}
