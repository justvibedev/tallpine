package agent

import (
	"context"
	"log/slog"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Deps contains optional durable dependencies for the agent catalog service.
type Deps struct {
	// Registry lists the shipped agent adapters. Nil means no agents.
	Registry          ports.AgentRegistry
	Cache             ports.AgentModelCatalogCache
	Discoverer        ports.AgentModelDiscoverer
	ModelDiscoveryDir string
	// ModelDiscoveryEnv returns the account environment a new local session of
	// the agent would use. Credential-scoped cloud discovery bypasses it.
	ModelDiscoveryEnv func(context.Context, string) (map[string]string, error)
	// ModelDiscoveryFingerprintEnv is ModelDiscoveryEnv for fingerprint checks,
	// which must not write to the account folder. Falls back to ModelDiscoveryEnv.
	ModelDiscoveryFingerprintEnv func(context.Context, string) (map[string]string, error)
	Sessions                     SessionUsageLookup
	Context                      context.Context
	Logger                       *slog.Logger
	CodexAccountRoot             string
	CodexPendingRoot             string
	CodexSwitchStagingRoot       string
	CodexGlobalHome              string
	CodexAccounts                ports.CodexAccountClientFactory
	CodexAccountSwitches         ports.CodexAccountSwitchStore
	CodexAccountStore            ports.CodexAccountRegistryStore
	CodexOperationGate           ports.CodexOperationGate
	// Clock overrides time.Now for deterministic account-bootstrap retry tests.
	Clock func() time.Time
	// ModelGate is applied to every catalog Models and ModelCatalog return. Optional.
	ModelGate ModelGate
}

// SessionUsageLookup provides durable session facts used to rank agent choices.
// The SQLite store satisfies this narrow read boundary.
type SessionUsageLookup interface {
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
}
