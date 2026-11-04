package store_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// plantedKeys models a Keychain holding a key another process planted; only
// Recreate (delete + create by the daemon) yields the daemon's own key.
type plantedKeys struct {
	current   atomic.Value // []byte
	recreated atomic.Int32
}

func newPlantedKeys() *plantedKeys {
	k := &plantedKeys{}
	k.current.Store(bytes.Repeat([]byte{0xAA}, 32)) // the attacker's known key
	return k
}

func (k *plantedKeys) Key(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(ports.ErrSecretKeyUnavailable, err)
	}
	return k.current.Load().([]byte), nil
}

func (k *plantedKeys) Recreate(context.Context) ([]byte, error) {
	n := k.recreated.Add(1) // each recreation is a fresh key: 0x42, 0x43, ...
	k.current.Store(bytes.Repeat([]byte{byte(0x41 + n)}, 32))
	return k.current.Load().([]byte), nil
}

func TestProjectEnvKeyRecordRefusesPlantedKey(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	record := filepath.Join(t.TempDir(), "state", "project-env-key.fingerprint")
	keys := newPlantedKeys()
	s.SetEnvCodec(store.NewEnvCodec(keys, on).WithKeyRecord(record))
	putProject(t, s, "p1", map[string]string{"TOKEN": "s3cret"})
	if keys.recreated.Load() != 1 {
		t.Fatalf("first use recreated %d times, want 1 (never adopt an existing item)", keys.recreated.Load())
	}
	if info, err := os.Stat(record); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("record = %v, %v", info, err)
	}
	// Sealed under the daemon's key, not the planted one.
	attacker := store.NewEnvCodec(&fakeKeys{key: bytes.Repeat([]byte{0xAA}, 32)}, on)
	if _, err := attacker.Open(ctx, "p1", storedEnv(t, s, "p1")); err == nil {
		t.Fatal("env opened with the planted key")
	}

	// Restart: someone swaps the Keychain item for a known key (-T daemon).
	keys.current.Store(bytes.Repeat([]byte{0xAA}, 32))
	s.SetEnvCodec(store.NewEnvCodec(keys, on).WithKeyRecord(record))
	if _, err := getEnv(t, s, "p1"); !errors.Is(err, ports.ErrProjectEnvLocked) {
		t.Fatalf("swapped key: err = %v, want locked", err)
	}
	if keys.recreated.Load() != 1 {
		t.Fatal("recreated over a recorded key")
	}
	// The real key back: readable.
	keys.current.Store(bytes.Repeat([]byte{0x42}, 32))
	s.SetEnvCodec(store.NewEnvCodec(keys, on).WithKeyRecord(record))
	if env, err := getEnv(t, s, "p1"); err != nil || env["TOKEN"] != "s3cret" {
		t.Fatalf("real key = %v, %v", env, err)
	}
}

func TestProjectEnvNoRecordWithSealedRowsRefused(t *testing.T) {
	s := newTestStore(t)
	keys := newPlantedKeys()
	s.SetEnvCodec(store.NewEnvCodec(keys, on))
	putProject(t, s, "p1", map[string]string{"TOKEN": "s3cret"})
	// Record lost (or never written) while sealed env exists: refuse, never
	// recreate (that would make the env unrecoverable).
	s.SetEnvCodec(store.NewEnvCodec(keys, on).WithKeyRecord(filepath.Join(t.TempDir(), "rec")))
	if _, err := getEnv(t, s, "p1"); !errors.Is(err, ports.ErrProjectEnvLocked) {
		t.Fatalf("err = %v, want locked", err)
	}
	if keys.recreated.Load() != 0 {
		t.Fatal("recreated the key while sealed env exists")
	}
}

// A cancelled caller's failure is not cached: the next live call succeeds at
// once instead of waiting for the backoff window.
func TestProjectEnvCancelledKeyReadNotCached(t *testing.T) {
	ctx := context.Background()
	keys := newPlantedKeys()
	c := store.NewEnvCodec(keys, on)
	sealed, err := c.Seal(ctx, "p1", map[string]string{"A": "one"})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	fresh := store.NewEnvCodec(keys, on)
	if _, err := fresh.Open(cancelled, "p1", sealed); err == nil {
		t.Fatal("opened with a cancelled context")
	}
	if env, err := fresh.Open(ctx, "p1", sealed); err != nil || env["A"] != "one" {
		t.Fatalf("live call after cancel = %v, %v", env, err)
	}
}

// The Keychain item is lost (deleted, Keychain reset) after a record exists:
// with nothing sealed any more, the next save recreates and re-records the key
// instead of locking every project for good.
func TestProjectEnvLostKeyRecoversWhenNothingSealed(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	record := filepath.Join(t.TempDir(), "project-env-key.fingerprint")
	keys := newPlantedKeys()
	s.SetEnvCodec(store.NewEnvCodec(keys, on).WithKeyRecord(record))
	putProject(t, s, "p1", map[string]string{"TOKEN": "s3cret"})

	// Item deleted: Key() now yields some other key; restart.
	keys.current.Store(bytes.Repeat([]byte{0x99}, 32))
	s.SetEnvCodec(store.NewEnvCodec(keys, on).WithKeyRecord(record))
	if _, err := getEnv(t, s, "p1"); !errors.Is(err, ports.ErrProjectEnvLocked) {
		t.Fatalf("sealed project with lost key: err = %v, want locked", err)
	}
	// Recovery: clear the unrecoverable values (what removing the project does).
	if cleared, err := s.ClearProjectEnv(ctx, "p1"); err != nil || !cleared {
		t.Fatalf("clear = %v, %v", cleared, err)
	}
	if again, err := s.ClearProjectEnv(ctx, "p1"); err != nil || again {
		t.Fatalf("second clear = %v, %v; want nothing to clear", again, err)
	}
	before := keys.recreated.Load()
	putProject(t, s, "p2", map[string]string{"NEW": "value"})
	if keys.recreated.Load() != before+1 {
		t.Fatal("lost key not recreated")
	}
	if env, err := getEnv(t, s, "p2"); err != nil || env["NEW"] != "value" {
		t.Fatalf("p2 after recovery = %v, %v", env, err)
	}
	// And the new record holds across a restart.
	s.SetEnvCodec(store.NewEnvCodec(keys, on).WithKeyRecord(record))
	if env, err := getEnv(t, s, "p2"); err != nil || env["NEW"] != "value" {
		t.Fatalf("p2 after restart = %v, %v", env, err)
	}
}

// Sealed values in an archived project do not hold recovery hostage after the
// key is lost: A and B sealed, B archived, key lost, A removed (cleared), then
// a save for another project works and B can be read for re-adding.
func TestProjectEnvLostKeyArchivedSealedDoesNotBlock(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	record := filepath.Join(t.TempDir(), "project-env-key.fingerprint")
	keys := newPlantedKeys()
	s.SetEnvCodec(store.NewEnvCodec(keys, on).WithKeyRecord(record))
	putProject(t, s, "a", map[string]string{"A": "one"})
	putProject(t, s, "b", map[string]string{"B": "two"})
	if _, err := s.ArchiveProject(ctx, "b", time.Now()); err != nil {
		t.Fatal(err)
	}
	keys.current.Store(bytes.Repeat([]byte{0x99}, 32)) // key item lost
	s.SetEnvCodec(store.NewEnvCodec(keys, on).WithKeyRecord(record))
	if _, err := s.ClearProjectEnv(ctx, "a"); err != nil { // Remove of A
		t.Fatal(err)
	}
	putProject(t, s, "c", map[string]string{"C": "three"})
	if env, err := getEnv(t, s, "c"); err != nil || env["C"] != "three" {
		t.Fatalf("c after recovery = %v, %v", env, err)
	}
	// The archived project reads (env dropped) so it can be re-added.
	r, ok, err := s.GetProject(ctx, "b")
	if err != nil || !ok || len(r.Config.Env) != 0 {
		t.Fatalf("archived b = %+v %v %v", r.Config.Env, ok, err)
	}
	putProject(t, s, "b", map[string]string{"B": "re-entered"})
	if env, err := getEnv(t, s, "b"); err != nil || env["B"] != "re-entered" {
		t.Fatalf("re-added b = %v, %v", env, err)
	}
}
