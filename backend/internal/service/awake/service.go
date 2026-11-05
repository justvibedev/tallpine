// Package awake owns desktop wake intent independently of Connect Mobile.
package awake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Store persists desktop wake intent.
type Store interface {
	LoadAwake(context.Context) (string, error)
	SaveAwake(context.Context, string) error
}

// Host defines native ownership and observation operations.
type Host interface {
	Supported() bool
	Active() bool
	Start() error
	Stop()
	ConfigureLid(bool, bool) error
	SetupLid() error
	LidStatus() (bool, bool, string)
	ConfigureIdle(time.Time, bool) error
	SetDeadline(time.Time) error
	SetProtection(int) error
	ConfigureEffects(domain.AwakeSettings) error
	RequestRecovery() error
	RecoveryReason() string
	RelaunchBrowser(context.Context, string) error
	JoinNetwork(context.Context, string) error
	Observe(context.Context) (domain.AwakeObservation, error)
	Capabilities() []domain.AwakeCapability
}

// Bindings resolves session and application ownership.
type Bindings interface {
	Alive(context.Context, string, string, string) (bool, string, error)
	AgentActive(context.Context) (bool, error)
}

// Service serializes requests, rules, and recovery.
type Service struct {
	mu          sync.Mutex
	store       Store
	host        Host
	bindings    Bindings
	doc         domain.AwakeDocument
	observation domain.AwakeObservation
	deadlines   map[string]time.Time
	activeRules []string
	ruleMatches map[string]ruleMatch
	started     *time.Time
	reason      string
	now         func() time.Time
	// Download events are fed by the desktop, expire independently of the renderer.
	downloads     map[string]bool
	downloadsSeen time.Time
	networkOutage time.Time
	networkBusy   bool
	closed        bool
	workers       sync.WaitGroup
	networkCancel context.CancelFunc
	agentCancel   context.CancelFunc
	agentBusy     bool

	// Reconciliation keeps failed document changes in memory; retry until durable.
	reconcileSavePending   bool
	powerAuthorizationBusy bool
	// Only resume rules that this daemon paused for a battery stop. A restart
	// keeps persisted rules paused rather than guessing their previous intent.
	resumeBatteryRules bool
}

// ErrValidation identifies invalid wake requests.
var ErrValidation = errors.New("invalid stay-awake request")

// New loads intent and ends interrupted manual requests.
func New(ctx context.Context, store Store, host Host, bindings Bindings) (*Service, error) {
	s := &Service{store: store, host: host, bindings: bindings, now: time.Now, deadlines: map[string]time.Time{}, downloads: map[string]bool{}, ruleMatches: map[string]ruleMatch{}}
	raw, err := store.LoadAwake(ctx)
	if err != nil {
		return nil, err
	}
	s.doc = domain.AwakeDocument{Version: 1, Settings: domain.AwakeSettings{LidClosed: true, StopBatteryPercent: 10, PauseApps: []string{}}, Sessions: []domain.AwakeSession{}, Rules: []domain.AwakeRule{}}
	if raw == "{}" {
		s.doc.Settings.LidClosed, _, _ = host.LidStatus()
	}
	if raw != "{}" {
		if err = json.Unmarshal([]byte(raw), &s.doc); err != nil {
			return nil, fmt.Errorf("read awake intent: %w", err)
		}
		if s.doc.Version != 1 {
			return nil, errors.New("unsupported awake settings version")
		}
	}
	// Older PR builds could persist deferred opt-ins. Disable them durably
	// before any automatic rule or host action is allowed to run.
	changed := clearDeferredSettings(&s.doc.Settings)
	// A daemon restart never resurrects an unbounded manual request.
	for i := range s.doc.Sessions {
		if s.doc.Sessions[i].EndedAt == nil {
			now := s.now()
			s.doc.Sessions[i].EndedAt = &now
			s.doc.Sessions[i].EndReason = "interrupted"
			changed = true
		}
	}
	if changed {
		if err := s.save(ctx); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func clone(doc domain.AwakeDocument) domain.AwakeDocument {
	data, _ := json.Marshal(doc)
	var out domain.AwakeDocument
	_ = json.Unmarshal(data, &out)
	return out
}
func (s *Service) save(ctx context.Context) error {
	data, err := json.Marshal(s.doc)
	if err != nil {
		return err
	}
	if err := s.store.SaveAwake(ctx, string(data)); err != nil {
		return err
	}
	// Every successful save includes any pending reconciliation changes.
	s.reconcileSavePending = false
	return nil
}

// Status returns current wake state.
func (s *Service) Status() domain.AwakeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := clone(s.doc)
	if d.Sessions == nil {
		d.Sessions = []domain.AwakeSession{}
	}
	if d.Rules == nil {
		d.Rules = []domain.AwakeRule{}
	}
	if d.Settings.PauseApps == nil {
		d.Settings.PauseApps = []string{}
	}
	_, lidActive, lidReason := s.host.LidStatus()
	reason := s.reason
	if recovery := s.host.RecoveryReason(); reason == "" && recovery != "" {
		reason = recovery
	}
	if reason == "" && lidReason != "" && lidReason != "setup_needed" {
		reason = lidReason
	}
	rules := append([]string{}, s.activeRules...)
	observation := s.observation
	observation.NetworkAddresses = append([]string{}, observation.NetworkAddresses...)
	observation.Devices = append([]string{}, observation.Devices...)
	observation.Applications = append([]domain.AwakeApplication{}, observation.Applications...)
	observation.Browsers = append([]domain.AwakeApplication{}, observation.Browsers...)
	observation.KnownNetworks = append([]string{}, observation.KnownNetworks...)
	return domain.AwakeState{Settings: d.Settings, Sessions: d.Sessions, Rules: d.Rules, ActiveRules: rules, Capabilities: s.capabilities(), Observation: observation, Active: s.host.Active(), LidActive: lidActive, Reason: reason, Paused: d.Paused, StartedAt: s.started, NetworkOutcome: d.Network.Outcome, AgentOutcome: d.AgentOutcome}
}
func invalid(message string) error { return fmt.Errorf("%w: %s", ErrValidation, message) }
