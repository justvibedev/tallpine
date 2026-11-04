package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
)

type slowKeys struct{ entered, release chan struct{} }

func (k slowKeys) Key(context.Context) ([]byte, error) {
	k.entered <- struct{}{}
	<-k.release
	return make([]byte, 32), nil
}

// TestCachedKeyOnlyNeverWaitsForKeyRead: under the store write lock a codec
// call must fail fast while another goroutine is inside the Keychain read.
func TestCachedKeyOnlyNeverWaitsForKeyRead(t *testing.T) {
	keys := slowKeys{entered: make(chan struct{}, 1), release: make(chan struct{})}
	c := NewEnvCodec(keys, func() bool { return true })
	go func() { _, _ = c.cipherFor(context.Background()) }()
	<-keys.entered
	done := make(chan error, 1)
	go func() { _, err := c.cipherFor(cachedKeyOnly(context.Background())); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, ports.ErrProjectEnvLocked) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cached-only read waited for the Keychain")
	}
	close(keys.release)
}

// TestCachedKeyOnlyUsesLoadedKeyDuringAnotherRead: once the key is loaded, a
// cached-only call succeeds every time, even with other readers active.
func TestCachedKeyOnlyUsesLoadedKeyDuringAnotherRead(t *testing.T) {
	keys := slowKeys{entered: make(chan struct{}, 1), release: make(chan struct{})}
	close(keys.release)
	c := NewEnvCodec(keys, func() bool { return true })
	if _, err := c.cipherFor(context.Background()); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	defer close(stop)
	for i := 0; i < 4; i++ {
		go func() {
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = c.cipherFor(context.Background())
				}
			}
		}()
	}
	for i := 0; i < 20000; i++ {
		if _, err := c.cipherFor(cachedKeyOnly(context.Background())); err != nil {
			t.Fatalf("loaded key refused under contention: %v", err)
		}
	}
}
