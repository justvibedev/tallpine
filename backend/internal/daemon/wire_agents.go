package daemon

import (
	"context"
	"fmt"
	"path/filepath"

	claudecodeagent "tallpine.eu/tallpine/backend/internal/adapters/agent/claudecode"
	codexagent "tallpine.eu/tallpine/backend/internal/adapters/agent/codex"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/modelcatalog"
	agentregistry "tallpine.eu/tallpine/backend/internal/adapters/agent/registry"
	chatdriveracp "tallpine.eu/tallpine/backend/internal/adapters/chatdriver/acp"
	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/codexappserver"
	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/deepseekharnessacp"
	"tallpine.eu/tallpine/backend/internal/adapters/reviewer"
	"tallpine.eu/tallpine/backend/internal/codexops"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	reviewcore "tallpine.eu/tallpine/backend/internal/review"
	agentsvc "tallpine.eu/tallpine/backend/internal/service/agent"
	codexaccounts "tallpine.eu/tallpine/backend/internal/service/agentaccounts/codex"
)

// wireAgents: model discovery, Codex accounts, the agent service and reviewers.
func (b *boot) wireAgents() error {
	var err error
	b.codexModelPlugin = codexagent.New()
	codexModelDriver := codexappserver.New(b.codexModelPlugin, b.log)
	modelDiscoverer := modelcatalog.Discoverer{
		// DeepSeek Harness's catalog is the model select a short-lived
		// `dsh --profile acp` session advertises. Discovery launches with the
		// same privacy overlay and telemetry env as a real session.
		ACPOptions: map[string]modelcatalog.ACPOptionListFunc{
			"deepseek-harness": func(listCtx context.Context, request ports.AgentModelDiscoveryRequest) ([]ports.ChatConfigOption, error) {
				launch, err := deepseekharnessacp.DiscoveryLaunch(request.Binary, request.Env, b.cfg.DataDir)
				if err != nil {
					return nil, err
				}
				return chatdriveracp.DiscoverConfigOptions(listCtx, launch, request.WorkingDir, b.log)
			},
		},
		CodexModels: func(listCtx context.Context, request ports.AgentModelDiscoveryRequest) ([]ports.ChatModel, error) {
			return codexModelDriver.DiscoverModels(listCtx, request.WorkingDir, request.Env)
		},
		// Claude's model IDs are provider-specific — first-party aliases,
		// Bedrock ARNs-in-miniature, Vertex @-versions — so the list has to come
		// from whichever provider is configured. On failure, the service retains
		// the matching persisted catalog instead of inventing model aliases.
		ClaudeModels: func(listCtx context.Context, request ports.AgentModelDiscoveryRequest) ([]ports.AgentModelInfo, error) {
			return claudecodeagent.ProviderModels(listCtx, request.Binary, request.WorkingDir, request.Env)
		},
		ClaudeFingerprint: func(fingerprintCtx context.Context, request ports.AgentModelDiscoveryRequest) string {
			return claudecodeagent.ProviderCatalogFingerprint(fingerprintCtx, request.Binary, request.WorkingDir, request.Env)
		},
	}
	// Build the multi-tracker dispatching to both GitHub and GitLab once,
	// shared between the session service and the intake observer below.
	// Env-configured tokens are validated eagerly here; CLI credential probing
	// (`gh auth token`) stays lazy inside the multi-tracker so boot is not
	// blocked. May be nil (no usable credentials) — the session service's
	// nil-guard and the intake resolver's backoff both tolerate that
	// (issue #2685).
	b.tracker = newMultiTracker(b.cfg.GitLab, b.log)
	codexPlugin := codexagent.New()
	codexHome, err := codexPlugin.NativeSessionConfigDir(b.ctx, nil)
	if err != nil {
		b.stop()
		b.lcStack.Stop()
		if cdcErr := b.cdcPipe.Stop(); cdcErr != nil {
			b.log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return fmt.Errorf("resolve device-global Codex home: %w", err)
	}
	b.codexOperationGate = codexops.NewGate()
	codexAccounts := codexappserver.NewAccountFactoryWithResolver(func(resolveCtx context.Context) (string, error) {
		return codexagent.New().ResolveBinary(resolveCtx)
	}, b.log)
	b.agentAccountSvc = newAgentAccounts(b.cfg, b.store, codexHome, codexAccounts, b.log)
	// Accounts the old file registry created are copied into the account store
	// once per account (ids kept, old files left read-only for one release); a
	// failure is logged and those accounts stay unlisted until the next start.
	if imported, importErr := codexaccounts.ImportRegistry(b.ctx, filepath.Join(b.cfg.StateDir, "harnesses", "codex", "accounts"), b.store, b.log); importErr != nil {
		b.log.Warn("Codex account registry import failed", "error", importErr)
	} else if imported.Imported+imported.Skipped > 0 {
		b.log.Info("Codex account registry import finished", "imported", imported.Imported, "existing", imported.Existing, "skipped", imported.Skipped)
	}
	if pruned, pruneErr := codexaccounts.PruneStaleRows(b.ctx, filepath.Join(b.cfg.StateDir, "harnesses", "codex", "accounts"), b.store, b.log); pruneErr != nil {
		b.log.Warn("Codex account stale-row cleanup failed", "error", pruneErr)
	} else if pruned > 0 {
		b.log.Info("Codex account stale-row cleanup finished", "removed", pruned)
	}
	agentDeps := agentsvc.Deps{
		Registry: agentregistry.Harnessed,
		Cache:    b.store, Discoverer: modelDiscoverer, Sessions: b.store, Context: b.ctx, Logger: b.log,
		ModelDiscoveryDir: filepath.Join(b.cfg.DataDir, "model-discovery"),
		ModelDiscoveryEnv: func(discoveryCtx context.Context, agentID string) (map[string]string, error) {
			if agentID != string(domain.HarnessClaudeCode) {
				return nil, nil
			}
			return b.agentAccountSvc.NewSessionLaunchEnv(discoveryCtx, domain.AgentHarness(agentID))
		},
		ModelDiscoveryFingerprintEnv: func(discoveryCtx context.Context, agentID string) (map[string]string, error) {
			if agentID != string(domain.HarnessClaudeCode) {
				return nil, nil
			}
			return b.agentAccountSvc.NewSessionLaunchEnvReadOnly(discoveryCtx, domain.AgentHarness(agentID))
		},
		CodexAccountRoot:       filepath.Join(b.cfg.StateDir, "harnesses", "codex", "accounts"),
		CodexPendingRoot:       filepath.Join(b.cfg.StateDir, "harnesses", "codex", "pending-accounts"),
		CodexSwitchStagingRoot: filepath.Join(b.cfg.StateDir, "harnesses", "codex", "switch-staging"),
		CodexGlobalHome:        codexHome,
		CodexAccountSwitches:   b.store,
		CodexAccountStore:      b.store,
		CodexAccounts:          codexAccounts,
		CodexOperationGate:     b.codexOperationGate,
		ModelGate:              claudeModelGate,
	}
	// Minimum Claude Code versions the API has reported per model persist
	// across restarts so a model stays marked unavailable until the CLI updates.
	claudecodeagent.ConfigureModelVersionRequirements(
		filepath.Join(b.cfg.StateDir, "harnesses", "claude-code", "model-versions.json"))
	b.agentSvc = agentsvc.NewWithDeps(agentDeps)
	b.agentSvc.WarmModelCatalogs(b.ctx)

	b.persistentHostsReconciled = make(chan struct{})
	b.reviewerChatsRecovered = make(chan struct{})
	b.reviewers, err = reviewer.NewResolver()
	if err != nil {
		b.stop()
		b.lcStack.Stop()
		if cdcErr := b.cdcPipe.Stop(); cdcErr != nil {
			b.log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return fmt.Errorf("reviewer resolver: %w", err)
	}
	b.reviewerAccounts = &reviewcore.AccountEnvSlot{}
	return nil
}
