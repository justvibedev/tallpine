package sessionmanager

import (
	"context"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// semanticAcceptanceTimeout is how long SendSemantic waits for the agent to
// accept a typed message before reporting that it has not.
const semanticAcceptanceTimeout = 10 * time.Second

func (m *Manager) semanticAcceptanceTimeout() time.Duration {
	if m.sendConfirm.semanticAcceptance > 0 {
		return m.sendConfirm.semanticAcceptance
	}
	return semanticAcceptanceTimeout
}

// semanticTurnUnfinished reports a turn still holding typed input: running, or
// stopped on a permission decision that keeps it open until someone answers.
func semanticTurnUnfinished(rec domain.SessionRecord) bool {
	return rec.Activity.State == domain.ActivityActive || rec.Activity.State == domain.ActivityBlocked
}

// awaitSemanticAcceptance polls until the agent accepts the message or the
// acceptance window closes, returning the last session state it read. An
// accepted message is forgotten here, so callers just return.
func (m *Manager) awaitSemanticAcceptance(ctx context.Context, id domain.SessionID, key string) (domain.SessionRecord, bool, error) {
	deadline := time.NewTimer(m.semanticAcceptanceTimeout())
	defer deadline.Stop()
	ticker := time.NewTicker(m.sendConfirm.pollInterval)
	defer ticker.Stop()
	for {
		current, found, err := m.store.GetSession(ctx, id)
		if err != nil {
			return domain.SessionRecord{}, false, err
		}
		if !found || current.IsTerminated || current.Activity.State == domain.ActivityExited {
			return domain.SessionRecord{}, false, ErrTerminated
		}
		if semanticMessageAccepted(current, key) {
			m.semanticPastes.forget(id, key)
			return current, true, nil
		}
		select {
		case <-ctx.Done():
			return domain.SessionRecord{}, false, ctx.Err()
		case <-deadline.C:
			return current, false, nil
		case <-ticker.C:
		}
	}
}

// semanticPasteLedger remembers which report batches were typed into each
// terminal session and under which launch, so no batch is typed twice during one
// turn. It is memory only: after a daemon restart a batch is typed once more,
// the same as any retry.
type semanticPasteLedger struct {
	mu       sync.Mutex
	launches map[domain.SessionID]map[string]string
}

func (l *semanticPasteLedger) written(id domain.SessionID, key, launch string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	typedIn, ok := l.launches[id][key]
	return ok && typedIn == launch
}

// record notes a typed batch and drops batches typed into earlier launches,
// which that launch's agent can no longer accept.
func (l *semanticPasteLedger) record(id domain.SessionID, key, launch string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.launches == nil {
		l.launches = map[domain.SessionID]map[string]string{}
	}
	batches := l.launches[id]
	if batches == nil {
		batches = map[string]string{}
		l.launches[id] = batches
	}
	for other, typedIn := range batches {
		if typedIn != launch {
			delete(batches, other)
		}
	}
	batches[key] = launch
}

func (l *semanticPasteLedger) forget(id domain.SessionID, key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.launches[id], key)
	if len(l.launches[id]) == 0 {
		delete(l.launches, id)
	}
}
