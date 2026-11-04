package settings

import (
	"context"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
)

type hibernationSettingsStore struct {
	err    error
	policy *domain.ChatHibernationPolicy
	writes int
}

func (s *hibernationSettingsStore) GetAppSettings(context.Context) (Snapshot, error) {
	policy := domain.DefaultChatHibernationPolicy()
	if s.policy != nil {
		policy = *s.policy
	}
	return Snapshot{DefaultSessionMode: domain.SessionModeChat, ChatHibernation: policy}, s.err
}
func (*hibernationSettingsStore) SetDefaultSessionMode(context.Context, domain.SessionMode, time.Time) error {
	return nil
}
func (*hibernationSettingsStore) SetCloudOffering(context.Context, bool, time.Time) error { return nil }
func (*hibernationSettingsStore) SetVersionRefresh(context.Context, bool, time.Time) error {
	return nil
}
func (*hibernationSettingsStore) SetStorageCompaction(context.Context, bool, int, time.Time) error {
	return nil
}
func (s *hibernationSettingsStore) SetChatHibernation(_ context.Context, policy domain.ChatHibernationPolicy, _ time.Time) error {
	if s.err != nil {
		return s.err
	}
	s.writes++
	s.policy = &policy
	return nil
}

func TestChatHibernationIsDurableAndOnByDefault(t *testing.T) {
	ctx := context.Background()
	store := &hibernationSettingsStore{}
	first := New(store, nil, Offering{}, nil)
	if !first.ChatHibernationEnabled() {
		t.Fatal("fresh install did not enable chat hibernation")
	}
	if got := first.ChatHibernation(ctx); got.WorkerIdle != 20*time.Minute || got.OrchestratorIdle != time.Hour {
		t.Fatalf("default thresholds = %+v", got)
	}
	off := false
	snapshot, err := first.SetChatHibernation(ctx, ChatHibernationUpdate{Enabled: &off})
	if err != nil || snapshot.ChatHibernation.Enabled || first.ChatHibernationEnabled() {
		t.Fatalf("disable chat hibernation: snapshot=%+v err=%v", snapshot, err)
	}
	second := New(store, nil, Offering{}, nil)
	if second.ChatHibernationEnabled() {
		t.Fatal("restarted daemon forgot that chat hibernation is off")
	}
}

func TestChatHibernationThresholdsPatchIndependently(t *testing.T) {
	ctx := context.Background()
	store := &hibernationSettingsStore{}
	svc := New(store, nil, Offering{}, nil)
	worker := 5 * time.Minute
	if _, err := svc.SetChatHibernation(ctx, ChatHibernationUpdate{WorkerIdle: &worker}); err != nil {
		t.Fatal(err)
	}
	got := svc.ChatHibernation(ctx)
	if !got.Enabled || got.WorkerIdle != worker || got.OrchestratorIdle != time.Hour {
		t.Fatalf("policy after worker patch = %+v", got)
	}
	if got.IdleFor(domain.KindOrchestrator) != time.Hour || got.IdleFor(domain.KindWorker) != worker {
		t.Fatalf("IdleFor = %s/%s", got.IdleFor(domain.KindOrchestrator), got.IdleFor(domain.KindWorker))
	}
	for _, bad := range []time.Duration{0, 30 * time.Second, 8 * 24 * time.Hour, 90*time.Second + time.Millisecond} {
		if _, err := svc.SetChatHibernation(ctx, ChatHibernationUpdate{OrchestratorIdle: &bad}); !errors.Is(err, ErrInvalidChatHibernation) {
			t.Fatalf("threshold %s accepted: %v", bad, err)
		}
	}
	if store.writes != 1 {
		t.Fatalf("writes = %d, want only the valid patch", store.writes)
	}
}

func TestDisablingChatHibernationSurvivesSettingsReadFailure(t *testing.T) {
	ctx := context.Background()
	store := &hibernationSettingsStore{}
	svc := New(store, nil, Offering{}, nil)
	if !svc.ChatHibernationEnabled() {
		t.Fatal("expected default on")
	}
	store.err = errors.New("database unavailable")
	off := false
	if _, err := svc.SetChatHibernation(ctx, ChatHibernationUpdate{Enabled: &off}); err == nil || svc.ChatHibernationEnabled() {
		t.Fatalf("disable after read error = %v, enabled=%v", err, svc.ChatHibernationEnabled())
	}
}

func TestUnreadableSettingsNeverHibernate(t *testing.T) {
	svc := New(&hibernationSettingsStore{err: errors.New("database unavailable")}, nil, Offering{}, nil)
	if svc.ChatHibernationEnabled() {
		t.Fatal("unreadable settings enabled hibernation")
	}
}

func TestOfferingFromConfigCarriesTrackerIntake(t *testing.T) {
	for _, on := range []bool{true, false} {
		got := OfferingFromConfig(config.Config{TrackerIntake: on})
		if got.TrackerIntakeEnabled != on {
			t.Errorf("OfferingFromConfig(TrackerIntake=%v).TrackerIntakeEnabled = %v, want %v", on, got.TrackerIntakeEnabled, on)
		}
	}
}

func (*hibernationSettingsStore) SetHarnessDefault(context.Context, domain.AgentHarness, domain.HarnessDefault, time.Time) error {
	return nil
}

type versionRefreshStore struct {
	hibernationSettingsStore
	refresh bool
	reads   int
}

func (s *versionRefreshStore) GetAppSettings(context.Context) (Snapshot, error) {
	s.reads++
	return Snapshot{VersionRefresh: s.refresh}, nil
}

func (s *versionRefreshStore) SetVersionRefresh(_ context.Context, enabled bool, _ time.Time) error {
	s.refresh = enabled
	return nil
}

func TestVersionRefreshToggleIsReadOnceAndCached(t *testing.T) {
	ctx := context.Background()
	store := &versionRefreshStore{refresh: true}
	svc := New(store, nil, Offering{}, nil)
	first := svc.VersionRefreshEnabled(ctx)
	if second := svc.VersionRefreshEnabled(ctx); !first || !second || store.reads != 1 {
		t.Fatalf("enabled reads = %d, want one cached read", store.reads)
	}
	snapshot, err := svc.SetVersionRefresh(ctx, false)
	if err != nil || snapshot.VersionRefresh || svc.VersionRefreshEnabled(ctx) {
		t.Fatalf("turn off: snapshot=%+v err=%v enabled=%t", snapshot, err, svc.VersionRefreshEnabled(ctx))
	}
}

type storageCompactionStore struct {
	hibernationSettingsStore
	snapshot Snapshot
	reads    int
	writes   int
}

func (s *storageCompactionStore) GetAppSettings(context.Context) (Snapshot, error) {
	s.reads++
	return s.snapshot, nil
}

func (s *storageCompactionStore) SetStorageCompaction(_ context.Context, enabled bool, days int, _ time.Time) error {
	s.writes++
	s.snapshot.StorageCompaction, s.snapshot.ProviderEventRetentionDays = enabled, days
	return nil
}

func TestStorageCompactionPolicyIsCachedAndPartiallyUpdatable(t *testing.T) {
	ctx := context.Background()
	store := &storageCompactionStore{snapshot: Snapshot{StorageCompaction: true, ProviderEventRetentionDays: 14}}
	svc := New(store, nil, Offering{}, nil)
	first := svc.StoragePolicy(ctx)
	second := svc.StoragePolicy(ctx)
	if !first.Compaction || first.Retention != 14*24*time.Hour || second != first || store.reads != 1 {
		t.Fatalf("policy = %+v then %+v after %d reads, want one cached read of on/14d", first, second, store.reads)
	}

	off := false
	snapshot, err := svc.SetStorageCompaction(ctx, &off, nil)
	if err != nil || snapshot.StorageCompaction || snapshot.ProviderEventRetentionDays != 14 || svc.StoragePolicy(ctx).Compaction {
		t.Fatalf("turn off: snapshot=%+v err=%v policy=%+v", snapshot, err, svc.StoragePolicy(ctx))
	}
	days := 30
	snapshot, err = svc.SetStorageCompaction(ctx, nil, &days)
	if err != nil || snapshot.StorageCompaction || snapshot.ProviderEventRetentionDays != 30 ||
		svc.StoragePolicy(ctx).Retention != 30*24*time.Hour {
		t.Fatalf("set retention: snapshot=%+v err=%v policy=%+v", snapshot, err, svc.StoragePolicy(ctx))
	}

	writes := store.writes
	for _, bad := range []int{0, -1, MaxProviderEventRetentionDays + 1} {
		if _, err := svc.SetStorageCompaction(ctx, nil, &bad); !errors.Is(err, ErrInvalidStorageCompaction) {
			t.Errorf("retention %d: err = %v, want ErrInvalidStorageCompaction", bad, err)
		}
	}
	if _, err := svc.SetStorageCompaction(ctx, nil, nil); !errors.Is(err, ErrInvalidStorageCompaction) {
		t.Errorf("empty update: err = %v, want ErrInvalidStorageCompaction", err)
	}
	if store.writes != writes {
		t.Fatalf("invalid input reached the store: writes %d -> %d", writes, store.writes)
	}
}

func TestStorageCompactionPolicyIsOffWhileSettingsAreUnreadable(t *testing.T) {
	svc := New(&hibernationSettingsStore{err: errors.New("database unavailable")}, nil, Offering{}, nil)
	if policy := svc.StoragePolicy(context.Background()); policy.Compaction {
		t.Fatalf("unreadable settings enabled compaction: %+v", policy)
	}
}
