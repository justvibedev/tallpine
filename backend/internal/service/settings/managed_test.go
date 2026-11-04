package settings

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/managedcfg"
)

func TestManagedRetentionWinsAndRefusesChanges(t *testing.T) {
	ctx := context.Background()
	store := &storageCompactionStore{snapshot: Snapshot{StorageCompaction: true, ProviderEventRetentionDays: 14}}
	svc := New(store, nil, Offering{}, nil)
	svc.SetManaged(managedcfg.New(func() map[string]json.RawMessage {
		return map[string]json.RawMessage{"app.providerEventRetentionDays": json.RawMessage("7")}
	}))
	if got := svc.StoragePolicy(ctx).Retention.Hours(); got != 7*24 {
		t.Fatalf("retention = %vh, want the managed 7 days", got)
	}
	snap, err := svc.Get(ctx)
	if err != nil || snap.ProviderEventRetentionDays != 7 {
		t.Fatalf("Get = %+v err=%v, want 7", snap, err)
	}
	days := 30
	_, err = svc.SetStorageCompaction(ctx, nil, &days)
	var api *apierr.Error
	if !errors.As(err, &api) || api.Code != managedcfg.CodeLocked {
		t.Fatalf("err = %v, want SETTING_LOCKED", err)
	}
	same, off := 7, false
	if _, err := svc.SetStorageCompaction(ctx, &off, &same); err != nil {
		t.Fatalf("writing the managed value back with another field: %v", err)
	}
}

func TestManagedRetentionEchoIsNotSavedAndFileChangesApplyWithoutARead(t *testing.T) {
	ctx := context.Background()
	store := &storageCompactionStore{snapshot: Snapshot{StorageCompaction: true, ProviderEventRetentionDays: 14}}
	svc := New(store, nil, Offering{}, nil)
	var managed json.RawMessage
	svc.SetManaged(managedcfg.New(func() map[string]json.RawMessage {
		if managed == nil {
			return nil
		}
		return map[string]json.RawMessage{"app.providerEventRetentionDays": managed}
	}))
	if got := svc.StoragePolicy(ctx).Retention.Hours(); got != 14*24 {
		t.Fatalf("retention = %vh before the profile, want 14d", got)
	}
	managed = json.RawMessage("7") // the profile arrives after the cache was filled
	if got := svc.StoragePolicy(ctx).Retention.Hours(); got != 7*24 {
		t.Fatalf("retention = %vh after the profile arrived, want 7d without a settings read", got)
	}
	seven, off := 7, false
	if _, err := svc.SetStorageCompaction(ctx, &off, &seven); err != nil {
		t.Fatal(err)
	}
	if store.snapshot.ProviderEventRetentionDays != 14 {
		t.Fatalf("stored retention = %d, want the user's 14 (the managed echo must not be saved)", store.snapshot.ProviderEventRetentionDays)
	}
	managed = nil
	if got := svc.StoragePolicy(ctx).Retention.Hours(); got != 14*24 {
		t.Fatalf("retention = %vh after the profile is removed, want 14d", got)
	}
}
