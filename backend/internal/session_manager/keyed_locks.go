package sessionmanager

import "sync"

// keyedLocks is a set of per-key mutexes whose entries are freed as soon as no
// caller holds or waits for them.
type keyedLocks[K comparable] struct {
	mu      sync.Mutex
	entries map[K]*keyedLockEntry
}

type keyedLockEntry struct {
	mu   sync.Mutex
	refs int
}

func newKeyedLocks[K comparable]() *keyedLocks[K] {
	return &keyedLocks[K]{entries: make(map[K]*keyedLockEntry)}
}

// lock blocks until key's mutex is held and returns the function that releases
// it.
func (l *keyedLocks[K]) lock(key K) (unlock func()) {
	l.mu.Lock()
	e := l.entries[key]
	if e == nil {
		e = &keyedLockEntry{}
		l.entries[key] = e
	}
	e.refs++
	l.mu.Unlock()
	e.mu.Lock()
	return func() {
		e.mu.Unlock()
		l.mu.Lock()
		e.refs--
		if e.refs == 0 {
			delete(l.entries, key)
		}
		l.mu.Unlock()
	}
}

func (l *keyedLocks[K]) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}
