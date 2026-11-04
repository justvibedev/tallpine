package chat

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// ErrUsageLimitAutoResumeInvalid rejects a source that cannot safely resume.
var ErrUsageLimitAutoResumeInvalid = errors.New("usage-limit auto-resume is unavailable")

// ErrUsageLimitResetUnknown refuses scheduling without a known reset deadline.
var ErrUsageLimitResetUnknown = errors.New("the provider did not report a known usage-limit reset time")

const usageLimitAutoResumeBackoff = 30 * time.Second

// Bound provider processes and wakes without making one session block another.
const usageLimitAutoResumeWorkers = 4

type usageLimitStore interface {
	SetTurnAutoResume(context.Context, string, string, *time.Time, bool, string) error
	PostponeTurnAutoResume(context.Context, string, string, time.Time) (bool, error)
	AutoResumeTurns(context.Context) ([]domain.ConversationTurn, error)
	LatestAutoResumeTurn(context.Context, string) (domain.ConversationTurn, error)
	AppendAutoResumeRetryUserMessage(context.Context, string, domain.SessionID, string, domain.ConversationMessage, string, string, time.Time) (bool, error)
}

type usageLimitAccountReader interface {
	SessionLimits(context.Context, domain.SessionID, domain.AgentHarness, bool) (string, domain.AgentAccountLimits, error)
}

// UsageLimitFailover moves a limited session to another account instead of
// waiting for its limit to reset.
type UsageLimitFailover interface {
	// Enabled reports whether failover is switched on now.
	Enabled() bool
	// Failover starts a move and returns its id, or "" to wait for the reset.
	Failover(ctx context.Context, id domain.SessionID, harness domain.AgentHarness) string
	// MoveFinished reports whether a started move ended, and whether the
	// session now runs on the new account.
	MoveFinished(ctx context.Context, transitionID string) (finished, moved bool)
}

// SetUsageLimitFailover turns on failover; nil keeps waiting for resets.
func (s *Service) SetUsageLimitFailover(f UsageLimitFailover) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usageLimitFailover = f
}

// SetUsageLimitAccounts shares the account service's provider readings with recovery.
func (s *Service) SetUsageLimitAccounts(accounts usageLimitAccountReader) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usageLimitAccounts = accounts
}

// SetTurnAutoResume arms one failed human prompt, rather than a session-wide
// policy. Cancellation needs no live controller and remains available while busy.
func (s *Service) SetTurnAutoResume(ctx context.Context, id domain.SessionID, turnID string, enabled bool) error {
	store, ok := s.store.(usageLimitStore)
	if !ok {
		return ErrUsageLimitAutoResumeInvalid
	}
	record, found, err := s.sessions.GetSession(ctx, id)
	if err != nil {
		return err
	} else if !found {
		return ports.ErrSessionNotFound
	}
	conversation, err := s.store.ConversationForSession(ctx, id)
	if errors.Is(err, domain.ErrNoConversation) {
		return domain.ErrNoConversationTurn
	}
	if err != nil {
		return err
	}
	source, err := s.store.TurnByID(ctx, turnID)
	if err != nil {
		return err
	}
	if source.ConversationID != conversation.ID {
		return domain.ErrNoConversationTurn
	}
	if !enabled {
		err = store.SetTurnAutoResume(ctx, conversation.ID, turnID, nil, false, "")
		if err == nil {
			s.autoResumeMu.Lock()
			delete(s.autoResumeRetryAt, turnID)
			s.autoResumeMu.Unlock()
		}
		return err
	}
	if err := validateUsageLimitSession(record); err != nil {
		return err
	}
	if err := validateUsageLimitSource(ctx, s.store, store, source, id, record.Kind); err != nil {
		return err
	}
	err = store.SetTurnAutoResume(ctx, conversation.ID, turnID, source.UsageLimit.ResetsAt, true, "")
	if err == nil {
		s.autoResumeMu.Lock()
		delete(s.autoResumeRetryAt, turnID)
		s.autoResumeMu.Unlock()
	}
	return err
}

func validateUsageLimitSession(record domain.SessionRecord) error {
	if domain.NormalizeSessionMode(record.Mode) != domain.SessionModeChat {
		return ErrNotChatMode
	}
	if record.IsTerminated || record.ProvisionState.WithDefault() != domain.SessionProvisionReady {
		return fmt.Errorf("%w: this session is no longer ready", ErrUsageLimitAutoResumeInvalid)
	}
	if record.Activity.State == domain.ActivityBlocked || record.Activity.State == domain.ActivityWaitingInput {
		return fmt.Errorf("%w: the agent needs input", ErrUsageLimitAutoResumeInvalid)
	}
	return nil
}

// Account readings can extend a known deadline, never shorten it or create consent.
func (s *Service) postponeUsageLimit(ctx context.Context, store usageLimitStore, source domain.ConversationTurn, harness domain.AgentHarness) (bool, error) {
	s.mu.RLock()
	accounts := s.usageLimitAccounts
	s.mu.RUnlock()
	if accounts == nil {
		return false, nil
	}
	// An optional quota probe must not hold up the already-authorized retry.
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	_, limits, err := accounts.SessionLimits(readCtx, source.HandledBySessionID, harness, true)
	cancel()
	if errors.Is(err, ports.ErrAgentAccountChanged) {
		return false, err
	}
	if err != nil {
		s.log.Debug("usage-limit account refresh unavailable; using turn reset", "session", source.HandledBySessionID, "error", err)
		limits.AutoResumeResetsAt = nil
	}
	if limits.AutoResumeResetsAt == nil || !limits.AutoResumeResetsAt.After(s.now()) ||
		!limits.AutoResumeResetsAt.After(*source.UsageLimit.ResetsAt) {
		return false, nil
	}
	// A retry or account move may consume consent during the probe. A zero-row
	// update leaves it consumed and ends this attempt without dispatching.
	_, err = store.PostponeTurnAutoResume(ctx, source.ConversationID, source.ID, *limits.AutoResumeResetsAt)
	return err == nil, err
}

// unattendedSessionKind reports sessions with no human at the keyboard: workers
// and orchestrators. Their turns are mostly automation-origin (orchestrator
// sends, relayed reports, review/CI deliveries), and replaying those after the
// reset is the point of auto-resume; an interactive chat only replays what a
// person typed.
func unattendedSessionKind(kind domain.SessionKind) bool {
	return kind == domain.KindWorker || kind == domain.KindOrchestrator
}

func usageLimitReplayable(origin domain.MessageOrigin, kind domain.SessionKind) bool {
	return origin == domain.MessageOriginHuman || (origin == domain.MessageOriginAutomation && unattendedSessionKind(kind))
}

func validateUsageLimitSource(ctx context.Context, turns Store, store usageLimitStore, source domain.ConversationTurn, sessionID domain.SessionID, kind domain.SessionKind) error {
	if source.State != domain.TurnStateFailed || source.RolledBackAt != nil || source.UsageLimit == nil || source.HandledBySessionID != sessionID {
		return fmt.Errorf("%w: this turn is not an actionable usage-limit failure", ErrUsageLimitAutoResumeInvalid)
	}
	if source.UsageLimit.ResetsAt == nil {
		return ErrUsageLimitResetUnknown
	}
	prompt, err := turns.RetryPrompt(ctx, source.ConversationID, source.ID)
	if err != nil {
		return err
	}
	if !prompt.ActiveLineage {
		return ErrRetryStaleBranch
	}
	if !usageLimitReplayable(prompt.Origin, kind) {
		return ErrTurnNotRetryable
	}
	if source.ProviderTurnID == "" {
		return ErrRetryDeliveryUncertain
	}
	if _, retried, err := turns.RetryTurnIDForSource(ctx, source.ConversationID, source.ID); err != nil {
		return err
	} else if retried {
		return fmt.Errorf("%w: this turn already has a retry", ErrUsageLimitAutoResumeInvalid)
	}
	latest, err := store.LatestAutoResumeTurn(ctx, source.ConversationID)
	if err != nil {
		return err
	}
	if latest.ID != source.ID {
		return fmt.Errorf("%w: a newer turn superseded this prompt", ErrUsageLimitAutoResumeInvalid)
	}
	return nil
}

// retryUsageLimitTurn uses RetryTurn's durable idempotency and provider dispatch,
// and rechecks intent and lineage under the same lock as sends and queue drains.
func (c *Controller) retryUsageLimitTurn(ctx context.Context, turnID string) (domain.ConversationTurn, error) {
	if !c.sendMu.TryLock() {
		return domain.ConversationTurn{}, ErrTurnRunning
	}
	defer c.sendMu.Unlock()
	store, ok := c.store.(usageLimitStore)
	if !ok {
		return domain.ConversationTurn{}, ErrUsageLimitAutoResumeInvalid
	}
	source, err := c.store.TurnByID(ctx, turnID)
	if err != nil {
		return domain.ConversationTurn{}, err
	}
	if source.ConversationID != c.conversation.ID || source.UsageLimit == nil || !source.UsageLimit.AutoResume {
		return domain.ConversationTurn{}, ErrUsageLimitAutoResumeInvalid
	}
	if err := validateUsageLimitSource(ctx, c.store, store, source, c.sessionID, c.kind); err != nil {
		return domain.ConversationTurn{}, err
	}
	if source.UsageLimit.ResetsAt.After(c.now()) {
		return domain.ConversationTurn{}, domain.ErrConversationAutoResumeNotDue
	}
	return c.retryTurnLocked(ctx, turnID, true, true)
}

func permanentUsageLimitResumeError(err error) bool {
	return errors.Is(err, ErrUsageLimitAutoResumeInvalid) ||
		errors.Is(err, ErrUsageLimitResetUnknown) ||
		errors.Is(err, ports.ErrSessionNotFound) ||
		errors.Is(err, ErrNotChatMode) ||
		errors.Is(err, domain.ErrNoConversation) ||
		errors.Is(err, domain.ErrNoConversationTurn) ||
		errors.Is(err, domain.ErrConversationRebound) ||
		errors.Is(err, ErrTurnNotRetryable) ||
		errors.Is(err, ErrRetryStaleBranch) ||
		errors.Is(err, ErrRetryDeliveryUncertain) ||
		errors.Is(err, ErrRetryContentInvalid) ||
		errors.Is(err, ErrRetryUnsupported) ||
		errors.Is(err, ports.ErrAgentAccountChanged) ||
		errors.Is(err, ports.ErrChatAuthRequired)
}

// ResumeUsageLimitedTurns preserves consent through temporary failures. Only a
// permanent refusal or the transaction recording a retry consumes it.
func (s *Service) ResumeUsageLimitedTurns(ctx context.Context) error {
	s.finishFailoverMoves(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	store, ok := s.store.(usageLimitStore)
	if !ok {
		return nil
	}
	s.autoResumeMu.Lock()
	if s.autoResumeScanning || len(s.autoResumeInFlight) >= usageLimitAutoResumeWorkers {
		s.autoResumeMu.Unlock()
		return nil
	}
	s.autoResumeScanning = true
	s.autoResumeMu.Unlock()
	sources, err := store.AutoResumeTurns(ctx)
	// Enabled resolves the agent policy (a database read, maybe a process
	// exec), so it runs before any service lock is taken.
	failoverOn := s.failoverEnabled()
	armed := make(map[string]bool, len(sources))
	for _, source := range sources {
		armed[source.ID] = true
	}
	s.autoResumeMu.Lock()
	s.autoResumeScanning = false
	if err != nil || ctx.Err() != nil {
		s.autoResumeMu.Unlock()
		return errors.Join(err, ctx.Err())
	}
	for id := range s.autoResumeRetryAt {
		if !armed[id] {
			delete(s.autoResumeRetryAt, id)
		}
	}
	var claimed []domain.ConversationTurn
	for _, candidate := range sources {
		if len(s.autoResumeInFlight) >= usageLimitAutoResumeWorkers {
			break
		}
		if s.autoResumeInFlight[candidate.ID] || s.autoResumeRetryAt[candidate.ID].After(s.now()) {
			continue
		}
		// Eligibility reads are needed only once the durable deadline arrives;
		// with failover on, a waiting turn is also looked at for a move.
		if candidate.UsageLimit != nil && candidate.UsageLimit.ResetsAt != nil && candidate.UsageLimit.ResetsAt.After(s.now()) &&
			!failoverOn {
			continue
		}
		if s.autoResumeInFlight == nil {
			s.autoResumeInFlight = make(map[string]bool)
		}
		s.autoResumeInFlight[candidate.ID] = true
		claimed = append(claimed, candidate)
	}
	s.autoResumeMu.Unlock()

	failures := make(chan error, len(claimed))
	var workers sync.WaitGroup
	for _, candidate := range claimed {
		workers.Go(func() {
			defer func() {
				s.autoResumeMu.Lock()
				delete(s.autoResumeInFlight, candidate.ID)
				s.autoResumeMu.Unlock()
			}()
			if err := s.resumeUsageLimitedTurn(ctx, store, candidate); err != nil {
				failures <- err
			}
		})
	}
	workers.Wait()
	close(failures)
	var errs []error
	for err := range failures {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (s *Service) resumeUsageLimitedTurn(ctx context.Context, store usageLimitStore, candidate domain.ConversationTurn) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// A cancellation or manual retry can consume intent after the scan.
	source, err := s.store.TurnByID(ctx, candidate.ID)
	var record domain.SessionRecord
	if err == nil {
		if source.UsageLimit != nil && !source.UsageLimit.AutoResume {
			return nil
		}
		if source.UsageLimit != nil && source.UsageLimit.ResetsAt != nil && source.UsageLimit.ResetsAt.After(s.now()) {
			s.failoverUsageLimitedTurn(ctx, store, source)
			return nil
		}
		record, err = s.requireChatSession(ctx, source.HandledBySessionID)
		if err == nil {
			err = validateUsageLimitSession(record)
		}
	}
	if err == nil {
		err = validateUsageLimitSource(ctx, s.store, store, source, source.HandledBySessionID, record.Kind)
	}
	if err == nil {
		var postponed bool
		postponed, err = s.postponeUsageLimit(ctx, store, source, record.Harness)
		if postponed {
			return nil
		}
	}
	if err == nil {
		// Hibernation recovery already permits a 60-second native wake.
		attemptCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		_, err = s.retryTurn(attemptCtx, source.HandledBySessionID, source.ID, true, false)
		cancel()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, domain.ErrConversationAutoResumeNotDue) {
		return nil
	}
	if permanentUsageLimitResumeError(err) {
		err = store.SetTurnAutoResume(ctx, candidate.ConversationID, candidate.ID, nil, false,
			"Auto-resume stopped: "+err.Error())
	}
	s.autoResumeMu.Lock()
	defer s.autoResumeMu.Unlock()
	if err == nil {
		delete(s.autoResumeRetryAt, candidate.ID)
	} else {
		if s.autoResumeRetryAt == nil {
			s.autoResumeRetryAt = make(map[string]time.Time)
		}
		s.autoResumeRetryAt[candidate.ID] = s.now().Add(usageLimitAutoResumeBackoff)
	}
	return err
}

// failoverRecheck is how long a turn that found no account to move to is left
// alone before failover looks again.
const failoverRecheck = time.Minute

// failoverEnabled reports whether failover is on now. It reads the pointer
// under the lock and asks it with no lock held.
func (s *Service) failoverEnabled() bool {
	s.mu.RLock()
	failover := s.usageLimitFailover
	s.mu.RUnlock()
	return failover != nil && failover.Enabled()
}

// failoverMove is a move started for an armed turn, followed until it ends and
// its turn is retried on the new account.
type failoverMove struct {
	session domain.SessionID
	moveID  string
	// moved is set once the move ended on the new account; the commit disarmed
	// the turn, so the retry is kept until it succeeds or cannot succeed.
	moved    bool
	attempts int
	retryAt  time.Time
}

// failoverRetryAttempts bounds the retries after a move that fail for a
// passing reason, with failoverRetryBackoff between them.
const (
	failoverRetryAttempts = 5
	failoverRetryBackoff  = 30 * time.Second
)

// failoverUsageLimitedTurn moves the session of an armed turn that is still
// waiting for its reset to an account with headroom. The retry on the new
// account is driven by finishFailoverMoves, not by the armed-turn scan: the
// move's commit rebinds the session, which disarms the turn. Any trouble here
// leaves the turn armed to resume at the reset.
func (s *Service) failoverUsageLimitedTurn(ctx context.Context, store usageLimitStore, source domain.ConversationTurn) {
	s.mu.RLock()
	failover := s.usageLimitFailover
	s.mu.RUnlock()
	if failover == nil || !failover.Enabled() {
		return
	}
	s.autoResumeMu.Lock()
	_, moving := s.failoverMoves[source.ID]
	waiting := s.failoverRetryAt[source.ID].After(s.now())
	if !waiting {
		delete(s.failoverRetryAt, source.ID)
	}
	s.autoResumeMu.Unlock()
	if moving || waiting {
		return
	}
	record, err := s.requireChatSession(ctx, source.HandledBySessionID)
	if err == nil {
		err = validateUsageLimitSession(record)
	}
	if err == nil {
		err = validateUsageLimitSource(ctx, s.store, store, source, source.HandledBySessionID, record.Kind)
	}
	if err != nil {
		return
	}
	moveID := failover.Failover(ctx, source.HandledBySessionID, record.Harness)
	s.autoResumeMu.Lock()
	if moveID == "" {
		// Skip the reads above until the next worthwhile attempt.
		if s.failoverRetryAt == nil {
			s.failoverRetryAt = make(map[string]time.Time)
		}
		s.failoverRetryAt[source.ID] = s.now().Add(failoverRecheck)
		s.autoResumeMu.Unlock()
		return
	}
	if s.failoverMoves == nil {
		s.failoverMoves = make(map[string]failoverMove)
	}
	move := failoverMove{session: source.HandledBySessionID, moveID: moveID}
	s.failoverMoves[source.ID] = move
	s.autoResumeMu.Unlock()
	s.persistFailoverMove(ctx, source.ID, move)
}

// finishFailoverMoves retries the turn of every started move that has ended on
// the new account. It runs on each scan whether or not the turn is still
// armed, because the commit of the move disarms it.
func (s *Service) finishFailoverMoves(ctx context.Context) {
	s.loadFailoverMoves(ctx)
	s.mu.RLock()
	failover := s.usageLimitFailover
	s.mu.RUnlock()
	store, ok := s.store.(usageLimitStore)
	if failover == nil || !ok {
		return
	}
	s.autoResumeMu.Lock()
	pending := make(map[string]failoverMove, len(s.failoverMoves))
	for turnID, move := range s.failoverMoves {
		pending[turnID] = move
	}
	s.autoResumeMu.Unlock()
	var wg sync.WaitGroup
	for turnID, move := range pending {
		if !move.moved {
			finished, moved := failover.MoveFinished(ctx, move.moveID)
			if !finished {
				continue
			}
			if !moved {
				s.dropFailoverMove(ctx, turnID, move)
				continue
			}
			move.moved = true
			s.disarmMovedTurn(ctx, store, turnID)
		} else if s.now().Before(move.retryAt) {
			continue
		}
		// Whoever takes the entry owns this attempt; a failed one puts it back.
		s.autoResumeMu.Lock()
		cur, mine := s.failoverMoves[turnID]
		if mine && cur.moveID == move.moveID {
			delete(s.failoverMoves, turnID)
		} else {
			mine = false
		}
		s.autoResumeMu.Unlock()
		if !mine {
			continue
		}
		wg.Go(func() { s.retryAfterFailover(ctx, store, turnID, move) })
	}
	wg.Wait()
}

// retryAfterFailover retries the turn through the manual path. The armed
// consent was for the old account; the move is the user's failover choice. A
// retry that fails for a passing reason is kept for another attempt, because
// the move's commit already disarmed the turn.
func (s *Service) retryAfterFailover(ctx context.Context, store usageLimitStore, turnID string, move failoverMove) {
	source, err := s.store.TurnByID(ctx, turnID)
	if err == nil {
		var record domain.SessionRecord
		record, err = s.requireChatSession(ctx, move.session)
		if err == nil {
			err = validateUsageLimitSource(ctx, s.store, store, source, move.session, record.Kind)
		}
	}
	if err == nil {
		retryCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		_, err = s.retryTurn(retryCtx, move.session, turnID, false, true)
		cancel()
	}
	if err == nil {
		s.forgetFailoverMove(ctx, turnID)
		return
	}
	move.attempts++
	if permanentUsageLimitResumeError(err) || move.attempts >= failoverRetryAttempts {
		s.log.Warn("usage-limit failover: retry after the move failed; giving up", "session", move.session, "turn", turnID, "error", err)
		s.forgetFailoverMove(ctx, turnID)
		return
	}
	s.log.Info("usage-limit failover: retry after the move failed; trying again", "session", move.session, "turn", turnID, "error", err)
	move.retryAt = s.now().Add(failoverRetryBackoff)
	s.autoResumeMu.Lock()
	if s.failoverMoves == nil {
		s.failoverMoves = make(map[string]failoverMove)
	}
	_, taken := s.failoverMoves[turnID]
	if !taken {
		s.failoverMoves[turnID] = move
	}
	s.autoResumeMu.Unlock()
	if !taken {
		s.persistFailoverMove(ctx, turnID, move)
	}
}
