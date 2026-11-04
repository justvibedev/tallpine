package sessionmanager

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/brand"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// The chat-mode controller launch.
//
// It is deliberately a separate path rather than conditionals threaded through
// the terminal launch: a chat session starts no agent process inside a runtime,
// has no pane to write into, and has no prompt-delivery strategy. Sharing that
// code would mean every step asking which kind of session it is in.
//
// What the two modes DO share is everything above this point — project
// resolution, prompts, the seed row, the worktree, provisioning, attachments —
// which is why the branch sits where it does and not higher.

// ChatLauncher starts the structured controller for a chat session. Implemented
// by the chat service; nil in a build without chat support.
type ChatLauncher interface {
	// SupportsChat reports whether a harness has a Chat driver at all, without
	// probing the local install. Use it to decide whether Chat is even offerable.
	SupportsChat(harness domain.AgentHarness) bool
	// PreflightChat reports whether a harness can start in chat mode right now.
	// Called before any durable state exists so an unsupported request costs
	// nothing.
	PreflightChat(ctx context.Context, harness domain.AgentHarness, permissions ports.PermissionMode, decision agentpolicy.Decision) error
	// StartChat launches the controller and returns the provider conversation
	// handle to persist for resume. Implementations must call ControllerReady
	// after the provider and generation exist but before consuming live events.
	StartChat(ctx context.Context, cfg ChatStart) (ChatStarted, error)
	// StartChatTurn delivers the initial prompt as a normal turn. There is no
	// paste-and-Enter equivalent in chat mode: the provider either accepts the
	// turn or reports why.
	StartChatTurn(ctx context.Context, id domain.SessionID, text string) (string, error)
	// RelaySessionChatTurn preserves delivery identity, authorship, and acceptance
	// time. A durable clientMessageID makes queued retries idempotent.
	RelaySessionChatTurn(ctx context.Context, id domain.SessionID, text, clientMessageID string, options ports.MessageDeliveryOptions) (string, error)
	// HasLiveChatController reports whether the daemon still owns a controller
	// for the session. Resume uses this to distinguish a stale durable activity
	// state left by an older daemon from a genuinely live controller.
	HasLiveChatController(id domain.SessionID) bool
	// StopChat releases a session's controller.
	StopChat(ctx context.Context, id domain.SessionID) error
	// QueueChatPrompt records the opening prompt as a queued turn instead of
	// sending it. An asynchronous spawn has no controller yet; DrainChatQueue
	// delivers this turn, and anything the user typed after it, in order.
	QueueChatPrompt(ctx context.Context, id domain.SessionID, text string) (string, error)
	// DrainChatQueue dispatches what accumulated while the session had no
	// controller.
	DrainChatQueue(ctx context.Context, id domain.SessionID) error
}

// ChatStart is what the launcher needs. It mirrors the terminal path's
// LaunchConfig in spirit: everything resolved, nothing left to look up.
type ChatStart = ports.ChatControllerStart

// ChatStarted is the durable result of a launch.
type ChatStarted = ports.ChatControllerStarted

// ChatControllerCommit carries the post-commit conversation state back to Chat
// Service without making it read again after durable ownership has changed.
type ChatControllerCommit = ports.ChatControllerCommit

// chatProviderOwnershipStore supplies the durable handoff and history
// facts. Embedders without these reads retain strict ordinary-resume behavior.
type chatProviderOwnershipStore interface {
	GetLatestSessionInterfaceTransition(context.Context, domain.SessionID) (domain.SessionInterfaceTransition, bool, error)
	ConversationForSession(context.Context, domain.SessionID) (domain.ConversationRecord, error)
	ConversationBranch(context.Context, string, string) (domain.ConversationBranch, error)
}

func interfaceTransitionProviderBoundaryID(transitionID string) string {
	return transitionID + ":provider"
}

// chatSpawn bundles the shared state the chat launch needs from Spawn, so the
// signature does not grow to a dozen positional arguments.
type chatSpawn struct {
	policy           agentpolicy.Decision
	cfg              ports.SpawnConfig
	project          domain.ProjectRecord
	projectKind      domain.ProjectKind
	record           domain.SessionRecord
	workspace        ports.WorkspaceInfo
	workspaceProject *ports.WorkspaceProjectInfo
	prompt           string
	systemPrompt     string
	systemPromptFile string
	// promptQueued means the opening prompt is already a durable queued turn
	// (asynchronous spawn). The controller drains it; sending it again here
	// would deliver the user's brief twice.
	promptQueued bool
	// modelNotice is a model-fallback notice the controller start posts.
	modelNotice string
}

// launchChatController starts the provider controller for a chat session and
// marks it spawned.
//
// Rollback mirrors the terminal path's: a failure before the controller exists
// tears the workspace down, and a failure after it exists closes the controller
// first so no app-server process is left behind holding the worktree.
func (m *Manager) launchChatController(ctx context.Context, in chatSpawn) (domain.SessionRecord, error) {
	id := in.record.ID
	releaseCodexAdmission, err := m.acquireCodexControllerAdmission(ctx, in.cfg.Harness)
	if err != nil {
		m.rollbackSeedSpawnWorkspace(ctx, in.record, in.workspace, in.workspaceProject, false, in.promptQueued)
		return domain.SessionRecord{}, wrapSpawnStage(id, ErrChatController, err)
	}
	defer releaseCodexAdmission()
	agentConfig := in.cfg.AgentConfig
	if !in.cfg.AgentConfigResolved {
		agentConfig = applySpawnAgentConfigUnder(in.policy, in.cfg.Harness, effectiveAgentConfigFor(in.cfg.Harness, in.cfg.Kind, in.cfg.Role, in.project.Config), in.cfg.AgentConfig)
	}

	var diffBaseSHA, diffBaseRef string
	if in.projectKind == domain.ProjectKindSingleRepo {
		diffBaseSHA, diffBaseRef = resolveSpawnDiffBase(
			ctx, in.workspace.Path, in.workspace.BaseRef)
	}
	// Chat Service retains this unprivileged base environment. The bearer is
	// minted inside its per-session launch gate and is never cached for reuse.
	env := m.runtimeEnv(id, in.record.ProjectID, in.record.IssueID, in.project.Config.Env)
	if agent, ok := m.agents.Agent(in.cfg.Harness); ok {
		m.augmentAgentRuntimeEnv(agent, env)
		// Chat launches go through the same workspace preparation as terminal
		// launches: without the adapter's hooks (e.g. Command Code's
		// SessionStart hook, which injects the standing instructions from the
		// prompt file) a fresh chat session would start with no Tallpine role or
		// coordination context.
		if err := m.prepareWorkspace(ctx, agent, id, in.workspace.Path, in.systemPrompt, in.systemPromptFile, agentConfig, env); err != nil {
			m.rollbackSeedSpawnWorkspace(ctx, in.record, in.workspace, in.workspaceProject, false, in.promptQueued)
			return domain.SessionRecord{}, wrapSpawnStage(id, ErrSpawnPrepare, err)
		}
	}

	var (
		controllerCommitted bool
		completionErr       error
	)
	_, err = m.chat.StartChat(ctx, ChatStart{
		Policy:                  in.policy.WithSession(id),
		SessionID:               id,
		ProjectID:               in.cfg.ProjectID,
		Kind:                    in.cfg.Kind,
		Harness:                 in.cfg.Harness,
		DataDir:                 m.dataDir,
		WorkspacePath:           in.workspace.Path,
		Env:                     env,
		Model:                   agentConfig.Model,
		ModelNotice:             in.modelNotice,
		Effort:                  agentConfig.Effort,
		Permissions:             agentConfig.Permissions,
		SystemPrompt:            in.systemPrompt,
		AdditionalDirectories:   workspaceProjectDirectories(in.workspace.Path, in.workspaceProject),
		MCPServers:              m.tallpineMCPServers(env),
		ExpectedControllerOwner: in.record.ControllerOwner(),
		PrepareControllerEnv: func(launchCtx context.Context, expected domain.SessionControllerOwner, harness domain.AgentHarness) (map[string]string, error) {
			prepared, launchEnv, prepareErr := m.prepareChatControllerEnv(
				launchCtx, in.record, in.project.Config.Env, expected, harness,
			)
			if prepareErr != nil {
				return nil, fmt.Errorf("%w: %w", ErrSpawnBrowser, prepareErr)
			}
			if agent, ok := m.agents.Agent(harness); ok {
				m.augmentAgentRuntimeEnv(agent, launchEnv)
			}
			in.record = prepared
			return launchEnv, nil
		},
		ControllerReady: func(started ChatStarted) (ChatControllerCommit, error) {
			metadata := domain.SessionMetadata{
				Permissions:       in.record.Metadata.Permissions,
				Branch:            in.workspace.Branch,
				WorkspacePath:     in.workspace.Path,
				WorkspaceRepoPath: in.workspace.RepoPath,
				Prompt:            in.prompt,
				DiffBaseSHA:       diffBaseSHA,
				DiffBaseRef:       diffBaseRef,
				ArtifactDir:       in.record.Metadata.ArtifactDir,
				// No RuntimeHandleID or RuntimeLaunchID: a chat session has no
				// agent pane. Leaving them empty keeps the reaper from probing for
				// a terminal that was never created.
				ProviderConversationID:    started.ProviderConversationID,
				ControllerGeneration:      started.ControllerGeneration,
				BrowserCapabilityVerifier: in.record.Metadata.BrowserCapabilityVerifier,
				Model:                     agentConfig.Model,
				Effort:                    agentConfig.Effort,
			}
			committedConversation, commitErr := m.markChatControllerSpawned(
				ctx, id, metadata, started.Conversation, started.ProviderBoundary,
				started.CommitProviderHistory, nil, started.LiveReconnect,
			)
			completionErr = commitErr
			controllerCommitted = completionErr == nil
			return ChatControllerCommit{
				Conversation: committedConversation,
				ControllerOwner: chatControllerOwner(
					in.record, in.cfg.Harness, started.ProviderConversationID, started.ControllerGeneration,
				),
			}, completionErr
		},
	})
	if err != nil {
		if completionErr != nil || controllerCommitted {
			m.stopChatAfterSpawnFailure(ctx, id)
			workspaceDestroyed := m.rollbackPreparedSpawnWorkspaceAfterFailure(ctx, in.record, in.workspace, in.workspaceProject, true)
			if in.promptQueued {
				if workspaceDestroyed {
					m.clearProvisionedWorkspace(ctx, id, in.workspace.Path)
				}
			} else {
				m.markSpawnFailedTerminatedAfterFailure(ctx, id, false)
			}
			if completionErr != nil {
				return domain.SessionRecord{}, wrapSpawnStage(id, ErrSpawnCommit, completionErr)
			}
			return domain.SessionRecord{}, wrapSpawnStage(id, ErrChatController, agentlaunch.RedactError(err, in.project.Config.Env))
		}
		// No controller exists, so nothing provider-side needs closing. The
		// runtime was never touched, hence runtimeDestroyed=false.
		m.rollbackSeedSpawnWorkspace(ctx, in.record, in.workspace, in.workspaceProject, false, in.promptQueued)
		return domain.SessionRecord{}, wrapSpawnStage(id, ErrChatController, agentlaunch.RedactError(err, in.project.Config.Env))
	}

	// The initial prompt is a normal turn through the controller. There is no
	// paste-and-Enter equivalent here, and no "deliver after start" variant: the
	// provider either accepts the turn or reports why.
	if in.prompt != "" && !in.promptQueued {
		if _, err := m.chat.StartChatTurn(ctx, id, in.prompt); err != nil {
			m.stopChatAfterSpawnFailure(ctx, id)
			m.rollbackPreparedSpawnWorkspaceAfterFailure(ctx, in.record, in.workspace, in.workspaceProject, true)
			m.markSpawnFailedTerminatedAfterFailure(ctx, id, false)
			return domain.SessionRecord{}, wrapSpawnStage(id, ErrSpawnDeliverPrompt, agentlaunch.RedactError(err, in.project.Config.Env))
		}
	}

	return m.getRecord(ctx, id)
}

// tallpineMCPServers is the tool server every chat session gets: `tallpine mcp` from the
// daemon's own binary, which gives the agent the html_preview and html_render
// tools. Chat Service keeps it for every later start and resume of the
// session (edit, rollback, fork). Its env holds only what the CLI needs to
// reach this daemon for this session, never a credential such as
// TALLPINE_BROWSER_CAPABILITY.
func (m *Manager) tallpineMCPServers(env map[string]string) []ports.ChatMCPServerConfig {
	executable, err := m.executable()
	if err != nil || !filepath.IsAbs(executable) {
		m.logger.Warn("chat session gets no tallpine tool server; the agent can still run tallpine render",
			"executable", executable, "error", err)
		return nil
	}
	// TALLPINE_DATA_DIR too: a harness may start the server with exactly this env, and
	// without it config.Load needs $HOME to find the data dir.
	serverEnv := make(map[string]string, 3)
	for _, key := range []string{EnvSessionID, EnvRunFile, EnvDataDir} {
		if value := env[key]; value != "" {
			serverEnv[key] = value
		}
	}
	return []ports.ChatMCPServerConfig{{
		Name: brand.MCPServerName, Type: "stdio", Command: executable, Args: []string{"mcp"}, Env: serverEnv,
	}}
}

func (m *Manager) stopChatAfterSpawnFailure(ctx context.Context, id domain.SessionID) {
	cleanupCtx, cancel := spawnRollbackContext(ctx)
	defer cancel()
	m.stopChatBestEffort(cleanupCtx, id)
}

// stopChatBestEffort closes a controller during rollback. A failure here is
// logged rather than returned: the spawn is already failing, and the caller's
// error is the one worth surfacing.
func (m *Manager) stopChatBestEffort(ctx context.Context, id domain.SessionID) {
	if m.chat == nil {
		return
	}
	if err := m.chat.StopChat(ctx, id); err != nil {
		m.logger.Warn("spawn rollback: close chat controller", "sessionID", id, "error", err)
	}
}

// sendChat routes an outbound message into a chat session's conversation.
//
// It reports handled=false for anything that is not a live chat session, so the
// terminal path below it is reached unchanged — including for a session this
// build cannot serve, where the runtime guard's refusal is still the right answer.
//
// The refusals here mirror the terminal path's: a terminated session cannot
// receive a message, and one whose controller is gone cannot either. Busy is not
// a refusal — the controller queues a mid-turn message, which is strictly better
// than the terminal path's habit of dropping a nudge it cannot safely deliver.
func (m *Manager) sendChat(ctx context.Context, id domain.SessionID, message, clientMessageID string, options ports.MessageDeliveryOptions) (bool, error) {
	return m.sendChatReceipt(ctx, id, message, clientMessageID, options, nil)
}

// chatReceiptRelayer is implemented by launchers that can say whether a relayed
// turn started or was queued behind a running one.
type chatReceiptRelayer interface {
	RelaySessionChatTurnReceipt(ctx context.Context, id domain.SessionID, text, clientMessageID string, options ports.MessageDeliveryOptions) (ports.MessageReceipt, error)
}

// sendChatReceipt is sendChat that records the delivery receipt when asked.
func (m *Manager) sendChatReceipt(ctx context.Context, id domain.SessionID, message, clientMessageID string, options ports.MessageDeliveryOptions, receipt *ports.MessageReceipt) (bool, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return false, fmt.Errorf("send %s: session: %w", id, err)
	}
	if !ok || domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		return false, nil
	}
	if m.chat == nil {
		return true, fmt.Errorf("send %s: %w: chat mode is not available in this build",
			id, ports.ErrChatUnsupported)
	}
	if rec.IsTerminated {
		return true, fmt.Errorf("send %s: %w", id, ErrTerminated)
	}
	var relayErr error
	started := m.clock()
	var relayDone time.Time
	defer func() { m.logSlowChatSend(id, started, relayDone) }()
	if relayer, ok := m.chat.(chatReceiptRelayer); ok && receipt != nil {
		var got ports.MessageReceipt
		got, relayErr = relayer.RelaySessionChatTurnReceipt(ctx, id, message, clientMessageID, options)
		if relayErr == nil {
			*receipt = got
		}
	} else {
		var turnID string
		turnID, relayErr = m.chat.RelaySessionChatTurn(ctx, id, message, clientMessageID, options)
		if receipt != nil && relayErr == nil {
			*receipt = ports.MessageReceipt{Outcome: ports.MessageDelivered, ID: turnID}
		}
	}
	relayDone = m.clock()
	if relayErr != nil {
		// The relay can run for many seconds (it waits on the controller), and
		// the agent may be stopped meanwhile. Judge the stop from a fresh
		// record, not the one read before the relay, or a send that raced the
		// stop surfaces as INTERNAL_ERROR instead of AGENT_STOPPED (B488).
		// ExitAgent closes the controller before it records the exit, so an exit
		// in progress counts as stopped too. Check it before the read: the exit
		// is recorded before the operation ends, so one of the two sees it.
		exiting := m.agentOperationIs(id, agentOperationExit)
		if fresh, found, getErr := m.store.GetSession(ctx, id); getErr == nil && found {
			rec = fresh
		}
		if queued, ok := m.queueSendForResume(ctx, rec, message, clientMessageID, options, relayErr); ok {
			if receipt != nil {
				*receipt = queued
			}
			return true, nil
		}
		if (rec.Activity.State == domain.ActivityExited || exiting) && rec.HibernatedAt == nil && !m.chat.HasLiveChatController(id) {
			// Stopped on purpose (or for an unknown reason): say so instead of
			// failing with an internal error, and leave it stopped.
			m.logger.Info("chat send refused: agent stopped", "sessionID", id, "cause", relayErr)
			return true, fmt.Errorf("send %s: %w", id, ErrAgentStoppedByUser)
		}
		m.logger.Warn("chat send failed", "sessionID", id, "error", relayErr)
		return true, fmt.Errorf("send %s: %w", id, relayErr)
	}
	return true, nil
}

// WorkflowDefaultsProvider supplies the app-wide workflow defaults projects
// inherit. It is an optional ability of Deps.Defaults; without it every default
// is the built-in one.
type WorkflowDefaultsProvider interface {
	WorkflowDefaults(ctx context.Context) domain.WorkflowDefaults
}

// workflowDefaults reads the app defaults at spawn time, so a change affects
// only sessions created afterwards.
func (m *Manager) workflowDefaults(ctx context.Context) domain.WorkflowDefaults {
	if p, ok := m.defaults.(WorkflowDefaultsProvider); ok {
		return p.WorkflowDefaults(ctx).Normalized()
	}
	return domain.DefaultWorkflowDefaults()
}

// SessionModeDefaults supplies the daemon-owned default session interface.
type SessionModeDefaults interface {
	DefaultSessionMode(ctx context.Context) domain.SessionMode
}

// resolveSessionMode applies startSessionMode with the daemon-owned default,
// read here, at spawn time, so changing the preference affects only sessions
// created afterwards. An existing session changes only through an explicit,
// capability-gated interface transition; it is never re-resolved from the
// default.
func (m *Manager) resolveSessionMode(ctx context.Context, kind domain.SessionKind, requested, projectDefault domain.SessionMode) domain.SessionMode {
	var defaultMode domain.SessionMode
	if m.defaults != nil {
		defaultMode = m.defaults.DefaultSessionMode(ctx)
	}
	return startSessionMode(kind, requested, projectDefault, defaultMode)
}

// startSessionMode is the precedence for a spawn's controller mode:
//
//  1. the mode the caller explicitly requested;
//  2. Chat for an orchestrator;
//  3. the project's default session mode;
//  4. the daemon-owned default;
//  5. the compatibility default, TUI.
//
// Whether the harness can run the result is checked afterwards.
func startSessionMode(kind domain.SessionKind, requested, projectDefault, defaultMode domain.SessionMode) domain.SessionMode {
	if requested.Valid() {
		return requested
	}
	if kind == domain.KindOrchestrator {
		return domain.SessionModeChat
	}
	if projectDefault.Valid() {
		return projectDefault
	}
	return domain.NormalizeSessionMode(defaultMode)
}
