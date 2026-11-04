package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// fakeKeys is the SecretKeyStore in unit tests: no Keychain is touched.
type fakeKeys struct {
	key   []byte
	down  atomic.Bool
	calls atomic.Int32
}

func newFakeKeys() *fakeKeys { return &fakeKeys{key: bytes.Repeat([]byte{7}, 32)} }

func (f *fakeKeys) Key(context.Context) ([]byte, error) {
	f.calls.Add(1)
	if f.down.Load() {
		return nil, ports.ErrSecretKeyUnavailable
	}
	return f.key, nil
}

func on() bool  { return true }
func off() bool { return false }

func storedEnv(t *testing.T, s *sqlite.Store, id string) map[string]string {
	t.Helper()
	raw, err := s.RawProjectConfig(context.Background(), id)
	if err != nil {
		t.Fatalf("raw config: %v", err)
	}
	var cfg domain.ProjectConfig
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			t.Fatalf("decode raw: %v", err)
		}
	}
	return cfg.Env
}

func putProject(t *testing.T, s *sqlite.Store, id string, env map[string]string) {
	t.Helper()
	if err := s.UpsertProject(context.Background(), domain.ProjectRecord{
		ID: id, Path: "/tmp/" + id, RegisteredAt: time.Now().UTC().Truncate(time.Second),
		Config: domain.ProjectConfig{Env: env},
	}); err != nil {
		t.Fatalf("upsert %s: %v", id, err)
	}
}

func getEnv(t *testing.T, s *sqlite.Store, id string) (map[string]string, error) {
	t.Helper()
	r, ok, err := s.GetProject(context.Background(), id)
	if err == nil && !ok {
		t.Fatalf("project %s missing", id)
	}
	return r.Config.Env, err
}

func TestEnvCodecRoundTripAndAAD(t *testing.T) {
	ctx := context.Background()
	c := store.NewEnvCodec(newFakeKeys(), on)
	sealed, err := c.Seal(ctx, "p1", map[string]string{"TOKEN": "s3cret", "EMPTY": ""})
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range sealed {
		if !strings.HasPrefix(v, "tpenc:v1:") || strings.Contains(v, "s3cret") {
			t.Fatalf("%s not sealed: %q", k, v)
		}
	}
	got, err := c.Open(ctx, "p1", sealed)
	if err != nil || got["TOKEN"] != "s3cret" || got["EMPTY"] != "" {
		t.Fatalf("round trip = %v, %v", got, err)
	}
	if _, err := c.Open(ctx, "p2", sealed); !errors.Is(err, ports.ErrProjectEnvLocked) {
		t.Fatalf("other project opened: %v", err)
	}
	moved := map[string]string{"OTHER": sealed["TOKEN"]}
	if _, err := c.Open(ctx, "p1", moved); err == nil {
		t.Fatal("value under another name opened")
	}
}

func TestProjectEnvSealedOnWriteUnderProfile(t *testing.T) {
	s := newTestStore(t)
	s.SetEnvCodec(store.NewEnvCodec(newFakeKeys(), on))
	putProject(t, s, "p1", map[string]string{"TOKEN": "s3cret"})
	if raw, _ := s.RawProjectConfig(context.Background(), "p1"); strings.Contains(raw, "s3cret") {
		t.Fatalf("plaintext at rest: %s", raw)
	}
	env, err := getEnv(t, s, "p1")
	if err != nil || env["TOKEN"] != "s3cret" {
		t.Fatalf("read back = %v, %v", env, err)
	}
	ok, err := s.UpdateProjectSettings(context.Background(), "p1", "P1", domain.ProjectConfig{Env: map[string]string{"TOKEN": "n3w"}})
	if err != nil || !ok {
		t.Fatalf("update: %v", err)
	}
	if raw, _ := s.RawProjectConfig(context.Background(), "p1"); strings.Contains(raw, "n3w") {
		t.Fatalf("plaintext after update: %s", raw)
	}
	list, err := s.ListProjects(context.Background())
	if err != nil || len(list) != 1 || list[0].Config.Env["TOKEN"] != "n3w" {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

func TestProjectEnvProfileOffWritesPlaintext(t *testing.T) {
	s := newTestStore(t)
	keys := newFakeKeys()
	s.SetEnvCodec(store.NewEnvCodec(keys, off))
	putProject(t, s, "p1", map[string]string{"TOKEN": "plain"})
	if storedEnv(t, s, "p1")["TOKEN"] != "plain" {
		t.Fatalf("stored = %v", storedEnv(t, s, "p1"))
	}
	if keys.calls.Load() != 0 {
		t.Fatalf("key read %d times with profile off", keys.calls.Load())
	}
}

func TestProjectEnvMixedReadAndProfileRemoval(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	keys := newFakeKeys()
	putProject(t, s, "p1", map[string]string{"A": "plain"})
	sealed, err := store.NewEnvCodec(keys, on).Seal(ctx, "p1", map[string]string{"B": "hidden"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(domain.ProjectConfig{Env: map[string]string{"A": "plain", "B": sealed["B"]}})
	if err := s.SetRawProjectConfig(ctx, "p1", string(raw)); err != nil {
		t.Fatal(err)
	}
	// Profile removed: sealed values stay readable.
	s.SetEnvCodec(store.NewEnvCodec(keys, off))
	env, err := getEnv(t, s, "p1")
	if err != nil || env["A"] != "plain" || env["B"] != "hidden" {
		t.Fatalf("mixed read = %v, %v", env, err)
	}
}

func TestProjectEnvMigrationIdempotentPerProject(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putProject(t, s, "p1", map[string]string{"A": "one"})
	putProject(t, s, "p2", map[string]string{"B": "two"})
	putProject(t, s, "p3", nil)
	if _, err := s.ArchiveProject(ctx, "p2", time.Now()); err != nil {
		t.Fatal(err)
	}
	keys := newFakeKeys()
	s.SetEnvCodec(store.NewEnvCodec(keys, on))
	n, err := s.EncryptProjectEnvAtRest(ctx)
	if err != nil || n != 2 {
		t.Fatalf("first migration sealed %d, %v; want 2 (archived included)", n, err)
	}
	for id, plain := range map[string]string{"p1": "one", "p2": "two"} {
		for _, v := range storedEnv(t, s, id) {
			if !strings.HasPrefix(v, "tpenc:v1:") || strings.Contains(v, plain) {
				t.Fatalf("%s not sealed: %q", id, v)
			}
		}
	}
	before, _ := s.RawProjectConfig(ctx, "p1")
	n, err = s.EncryptProjectEnvAtRest(ctx)
	if err != nil || n != 0 {
		t.Fatalf("second migration sealed %d, %v; want 0", n, err)
	}
	if after, _ := s.RawProjectConfig(ctx, "p1"); after != before {
		t.Fatal("idempotent migration rewrote a sealed row")
	}
	if env, err := getEnv(t, s, "p1"); err != nil || env["A"] != "one" {
		t.Fatalf("read after migration = %v, %v", env, err)
	}
}

func TestProjectEnvMigrationKeyDownLeavesRows(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putProject(t, s, "p1", map[string]string{"A": "one"})
	keys := newFakeKeys()
	keys.down.Store(true)
	s.SetEnvCodec(store.NewEnvCodec(keys, on))
	before, _ := s.RawProjectConfig(ctx, "p1")
	if _, err := s.EncryptProjectEnvAtRest(ctx); !errors.Is(err, ports.ErrProjectEnvLocked) {
		t.Fatalf("migration err = %v, want locked", err)
	}
	if after, _ := s.RawProjectConfig(ctx, "p1"); after != before {
		t.Fatal("failed migration changed the row")
	}
}

func TestProjectEnvKeyUnavailableFailsClosed(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	keys := newFakeKeys()
	s.SetEnvCodec(store.NewEnvCodec(keys, on))
	putProject(t, s, "p1", map[string]string{"TOKEN": "s3cret"})
	before, _ := s.RawProjectConfig(ctx, "p1")

	// Daemon restarted with the Keychain locked: a fresh codec, no cached key.
	keys.down.Store(true)
	for name, sealing := range map[string]func() bool{"profile on": on, "profile off": off} {
		s.SetEnvCodec(store.NewEnvCodec(keys, sealing))
		if _, err := getEnv(t, s, "p1"); !errors.Is(err, ports.ErrProjectEnvLocked) {
			t.Fatalf("%s: get err = %v, want locked", name, err)
		}
		if _, ok, err := s.FindProjectByPath(ctx, "/tmp/p1"); ok || !errors.Is(err, ports.ErrProjectEnvLocked) {
			t.Fatalf("%s: find err = %v", name, err)
		}
		list, err := s.ListProjects(ctx)
		if err != nil || len(list) != 1 || list[0].Config.Env != nil {
			t.Fatalf("%s: list must keep the row without env: %+v, %v", name, list, err)
		}
		// Writes (e.g. saving settings from the list row, env dropped) refuse.
		if _, err := s.UpdateProjectSettings(ctx, "p1", "P1", domain.ProjectConfig{}); !errors.Is(err, ports.ErrProjectEnvLocked) {
			t.Fatalf("%s: update err = %v, want locked", name, err)
		}
		if err := s.UpsertProject(ctx, domain.ProjectRecord{ID: "p1", Path: "/tmp/p1", Config: domain.ProjectConfig{Env: map[string]string{"TOKEN": "plain"}}}); !errors.Is(err, ports.ErrProjectEnvLocked) {
			t.Fatalf("%s: upsert err = %v, want locked", name, err)
		}
		if after, _ := s.RawProjectConfig(ctx, "p1"); after != before {
			t.Fatalf("%s: locked project rewritten: %s", name, after)
		}
	}
	// No codec at all also fails closed on a sealed row.
	s.SetEnvCodec(nil)
	if _, err := getEnv(t, s, "p1"); !errors.Is(err, ports.ErrProjectEnvLocked) {
		t.Fatalf("no codec: get err = %v", err)
	}
	if _, err := s.UpdateProjectSettings(ctx, "p1", "P1", domain.ProjectConfig{}); !errors.Is(err, ports.ErrProjectEnvLocked) {
		t.Fatalf("no codec: update err = %v", err)
	}
	// Key back: readable again.
	keys.down.Store(false)
	s.SetEnvCodec(store.NewEnvCodec(keys, on))
	if env, err := getEnv(t, s, "p1"); err != nil || env["TOKEN"] != "s3cret" {
		t.Fatalf("after unlock = %v, %v", env, err)
	}
}

func TestSetProjectPermissionsKeepsSealedEnv(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	s.SetEnvCodec(store.NewEnvCodec(newFakeKeys(), on))
	putProject(t, s, "p1", map[string]string{"TOKEN": "s3cret"})
	row, ok, err := s.SetProjectPermissions(ctx, "p1", domain.PermissionModeDefault)
	if err != nil || !ok || row.Config.Env["TOKEN"] != "s3cret" {
		t.Fatalf("set permissions = %+v %v %v", row.Config.Env, ok, err)
	}
	if raw, _ := s.RawProjectConfig(ctx, "p1"); strings.Contains(raw, "s3cret") {
		t.Fatalf("plaintext after set permissions: %s", raw)
	}
}

// A failed key read (Keychain denied or locked) is reused for a while, so
// repeated list calls from pollers do not re-prompt per sealed project.
func TestProjectEnvKeyFailureBackoff(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	keys := newFakeKeys()
	s.SetEnvCodec(store.NewEnvCodec(keys, on))
	putProject(t, s, "p1", map[string]string{"A": "one"})
	putProject(t, s, "p2", map[string]string{"B": "two"})

	keys.down.Store(true)
	now := time.Unix(1000, 0)
	codec := store.NewEnvCodec(keys, on)
	codec.SetEnvCodecClock(func() time.Time { return now })
	s.SetEnvCodec(codec)
	keys.calls.Store(0)
	for range 5 {
		if _, err := s.ListProjects(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := getEnv(t, s, "p1"); !errors.Is(err, ports.ErrProjectEnvLocked) {
			t.Fatalf("get err = %v", err)
		}
	}
	if n := keys.calls.Load(); n != 1 {
		t.Fatalf("Key() called %d times within the backoff, want 1", n)
	}
	keys.down.Store(false)
	now = now.Add(2 * time.Minute)
	if env, err := getEnv(t, s, "p1"); err != nil || env["A"] != "one" {
		t.Fatalf("after backoff = %v, %v", env, err)
	}
	if n := keys.calls.Load(); n != 2 {
		t.Fatalf("Key() calls = %d, want 2 (one retry after the window)", n)
	}
}

func TestProjectEnvLegacyPlaintextFailsClosedWhenKeyDown(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putProject(t, s, "p1", map[string]string{"TOKEN": "legacy"})
	keys := newFakeKeys()
	keys.down.Store(true)
	now := time.Unix(1000, 0)
	codec := store.NewEnvCodec(keys, on)
	codec.SetEnvCodecClock(func() time.Time { return now })
	s.SetEnvCodec(codec)
	if _, err := getEnv(t, s, "p1"); !errors.Is(err, ports.ErrProjectEnvLocked) {
		t.Fatalf("read err = %v, want locked", err)
	}
	keys.down.Store(false)
	now = now.Add(2 * time.Minute) // past the cached key failure
	if n, err := s.EncryptProjectEnvAtRest(ctx); err != nil || n != 1 {
		t.Fatalf("migration = %d, %v", n, err)
	}
	for _, v := range storedEnv(t, s, "p1") {
		if !strings.HasPrefix(v, "tpenc:v1:") || strings.Contains(v, "legacy") {
			t.Fatalf("not sealed: %q", v)
		}
	}
	if env, err := getEnv(t, s, "p1"); err != nil || env["TOKEN"] != "legacy" {
		t.Fatalf("read after migration = %v, %v", env, err)
	}
}

func TestProjectEnvGetWithoutEnvKeepsStoredValues(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putProject(t, s, "p1", map[string]string{"TOKEN": "legacy"})
	keys := newFakeKeys()
	keys.down.Store(true)
	s.SetEnvCodec(store.NewEnvCodec(keys, on))
	r, ok, err := s.GetProjectWithoutEnv(ctx, "p1")
	if err != nil || !ok || len(r.Config.Env) != 0 {
		t.Fatalf("got %+v, %v, %v", r, ok, err)
	}
	if got := storedEnv(t, s, "p1"); got["TOKEN"] != "legacy" {
		t.Fatalf("stored env changed: %v", got)
	}
}
