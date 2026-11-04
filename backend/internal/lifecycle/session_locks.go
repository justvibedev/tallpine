package lifecycle

import (
	"sync"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// sessionLocks serializes the read-reduce-write sequences of one session.
// Lifecycle reducers read a session row, fold a signal into it and write it
// back, so two reducers for the same session must not interleave. Reducers for
// different sessions are independent: they used to share the manager mutex,
// which held every session's lifecycle behind one session's store round trips.
// Entries are reference counted and dropped when no reducer holds or waits on
// them, so the map tracks only sessions with work in flight.
type sessionLocks struct {
	mu    sync.Mutex
	locks map[domain.SessionID]*sessionLock
}

type sessionLock struct {
	mu   sync.Mutex
	refs int
}

// lock acquires id's lock and returns its release.
func (l *sessionLocks) lock(id domain.SessionID) func() {
	l.mu.Lock()
	if l.locks == nil {
		l.locks = map[domain.SessionID]*sessionLock{}
	}
	entry := l.locks[id]
	if entry == nil {
		entry = &sessionLock{}
		l.locks[id] = entry
	}
	entry.refs++
	l.mu.Unlock()
	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		l.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(l.locks, id)
		}
		l.mu.Unlock()
	}
}

// held reports how many sessions have a reducer holding or waiting on a lock.
func (l *sessionLocks) held() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.locks)
}
