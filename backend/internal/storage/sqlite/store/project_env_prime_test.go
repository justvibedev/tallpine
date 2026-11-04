package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// blockingKeys stands in for a Keychain read waiting on an access prompt.
type blockingKeys struct {
	*fakeKeys
	entered, release chan struct{}
}

func (b *blockingKeys) Key(ctx context.Context) ([]byte, error) {
	select {
	case b.entered <- struct{}{}:
	default:
	}
	<-b.release
	return b.fakeKeys.Key(ctx)
}

// TestProjectEnvKeyReadDoesNotHoldWriteLock: a key read stuck on the Keychain
// must not block other store writes (it once froze every session spawn).
func TestProjectEnvKeyReadDoesNotHoldWriteLock(t *testing.T) {
	s := newTestStore(t)
	keys := &blockingKeys{fakeKeys: newFakeKeys(), entered: make(chan struct{}, 1), release: make(chan struct{})}
	s.SetEnvCodec(store.NewEnvCodec(keys, on))
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := s.UpsertProject(context.Background(), domain.ProjectRecord{ID: "p1", Path: "/tmp/p1", RegisteredAt: time.Now().UTC(), Config: domain.ProjectConfig{Env: map[string]string{"TOKEN": "x"}}}); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-keys.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("key never read")
	}
	other := make(chan struct{})
	go func() {
		defer close(other)
		_ = s.SetProjectCreator(context.Background(), domain.ProjectID("a"), domain.ProjectID("b"), time.Now())
	}()
	select {
	case <-other:
	case <-time.After(3 * time.Second):
		close(keys.release)
		t.Fatal("another write blocked behind the Keychain read")
	}
	close(keys.release)
	<-done
}

// TestProjectEnvSealedRowKeyReadIsPrimedWithSealingOff: after the hardened
// profile is turned off, saving a sealed project still needs the key. That read
// must happen before the write lock, not under it.
func TestProjectEnvSealedRowKeyReadIsPrimedWithSealingOff(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	fake := newFakeKeys()
	putProject(t, s, "p1", map[string]string{"A": "plain"})
	sealed, err := store.NewEnvCodec(fake, on).Seal(ctx, "p1", map[string]string{"B": "hidden"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(domain.ProjectConfig{Env: map[string]string{"B": sealed["B"]}})
	if err := s.SetRawProjectConfig(ctx, "p1", string(raw)); err != nil {
		t.Fatal(err)
	}
	keys := &blockingKeys{fakeKeys: fake, entered: make(chan struct{}, 1), release: make(chan struct{})}
	s.SetEnvCodec(store.NewEnvCodec(keys, off)) // empty key cache, sealing off
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := s.UpdateProjectSettings(ctx, "p1", "P1", domain.ProjectConfig{Env: map[string]string{"B": "n"}}); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-keys.entered:
	case <-time.After(5 * time.Second):
		close(keys.release)
		t.Fatal("key never read before the write")
	}
	other := make(chan struct{})
	go func() {
		defer close(other)
		_ = s.SetProjectCreator(ctx, domain.ProjectID("a"), domain.ProjectID("b"), time.Now())
	}()
	select {
	case <-other:
	case <-time.After(3 * time.Second):
		close(keys.release)
		t.Fatal("another write blocked behind the Keychain read")
	}
	close(keys.release)
	<-done
}
