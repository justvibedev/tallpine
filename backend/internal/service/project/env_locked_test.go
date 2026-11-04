package project_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/service/project"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
	sqlitestore "tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

type switchKeys struct{ down bool }

func (k *switchKeys) Key(context.Context) ([]byte, error) {
	if k.down {
		return nil, ports.ErrSecretKeyUnavailable
	}
	return bytes.Repeat([]byte{3}, 32), nil
}

// A project whose sealed env cannot be opened reaches the API as 409
// PROJECT_ENV_LOCKED, not a generic 500, when opened; removing it clears the
// encrypted values and succeeds.
func TestProjectEnvLockedMapsToConflict(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	keys := &switchKeys{}
	hardened := func() bool { return true }
	store.SetEnvCodec(sqlitestore.NewEnvCodec(keys, hardened))
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "p", Path: t.TempDir(), DisplayName: "p", RegisteredAt: time.Now(),
		Config: domain.ProjectConfig{Env: map[string]string{"TOKEN": "s3cret"}}}); err != nil {
		t.Fatal(err)
	}
	keys.down = true
	store.SetEnvCodec(sqlitestore.NewEnvCodec(keys, hardened))
	svc := project.New(store)

	_, getErr := svc.Get(ctx, "p")
	var e *apierr.Error
	if !errors.As(getErr, &e) || e.Kind != apierr.KindConflict || e.Code != ports.CodeProjectEnvLocked {
		t.Fatalf("get: err = %#v, want 409 %s", getErr, ports.CodeProjectEnvLocked)
	}
	// Removing is the way out: it clears the encrypted values and archives.
	if _, err := svc.Remove(ctx, "p"); err != nil {
		t.Fatalf("remove locked project: %v", err)
	}
	row, ok, err := store.GetProject(ctx, "p")
	if err != nil || !ok || row.ArchivedAt.IsZero() || len(row.Config.Env) != 0 {
		t.Fatalf("after remove = %+v ok=%v err=%v; want archived, env cleared", row, ok, err)
	}
}

func TestProjectEnvLegacyPlaintextLockedRemovable(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	// Legacy row written with sealing off, then the hardened profile on, key down.
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "p", Path: t.TempDir(), DisplayName: "p", RegisteredAt: time.Now(),
		Config: domain.ProjectConfig{Env: map[string]string{"TOKEN": "legacy"}}}); err != nil {
		t.Fatal(err)
	}
	keys := &switchKeys{down: true}
	store.SetEnvCodec(sqlitestore.NewEnvCodec(keys, func() bool { return true }))
	svc := project.New(store)

	var e *apierr.Error
	if _, getErr := svc.Get(ctx, "p"); !errors.As(getErr, &e) || e.Code != ports.CodeProjectEnvLocked {
		t.Fatalf("get: err = %#v, want %s", getErr, ports.CodeProjectEnvLocked)
	}
	if _, err := svc.Remove(ctx, "p"); err != nil {
		t.Fatalf("remove locked legacy project: %v", err)
	}
	// The values were kept: with the key back, the archived row still opens.
	keys.down = false
	store.SetEnvCodec(sqlitestore.NewEnvCodec(keys, func() bool { return true }))
	row, ok, err := store.GetProject(ctx, "p")
	if err != nil || !ok || row.ArchivedAt.IsZero() || row.Config.Env["TOKEN"] != "legacy" {
		t.Fatalf("after remove = %+v ok=%v err=%v; want archived, TOKEN kept", row, ok, err)
	}
}
