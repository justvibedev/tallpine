// Package settings owns Tallpine's daemon-side user preferences.
//
// It exists so every spawn surface — desktop, mobile, `tallpine spawn`, headless —
// resolves one value. A renderer-held preference would look correct in Settings
// while disagreeing with the CLI, which is worse than having no control.
package settings

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/managedcfg"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Store is the durable preference surface.
type Store interface {
	GetAppSettings(ctx context.Context) (Snapshot, error)
	SetDefaultSessionMode(ctx context.Context, mode domain.SessionMode, now time.Time) error
	SetHarnessDefault(ctx context.Context, agent domain.AgentHarness, value domain.HarnessDefault, now time.Time) error
	SetVersionRefresh(ctx context.Context, enabled bool, now time.Time) error
	SetStorageCompaction(ctx context.Context, enabled bool, retentionDays int, now time.Time) error
	SetChatHibernation(ctx context.Context, policy domain.ChatHibernationPolicy, now time.Time) error
}

// Snapshot is the current preference set.
type Snapshot struct {
	HarnessDefaults    map[string]domain.HarnessDefault
	DefaultSessionMode domain.SessionMode
	// ChatHibernation releases idle Chat agents' processes (Settings,
	// General). On by default.
	ChatHibernation domain.ChatHibernationPolicy
	// VersionRefresh moves idle open sessions to a harness release installed
	// while they ran (Settings, General). On by default.
	VersionRefresh bool
	// DynamicOrchestratorContext lets an orchestrator raise its compaction
	// window while it researches (B413; Settings, General). On by default.
	DynamicOrchestratorContext bool
	// WorkflowDefaults are the app-wide workflow values projects inherit (B525;
	// Settings, Reviews).
	WorkflowDefaults domain.WorkflowDefaults
	// StorageCompaction coalesces repeated activity writes and compacts and
	// expires the raw provider-event archive (Settings, General). On by default.
	StorageCompaction bool
	// ProviderEventRetentionDays is how long provider events of a terminated
	// session are kept while StorageCompaction is on.
	ProviderEventRetentionDays int
	// PeerCheck is the owner's daemon peer check switch (B631): "default"
	// follows the build, "off" stops refusing callers Tallpine did not start.
	// Written only through the desktop app (daemon wiring), never here.
	PeerCheck string
	UpdatedAt time.Time
}

// Peer check switch values (app_settings.peer_check).
const (
	PeerCheckDefault = "default"
	PeerCheckOff     = "off"
)

// PeerCheckOrDefault reads an empty switch (a store without the column) as
// the default.
func PeerCheckOrDefault(value string) string {
	if value == PeerCheckOff {
		return PeerCheckOff
	}
	return PeerCheckDefault
}

// Bounds and default of the provider-event retention window, mirroring the
// app_settings CHECK constraint.
const (
	DefaultProviderEventRetentionDays = 14
	MinProviderEventRetentionDays     = 1
	MaxProviderEventRetentionDays     = 3650
)

// ErrInvalidStorageCompaction marks invalid storage-compaction input at the API
// boundary.
var ErrInvalidStorageCompaction = errors.New("invalid storage compaction setting")

// StoragePolicy is the storage-compaction setting as the write path and the
// archive janitor consume it.
type StoragePolicy struct {
	Compaction bool
	Retention  time.Duration
}

// ChatHibernationUpdate changes any subset of the hibernation preference.
type ChatHibernationUpdate struct {
	Enabled          *bool
	WorkerIdle       *time.Duration
	OrchestratorIdle *time.Duration
}

// Offering reports which Tallpine offerings this daemon exposes to clients. It is
// resolved once at boot from daemon config: offering gates are a deployment
// decision, not a user preference, so they ride alongside the preferences the
// settings endpoint serves rather than living in the durable store.
type Offering struct {
	// Client is the deployment's client identity (TALLPINE_CLIENT); empty when unset.
	Client string
	// LocalEnabled reports whether the local offering is available.
	LocalEnabled bool
	// TrackerIntakeEnabled reports the TALLPINE_TRACKER_INTAKE gate. Clients need it
	// to avoid offering a per-project intake control the daemon will ignore.
	TrackerIntakeEnabled bool
	// BacklogRunEnabled reports the TALLPINE_BACKLOG_RUN gate (backlog run mode).
	BacklogRunEnabled bool
}

// OfferingFromConfig derives the offering gates from daemon config.
func OfferingFromConfig(cfg config.Config) Offering {
	return Offering{
		Client:               cfg.Client,
		LocalEnabled:         cfg.LocalOffering,
		TrackerIntakeEnabled: cfg.TrackerIntake,
		BacklogRunEnabled:    cfg.BacklogRun,
	}
}

// ChatCapability reports which harnesses can run in chat mode, so the UI can warn
// that choosing chat narrows the agents available rather than letting the user
// discover it at spawn time.
type ChatCapability interface {
	SupportsChat(harness domain.AgentHarness) bool
}

// Service reads and writes preferences.
type Service struct {
	store    Store
	chat     ChatCapability
	offering Offering
	now      func() time.Time
	// chatHibernation caches the durable hibernation policy; nil is unread.
	chatHibernation atomic.Pointer[domain.ChatHibernationPolicy]
	// versionRefresh caches the durable toggle: 0 unread, 1 on, 2 off.
	versionRefresh atomic.Int32
	// storage caches the durable storage-compaction setting; nil until read.
	storage atomic.Pointer[StoragePolicy]
	// managed holds the values the managed file sets and locks (nil: none).
	managed *managedcfg.Locks
	// onDynamicContext runs after the dynamic orchestrator context toggle is
	// saved, so a daemon can end running research modes when it is turned off.
	onDynamicContext func(ctx context.Context, enabled bool)
}

// SetManaged sets the managed locks; call before the service is shared.
func (s *Service) SetManaged(l *managedcfg.Locks) { s.managed = l }

// New builds the service.
func New(store Store, chat ChatCapability, offering Offering, now func() time.Time) *Service {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{store: store, chat: chat, offering: offering, now: now}
}

// Offering reports the boot-resolved offering gates.
func (s *Service) Offering() Offering {
	return s.offering
}

// Get returns the current preferences.
func (s *Service) Get(ctx context.Context) (Snapshot, error) {
	snapshot, err := s.store.GetAppSettings(ctx)
	if err == nil {
		s.cacheVersionRefresh(snapshot.VersionRefresh)
		s.cacheStoragePolicy(snapshot)
		policy := snapshot.ChatHibernation
		s.chatHibernation.Store(&policy)
		// The caches keep the stored values; the managed overlay is applied on read.
		snapshot = s.applyManaged(snapshot)
	}
	return snapshot, err
}

func (s *Service) cacheStoragePolicy(snapshot Snapshot) {
	days := snapshot.ProviderEventRetentionDays
	if days < MinProviderEventRetentionDays || days > MaxProviderEventRetentionDays {
		days = DefaultProviderEventRetentionDays
	}
	s.storage.Store(&StoragePolicy{
		Compaction: snapshot.StorageCompaction,
		Retention:  time.Duration(days) * 24 * time.Hour,
	})
}

// StoragePolicy reports the storage-compaction setting. It reads the store once
// and then serves the cached value; an unreadable store counts as off until a
// read succeeds, so a failure never deletes anything.
func (s *Service) StoragePolicy(ctx context.Context) StoragePolicy {
	if policy := s.storage.Load(); policy != nil {
		return s.managedStorage(*policy)
	}
	snapshot, err := s.store.GetAppSettings(ctx)
	if err != nil {
		return StoragePolicy{}
	}
	s.cacheStoragePolicy(snapshot)
	return s.managedStorage(*s.storage.Load())
}

// managedStorage applies the managed retention window at read time, so a change
// of the managed file takes effect without a settings read refreshing a cache.
func (s *Service) managedStorage(p StoragePolicy) StoragePolicy {
	if days, ok := s.managedRetention(); ok {
		p.Retention = time.Duration(days) * 24 * time.Hour
	}
	return p
}

// SetStorageCompaction changes the storage-compaction toggle, the retention
// window, or both. A nil argument keeps the current value.
func (s *Service) SetStorageCompaction(ctx context.Context, enabled *bool, retentionDays *int) (Snapshot, error) {
	if enabled == nil && retentionDays == nil {
		return Snapshot{}, fmt.Errorf("%w: set enabled, providerEventRetentionDays, or both", ErrInvalidStorageCompaction)
	}
	if retentionDays != nil && (*retentionDays < MinProviderEventRetentionDays || *retentionDays > MaxProviderEventRetentionDays) {
		return Snapshot{}, fmt.Errorf("%w: providerEventRetentionDays must be between %d and %d",
			ErrInvalidStorageCompaction, MinProviderEventRetentionDays, MaxProviderEventRetentionDays)
	}
	current, err := s.store.GetAppSettings(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	if retentionDays != nil && s.managed.Locked(retentionKey) {
		// Echoing the managed value is not a change, and it must not be saved as
		// the user's own: keep the stored window.
		if *retentionDays != s.applyManaged(current).ProviderEventRetentionDays {
			return Snapshot{}, s.managed.Refuse(retentionKey)
		}
		retentionDays = nil
		if enabled == nil {
			return s.Get(ctx)
		}
	}
	next, days := current.StorageCompaction, current.ProviderEventRetentionDays
	if enabled != nil {
		next = *enabled
	}
	if retentionDays != nil {
		days = *retentionDays
	}
	if days < MinProviderEventRetentionDays || days > MaxProviderEventRetentionDays {
		days = DefaultProviderEventRetentionDays
	}
	if err := s.store.SetStorageCompaction(ctx, next, days, s.now()); err != nil {
		return Snapshot{}, err
	}
	current.StorageCompaction, current.ProviderEventRetentionDays = next, days
	s.cacheStoragePolicy(current)
	return s.Get(ctx)
}

func (s *Service) cacheVersionRefresh(enabled bool) {
	if enabled {
		s.versionRefresh.Store(1)
	} else {
		s.versionRefresh.Store(2)
	}
}

// VersionRefreshEnabled reports the user's "move open sessions to new
// versions" toggle. It reads the store once and then serves the cached value;
// an unreadable store counts as off until a read succeeds.
func (s *Service) VersionRefreshEnabled(ctx context.Context) bool {
	switch s.versionRefresh.Load() {
	case 1:
		return true
	case 2:
		return false
	}
	snapshot, err := s.store.GetAppSettings(ctx)
	if err != nil {
		return false
	}
	s.cacheVersionRefresh(snapshot.VersionRefresh)
	return snapshot.VersionRefresh
}

// SetVersionRefresh changes whether idle open sessions move to new harness releases.
func (s *Service) SetVersionRefresh(ctx context.Context, enabled bool) (Snapshot, error) {
	if err := s.store.SetVersionRefresh(ctx, enabled, s.now()); err != nil {
		return Snapshot{}, err
	}
	s.cacheVersionRefresh(enabled)
	return s.Get(ctx)
}

// DefaultSessionMode resolves the default for a spawn that named no mode. A read
// failure falls back to the compatibility default rather than failing the spawn:
// an unreadable preference should not stop work.
func (s *Service) DefaultSessionMode(ctx context.Context) domain.SessionMode {
	snapshot, err := s.store.GetAppSettings(ctx)
	if err != nil {
		return domain.DefaultSessionMode
	}
	return domain.NormalizeSessionMode(snapshot.DefaultSessionMode)
}

// AutoCompactDefault is the compaction choice new sessions of a harness start
// with, "" (Auto) when none is saved.
func (s *Service) AutoCompactDefault(ctx context.Context, harness domain.AgentHarness) string {
	snapshot, err := s.store.GetAppSettings(ctx)
	if err != nil {
		return ""
	}
	return snapshot.HarnessDefaults[string(harness)].AutoCompactWindow
}

// SetDefaultSessionMode changes the default for sessions created afterwards.
//
// It deliberately does not touch existing sessions or their controllers. This
// is a preference for future sessions, not an implicit migration of the present.
func (s *Service) SetDefaultSessionMode(ctx context.Context, mode domain.SessionMode) (Snapshot, error) {
	if !mode.Valid() {
		return Snapshot{}, fmt.Errorf("%w: %q", ports.ErrChatUnsupported, mode)
	}
	if err := s.store.SetDefaultSessionMode(ctx, mode, s.now()); err != nil {
		return Snapshot{}, err
	}
	return s.Get(ctx)
}

// ChatHibernation reports the idle Chat hibernation policy. It reads the store
// once and then serves the cached value. An unreadable store counts as
// disabled until a read succeeds: never stopping an agent is the safe side.
func (s *Service) ChatHibernation(ctx context.Context) domain.ChatHibernationPolicy {
	if cached := s.chatHibernation.Load(); cached != nil {
		return *cached
	}
	snapshot, err := s.store.GetAppSettings(ctx)
	if err != nil {
		return domain.ChatHibernationPolicy{}
	}
	policy := snapshot.ChatHibernation
	s.chatHibernation.Store(&policy)
	return policy
}

// ChatHibernationEnabled reports whether idle Chat sessions may sleep.
func (s *Service) ChatHibernationEnabled() bool {
	return s.ChatHibernation(context.Background()).Enabled
}

// SetChatHibernation changes any subset of the hibernation preference.
func (s *Service) SetChatHibernation(ctx context.Context, update ChatHibernationUpdate) (Snapshot, error) {
	disabling := update.Enabled != nil && !*update.Enabled
	if disabling {
		// Disabling takes effect for this daemon even if the store is unreadable
		// or the write below fails: never stopping an agent is the safe side.
		off := domain.ChatHibernationPolicy{}
		if cached := s.chatHibernation.Load(); cached != nil {
			off = *cached
		}
		off.Enabled = false
		s.chatHibernation.Store(&off)
	}
	current, err := s.store.GetAppSettings(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	policy := current.ChatHibernation
	if update.Enabled != nil {
		policy.Enabled = *update.Enabled
	}
	if update.WorkerIdle != nil {
		policy.WorkerIdle = *update.WorkerIdle
	}
	if update.OrchestratorIdle != nil {
		policy.OrchestratorIdle = *update.OrchestratorIdle
	}
	if !domain.ValidChatHibernationIdle(policy.WorkerIdle) || !domain.ValidChatHibernationIdle(policy.OrchestratorIdle) {
		return Snapshot{}, fmt.Errorf("%w: thresholds must be whole seconds between %s and %s",
			ErrInvalidChatHibernation, domain.MinChatHibernationIdle, domain.MaxChatHibernationIdle)
	}
	if err := s.store.SetChatHibernation(ctx, policy, s.now()); err != nil {
		if !disabling {
			s.chatHibernation.Store(nil)
		}
		return Snapshot{}, err
	}
	s.chatHibernation.Store(&policy)
	return s.Get(ctx)
}

// ErrInvalidChatHibernation rejects an out-of-range hibernation threshold.
var ErrInvalidChatHibernation = errors.New("invalid chat hibernation setting")

// ChatHarnesses lists the harnesses that can run in chat mode today.
func (s *Service) ChatHarnesses(candidates []domain.AgentHarness) []domain.AgentHarness {
	if s.chat == nil {
		return nil
	}
	var out []domain.AgentHarness
	for _, harness := range candidates {
		if s.chat.SupportsChat(harness) {
			out = append(out, harness)
		}
	}
	return out
}

// maxDisabledModels bounds the picker denylist; the largest catalog (OpenCode
// via models.dev) lists under 9,000 models.
const maxDisabledModels = 20000

// normalizeDisabledModels trims and de-duplicates ids, keeping the given order.
func normalizeDisabledModels(ids []string) ([]string, bool) {
	if len(ids) > maxDisabledModels {
		return nil, false
	}
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || len(id) > 256 || strings.ContainsFunc(id, unicode.IsControl) {
			return nil, false
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, true
	}
	return out, true
}

// ErrInvalidHarnessDefault marks invalid preference input at the API boundary.
var ErrInvalidHarnessDefault = errors.New("invalid harness default")

// ErrUnknownHarness marks a preference for a harness Tallpine does not know.
var ErrUnknownHarness = errors.New("unknown harness")

// NormalizeHarnessDefault validates a harness's picker preference and returns
// it with its disabled models trimmed and de-duplicated. The settings registry
// validates app.harnessDefaults with it.
func NormalizeHarnessDefault(agent domain.AgentHarness, value domain.HarnessDefault) (domain.HarnessDefault, error) {
	if !slices.Contains(domain.AllHarnesses, agent) {
		return value, fmt.Errorf("%w %q", ErrUnknownHarness, agent)
	}
	disabled, ok := normalizeDisabledModels(value.DisabledModels)
	if !ok {
		return value, fmt.Errorf("%w: disabled models must be at most %d model ids of up to 256 characters", ErrInvalidHarnessDefault, maxDisabledModels)
	}
	value.DisabledModels = disabled
	if !domain.ValidAutoCompactWindow(value.AutoCompactWindow) {
		return value, fmt.Errorf("%w: unknown auto-compact window %q", ErrInvalidHarnessDefault, value.AutoCompactWindow)
	}
	return value, nil
}

// SetHarnessDefault replaces the selected harness's picker preference. The
// store writes only that harness's entry, so concurrent saves for other
// harnesses are kept.
func (s *Service) SetHarnessDefault(ctx context.Context, agent domain.AgentHarness, value domain.HarnessDefault) (Snapshot, error) {
	value, err := NormalizeHarnessDefault(agent, value)
	if err != nil {
		return Snapshot{}, err
	}
	if err := s.store.SetHarnessDefault(ctx, agent, value, s.now()); err != nil {
		return Snapshot{}, err
	}
	return s.Get(ctx)
}
