package sessionmanager

import (
	"sync"
	"testing"
)

func TestKeyedLocksFreeEntriesAndSerialize(t *testing.T) {
	l := newKeyedLocks[string]()
	counter := 0
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer l.lock("a")()
			counter++
		}()
	}
	wg.Wait()
	if counter != 50 {
		t.Fatalf("counter = %d, want 50", counter)
	}
	l.lock("b")()
	if n := l.len(); n != 0 {
		t.Fatalf("entries = %d after all unlocks, want 0", n)
	}
}
