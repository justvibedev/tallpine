package chat

import (
	"context"
	"errors"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

const maxSideExcerptContextBytes = 512 * 1024

var (
	// ErrSideUnavailable indicates that independent side chats are not configured.
	ErrSideUnavailable = errors.New("independent side chats are unavailable")
	// ErrSideAnchorUnavailable indicates that an exact completed-turn anchor cannot be found.
	ErrSideAnchorUnavailable = errors.New("exact side-chat fork anchor is unavailable")
	// ErrSideProviderUnsupported indicates that the provider cannot isolate a side host.
	ErrSideProviderUnsupported = errors.New("provider cannot isolate an independent side chat")
	// ErrSideCreateKeyRequired indicates that creation lacked an idempotency key.
	ErrSideCreateKeyRequired = errors.New("side chat idempotency key is required")
	// ErrSideQuestionInvalid indicates that a question or its client ID is missing.
	ErrSideQuestionInvalid = errors.New("side question and client message ID are required")
	// ErrSideDraftTooLarge indicates that a side draft exceeds its size limit.
	ErrSideDraftTooLarge = errors.New("side draft is too large")
	// ErrSideNotReady indicates that the side has no usable provider connection.
	ErrSideNotReady = errors.New("side provider is not ready; retry the connection")
	// ErrSidePolicyRecreate prevents resuming an unverified or uncertain boundary.
	ErrSidePolicyRecreate = errors.New("side-chat safety boundary cannot be verified; close this side and create a new one")
)

// SideStore is the launch-scoped in-memory storage boundary. Side rows never become
// the session's active main conversation or its active provider branch.
type SideStore interface {
	RenameSide(context.Context, string, string, time.Time) error
	CancelSideTurn(context.Context, string, string, time.Time) error
	ClaimSideLaunch(context.Context, string, time.Time) ([]domain.SideConversation, error)
	RecoverInterruptedSideTurns(context.Context, string, time.Time) error
	CreateSideConversation(context.Context, domain.SideConversation) (domain.SideConversation, bool, error)
	SideConversation(context.Context, string) (domain.SideConversation, error)
	ListSideConversations(context.Context, domain.SessionID, string) ([]domain.SideConversation, error)
	ListOpenSidesForRun(context.Context, string) ([]domain.SideConversation, error)
	SetSideReady(context.Context, string, string, string, time.Time) error
	SetSideRecovering(context.Context, string, string, time.Time) error
	SetSideReconstructed(context.Context, string, string, time.Time) error
	RegisterSideFork(context.Context, string, string, string, time.Time) error
	SetSideFailed(context.Context, string, string, string, time.Time) error
	CloseSideConversation(context.Context, string, time.Time) (domain.SideConversation, error)
	SideHostCleanupPending(context.Context) []domain.SideConversation
	CompleteSideHostCleanup(context.Context, string)
	CompleteSideProviderCleanup(context.Context, string) error
	SideProviderCleanupPending(context.Context) ([]domain.SideProviderCleanup, error)
	ReserveSideTurn(context.Context, domain.SideTurn, string) (domain.SideTurn, bool, error)
	ClaimNextSideTurn(context.Context, string, map[string]bool, time.Time) (domain.SideTurn, domain.SideConversation, bool, error)
	SettleSideTurn(context.Context, string, string, string, string, string, string, time.Time) error
	SetSideTurnProviderID(context.Context, string, string, string, string) error
	PrepareSideBoundary(context.Context, string, string, string) (bool, error)
	RequeueSideTurn(context.Context, string, string) error
	RetrySideTurn(context.Context, string, string) error
	UpdateSideSettings(context.Context, string, string, string, time.Time) error
	EditQueuedSideTurn(context.Context, string, string, string) error
	SideTurn(context.Context, string, string) (domain.SideTurn, error)
	SideTurns(context.Context, string, time.Time, int) ([]domain.SideTurn, bool, error)
	UpsertSideMessage(context.Context, domain.SideMessage, string) error
	SideMessages(context.Context, string, []string) ([]domain.SideMessage, error)
	UpsertSideActivity(string, string, domain.SideActivity) error
	SideActivities(string, []string) []domain.SideActivity
	SideApproval(string, string) (domain.SideActivity, error)
	SideInput(string, string) (domain.SideActivity, error)
	SetSideDraft(context.Context, string, string, string, time.Time) error
	SideDraft(context.Context, string) (string, error)
	SetSideReference(context.Context, string, ports.ChatExcerptReference, string, string, time.Time) error
}

// SideCreateRequest specifies the anchor and idempotency key for a new side chat.
type SideCreateRequest struct {
	IdempotencyKey string
	ForceNew       bool
	Label          string
	Reference      *ports.ChatExcerptReference
}

type sideRuntime struct {
	side            domain.SideConversation
	conv            ports.ChatConversation
	cancel          context.CancelFunc
	done            <-chan struct{}
	ctx             context.Context
	completed       chan ports.ChatEvent
	mu              sync.Mutex
	activeTurnID    string
	compacting      bool
	dispatchBlocked bool
	// stopRequested records that the user pressed Stop on the active turn. A
	// stopped turn must not hold the queue the way a provider failure does.
	stopRequested  bool
	providerTurnID string
	messageText    map[string]string
	messageIDs     map[string]string
	activityIDs    map[string]string
	activityText   map[string]string
}

type sideManager struct {
	service     *Service
	store       SideStore
	mu          sync.Mutex
	runID       string
	ctx         context.Context
	cancel      context.CancelFunc
	started     bool
	runtimes    map[string]*sideRuntime
	restoring   map[string]context.CancelFunc
	subscribers map[string]map[chan struct{}]struct{}
	wake        chan struct{}
}

func (m *sideManager) launchID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runID
}

func newSideManager(s *Service, store SideStore, runID string) *sideManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &sideManager{service: s, store: store, runID: runID, ctx: ctx, cancel: cancel,
		runtimes: make(map[string]*sideRuntime), restoring: make(map[string]context.CancelFunc),
		subscribers: make(map[string]map[chan struct{}]struct{}), wake: make(chan struct{}, 1)}
}
