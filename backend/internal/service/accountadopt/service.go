package accountadopt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

var (
	// ErrUnsupportedProvider means only Claude accounts can be adopted for now.
	ErrUnsupportedProvider = errors.New("use for all sessions is available for Claude accounts only")
	// ErrInvalidPolicy means the policy is neither wait nor interrupt.
	ErrInvalidPolicy = errors.New("policy must be wait or interrupt")
	// ErrRequestKeyRequired means a batch needs a client request key to be idempotent.
	ErrRequestKeyRequired = errors.New("requestKey is required")
	// ErrNotFound means no such batch for the account.
	ErrNotFound = errors.New("account adoption not found")
)

// InProgressError reports an unfinished batch of the same provider.
type InProgressError struct{ Batch domain.AccountAdoption }

func (e *InProgressError) Error() string {
	return fmt.Sprintf("another account switch (%s) is still running. A session waiting for a busy turn keeps it running: let that turn finish, or cancel that session's move from its inspector, then try again.", e.Batch.ID)
}

// Config wires the service.
type Config struct {
	Store    ports.AccountAdoptionStore
	Accounts Accounts
	Sessions Sessions
	Mover    Mover
	Log      *slog.Logger
	// Slots caps the moves in flight at once (default 2). A move that waits for
	// a busy turn does not hold a slot.
	Slots int
	// Poll is how often a running batch reads its moves (default 1s).
	Poll  time.Duration
	Now   func() time.Time
	NewID func() string
}

// Service plans, starts and reports bulk account switches.
type Service struct {
	cfg Config

	adoptMu sync.Mutex // one create at a time: check running, then store

	mu      sync.Mutex
	runCtx  context.Context
	running map[string]bool
	wg      sync.WaitGroup // the batch runners
}

// New builds the service.
func New(cfg Config) *Service {
	if cfg.Slots <= 0 {
		cfg.Slots = 2
	}
	if cfg.Poll <= 0 {
		cfg.Poll = time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.NewID == nil {
		cfg.NewID = uuid.NewString
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Service{cfg: cfg, runCtx: context.Background(), running: map[string]bool{}}
}

// Preview is what a switch would move.
type Preview struct {
	Account  domain.AgentAccount
	Sessions []Candidate
}

// Preview lists the sessions "use for all sessions" would move, after the same
// up-front refusals as Adopt.
func (s *Service) Preview(ctx context.Context, accountID string) (Preview, error) {
	account, err := s.target(ctx, accountID)
	if err != nil {
		return Preview{}, err
	}
	if err := s.cfg.Accounts.ValidateAccount(ctx, harnessOf(account.Provider), account.ID); err != nil {
		return Preview{}, err
	}
	list, err := candidates(ctx, s.cfg.Sessions, s.cfg.Accounts, account)
	if err != nil {
		return Preview{}, err
	}
	return Preview{Account: account, Sessions: list}, nil
}

// Adopt makes the account the default and queues a move of every live session
// of its provider that is on another account. A repeated (account, key) returns
// the stored batch (replay) and queues nothing.
func (s *Service) Adopt(ctx context.Context, accountID string, policy domain.AccountAdoptionPolicy, key string) (batch domain.AccountAdoption, replay bool, err error) {
	if !policy.Valid() {
		return domain.AccountAdoption{}, false, ErrInvalidPolicy
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return domain.AccountAdoption{}, false, ErrRequestKeyRequired
	}
	account, err := s.target(ctx, accountID)
	if err != nil {
		return domain.AccountAdoption{}, false, err
	}
	s.adoptMu.Lock()
	defer s.adoptMu.Unlock()
	if prior, ok, err := s.cfg.Store.GetAccountAdoptionByKey(ctx, account.ID, key); err != nil {
		return domain.AccountAdoption{}, false, err
	} else if ok {
		return prior, true, nil
	}
	if active, ok, err := s.cfg.Store.GetRunningAccountAdoption(ctx, account.Provider); err != nil {
		return domain.AccountAdoption{}, false, err
	} else if ok {
		return domain.AccountAdoption{}, false, &InProgressError{Batch: active}
	}
	// Refused before anything changes: a signed-out or split-history account.
	if err := s.cfg.Accounts.ValidateAccount(ctx, harnessOf(account.Provider), account.ID); err != nil {
		return domain.AccountAdoption{}, false, err
	}
	list, err := candidates(ctx, s.cfg.Sessions, s.cfg.Accounts, account)
	if err != nil {
		return domain.AccountAdoption{}, false, err
	}
	now := s.cfg.Now()
	batch = domain.AccountAdoption{
		ID: s.cfg.NewID(), AccountID: account.ID, Provider: account.Provider, Policy: policy,
		RequestKey: key, State: domain.AccountAdoptionRunning, CreatedAt: now, UpdatedAt: now,
	}
	for i, c := range list {
		batch.Items = append(batch.Items, domain.AccountAdoptionItem{
			SessionID: c.SessionID, Position: i, IsOrchestrator: c.IsOrchestrator, State: domain.AccountItemQueued, UpdatedAt: now,
		})
	}
	if err := s.cfg.Store.CreateAccountAdoption(ctx, batch); err != nil {
		return domain.AccountAdoption{}, false, err
	}
	// The default next, before any move: sessions created while the batch runs
	// start on it. A batch that was stored but cannot proceed is closed.
	if err := s.cfg.Accounts.SetDefault(ctx, account.ID); err != nil {
		_ = s.cfg.Store.CompleteAccountAdoption(ctx, batch.ID)
		return domain.AccountAdoption{}, false, err
	}
	if len(batch.Items) == 0 {
		if err := s.cfg.Store.CompleteAccountAdoption(ctx, batch.ID); err != nil {
			return domain.AccountAdoption{}, false, err
		}
		batch.State = domain.AccountAdoptionCompleted
		return batch, false, nil
	}
	s.start(batch.ID)
	return batch, false, nil
}

// Wait blocks until the batch runners have stopped, or the timeout passes.
// Cancel the context given to Start first.
func (s *Service) Wait(timeout time.Duration) {
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

// Get returns one of the account's batches.
func (s *Service) Get(ctx context.Context, accountID, batchID string) (domain.AccountAdoption, error) {
	batch, ok, err := s.cfg.Store.GetAccountAdoption(ctx, batchID)
	if err != nil {
		return domain.AccountAdoption{}, err
	}
	if !ok || batch.AccountID != accountID {
		return domain.AccountAdoption{}, ErrNotFound
	}
	return batch, nil
}

// Latest returns the account's newest batch.
func (s *Service) Latest(ctx context.Context, accountID string) (domain.AccountAdoption, error) {
	batch, ok, err := s.cfg.Store.GetLatestAccountAdoption(ctx, accountID)
	if err != nil {
		return domain.AccountAdoption{}, err
	}
	if !ok {
		return domain.AccountAdoption{}, ErrNotFound
	}
	return batch, nil
}

// Start continues the batches a restart interrupted. Their moves are durable
// and recover on their own; this resumes reading them and starting the rest.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	s.runCtx = ctx
	s.mu.Unlock()
	batches, err := s.cfg.Store.ListRunningAccountAdoptions(ctx)
	if err != nil {
		return err
	}
	for _, b := range batches {
		s.start(b.ID)
	}
	return nil
}

func (s *Service) target(ctx context.Context, accountID string) (domain.AgentAccount, error) {
	account, err := s.cfg.Accounts.Account(ctx, strings.TrimSpace(accountID))
	if err != nil {
		return domain.AgentAccount{}, err
	}
	if account.Provider != domain.AgentAccountProviderClaude {
		return domain.AgentAccount{}, ErrUnsupportedProvider
	}
	return account, nil
}

func harnessOf(p domain.AgentAccountProvider) domain.AgentHarness {
	if p == domain.AgentAccountProviderCodex {
		return domain.HarnessCodex
	}
	return domain.HarnessClaudeCode
}
