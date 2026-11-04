package codex

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/service/shellterm"
)

const (
	codexAccountDisplayTTL       = 5 * time.Minute
	codexAccountLaunchTTL        = 30 * time.Second
	codexAccountAuthTimeout      = 10 * time.Second
	codexAccountReconcileTimeout = 45 * time.Second
	codexAccountUsageTTL         = 5 * time.Minute
	codexResetCreditTimeout      = 15 * time.Second
	codexAccountLoginLifetime    = 15 * time.Minute
	codexAccountProcessLimit     = 2
)

// Accounts is the display-safe account-management view. Credentials and
// filesystem locations remain daemon-private.
type Accounts struct {
	ActiveAccountID      string                           `json:"activeAccountId,omitempty"`
	AccountRevision      int64                            `json:"accountRevision"`
	Accounts             []domain.CodexAccountSnapshot    `json:"accounts"`
	Capabilities         domain.CodexAccountCapabilities  `json:"capabilities"`
	DeviceReconciliation domain.CodexDeviceReconciliation `json:"deviceReconciliation"`
	ActiveLogin          *ActiveLogin                     `json:"activeLogin,omitempty"`
	CurrentSwitch        *domain.CodexAccountSwitch       `json:"currentSwitch,omitempty"`
}

// ActiveLogin is the safe in-memory login state needed to reattach the
// Settings terminal after a renderer remount.
type ActiveLogin struct {
	OperationID   string                         `json:"operationId"`
	AccountID     string                         `json:"accountId,omitempty"`
	Status        domain.CodexAccountLoginStatus `json:"status"`
	ReasonCode    string                         `json:"reasonCode"`
	Reason        string                         `json:"reason"`
	ExpiresAt     time.Time                      `json:"expiresAt"`
	ShellTerminal LoginTerminalDisplay           `json:"shellTerminal"`
}

// LoginTerminalDisplay excludes the terminal command and filesystem
// context while retaining the mux identity and display metadata.
type LoginTerminalDisplay struct {
	HandleID  string    `json:"handleId"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"createdAt"`
}

// AccountLoginTerminalStart combines safe login state with its trusted terminal.
type AccountLoginTerminalStart struct {
	Operation     domain.CodexAccountLoginOperation `json:"operation"`
	ShellTerminal shellterm.ShellTerminal           `json:"shellTerminal"`
}

type codexAccountLoginTerminalService interface {
	OpenCommandTerminal(context.Context, shellterm.OpenCommandTerminalInput) (shellterm.ShellTerminal, error)
	CloseShellTerminal(context.Context, string) error
	IsShellTerminalChildAlive(context.Context, string) (bool, error)
}

type accountAuthCall struct {
	done     chan struct{}
	previous domain.AgentAuthenticationObservation
	retry    bool
}
type accountAuthState struct {
	invalidated bool
	// launchVerified reports that a protected Codex account call succeeded.
	// account/read only discovers local account metadata and never sets it.
	launchVerified           bool
	reauthenticationRequired bool
	failures                 int
	nextRetryAt              time.Time
	call                     *accountAuthCall
}
type accountUsageState struct {
	value     *domain.CodexAccountUsageSummary
	checkedAt time.Time
	call      chan struct{}
}
type accountReconcileCall struct {
	done chan struct{}
	err  error
}
type accountLoginOperation struct {
	snapshot                 domain.CodexAccountLoginOperation
	targetAccountID          string
	deviceState              codexFileState
	startingGlobalCredential []byte
	targetWasActive          bool
	pendingDir               string
	home                     string
	terminalHandle           string
	terminalTitle            string
	terminalCreated          time.Time
	closing                  bool
	committing               bool
	commitDone               chan struct{}
}

type codexAccountManager struct {
	ctx               context.Context
	catalog           *codexAccountCatalog
	factory           ports.CodexAccountClientFactory
	operationGate     ports.CodexOperationGate
	logger            *slog.Logger
	now               func() time.Time
	after             func(time.Duration) <-chan time.Time
	newID             func() string
	processes         chan struct{}
	mutations         chan struct{}
	executable        func() (string, error)
	terminal          codexAccountLoginTerminalService
	globalHome        string
	pendingRoot       string
	switchStagingRoot string

	mu                      sync.Mutex
	accountStoreCall        *accountReconcileCall
	accountStoreErr         error
	accountStoreNextRetry   time.Time
	accountStoreFailures    int
	auth                    map[string]*accountAuthState
	usage                   map[string]*accountUsageState
	capabilities            domain.CodexAccountCapabilities
	snapshotRevision        int64
	deviceAccountID         string
	deferredAccountID       string
	deviceCredentialPresent bool
	login                   *accountLoginOperation
	reconcile               *accountReconcileCall
	reconcileRequested      bool
	reconciliation          domain.CodexDeviceReconciliation
	reconcileFailures       int
	reconcileScheduled      bool
	accountStoreReady       bool
	capacity                *codexCapacityCoordinator
	onAuthenticationChanged func()
	subscribers             map[chan Accounts]struct{}
}
