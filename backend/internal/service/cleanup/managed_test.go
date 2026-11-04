package cleanup

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/managedcfg"
)

func lockedAt(values map[string]string) *managedcfg.Locks {
	return managedcfg.New(func() map[string]json.RawMessage {
		out := map[string]json.RawMessage{}
		for k, v := range values {
			if v == "" {
				out[k] = nil
			} else {
				out[k] = json.RawMessage(v)
			}
		}
		return out
	})
}

func TestManagedCleanupFieldWinsOverStoredAndRefusesChanges(t *testing.T) {
	ctx := context.Background()
	store := &fakeStore{policy: `{"automatic":true}`}
	svc, _ := newTestService(store)
	svc.SetManaged(lockedAt(map[string]string{"app.cleanup.automatic": "false"}))

	policy, _, err := svc.Policy(ctx)
	if err != nil || policy.Automatic {
		t.Fatalf("policy = %+v err=%v, want the managed automatic=false", policy, err)
	}
	_, err = svc.UpdatePolicy(ctx, []byte(`{"automatic":true}`))
	var api *apierr.Error
	if !errors.As(err, &api) || api.Code != managedcfg.CodeLocked {
		t.Fatalf("UpdatePolicy(automatic) err = %v, want SETTING_LOCKED", err)
	}
	if store.policy != `{"automatic":true}` {
		t.Errorf("a refused write reached the store: %s", store.policy)
	}
	// An unlocked field still changes, and writing the locked field's current value is not a change.
	if _, err := svc.UpdatePolicy(ctx, []byte(`{"automatic":false,"worktrees":{"retentionDays":9}}`)); err != nil {
		t.Fatalf("unlocked field with the locked field unchanged: %v", err)
	}
}

func TestUnreadableManagedFileLocksCleanupAtStoredValue(t *testing.T) {
	store := &fakeStore{policy: `{"automatic":true}`}
	svc, _ := newTestService(store)
	svc.SetManaged(lockedAt(map[string]string{"app.cleanup.automatic": ""}))
	if p, _, _ := svc.Policy(context.Background()); !p.Automatic {
		t.Error("a locked key with no usable managed value must keep its stored value")
	}
	if _, err := svc.UpdatePolicy(context.Background(), []byte(`{"automatic":false}`)); err == nil {
		t.Error("write to a key locked by an unreadable file was accepted")
	}
}

func TestManagedCleanupIsNeverPersistedAndReplacesStoredRule(t *testing.T) {
	ctx := context.Background()
	store := &fakeStore{policy: `{"worktrees":{"retentionDays":9}}`}
	svc, _ := newTestService(store)
	svc.SetManaged(lockedAt(map[string]string{"app.cleanup.worktrees": `{"enabled":false}`}))
	got, _, _ := svc.Policy(ctx)
	if got.Worktrees.Enabled || got.Worktrees.RetentionDays != DefaultPolicy().Worktrees.RetentionDays {
		t.Fatalf("effective worktrees = %+v, want the managed rule over defaults, not the stored 9 days", got.Worktrees)
	}
	if _, err := svc.UpdatePolicy(ctx, []byte(`{"automatic":false,"worktrees":{"enabled":false}}`)); err != nil {
		t.Fatal(err) // echoing the managed value of a locked field is not a change
	}
	svc.SetManaged(nil)
	back, _, _ := svc.Policy(ctx)
	if back.Automatic || back.Worktrees.RetentionDays != 9 || !back.Worktrees.Enabled {
		t.Fatalf("after the profile is removed: %+v, want the user's 9-day worktrees rule and the unrelated edit kept", back)
	}
}
