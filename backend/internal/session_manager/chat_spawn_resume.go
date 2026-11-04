package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

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

// resumeChatController reattaches a chat session to its provider conversation
// after a daemon restart.
//
// It resumes the stored conversation rather than starting a new one. A resume
// that fails is reported, not silently replaced with a fresh conversation:
// presenting unrelated history as continuous is worse than an error the user can
// act on, and it would strand the work the old conversation was holding.
func (m *Manager) resumeChatController(
	ctx context.Context,
	operation string,
	rec domain.SessionRecord,
	project domain.ProjectRecord,
	ws ports.WorkspaceInfo,
	requireNativeHistory bool,
	reconnectOnly bool,
	controllerGeneration string,
	historyPolicy domain.SessionInterfaceTransitionHistoryPolicy,
	awakeTargets ...domain.AwakeAgentTarget,
) (RestoreResult, error) {
	if m.chat == nil {
		return RestoreResult{}, fmt.Errorf("%s %s: %w: chat mode is not available in this build",
			operation, rec.ID, ports.ErrChatUnsupported)
	}
	releaseCodexAdmission, err := m.acquireCodexControllerAdmission(ctx, rec.Harness)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("%s %s: %w", operation, rec.ID, err)
	}
	defer releaseCodexAdmission()

	// Recomputed rather than persisted, matching the terminal path: a restored
	// session keeps its standing instructions across the relaunch.
	systemPrompt, err := m.buildSystemPromptFor(ctx, rec.Kind, rec.ProjectID, rec.ID, rec.RequestedOutput, rec.Role, true)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("%s %s: system prompt: %w", operation, rec.ID, err)
	}
	decision := m.decideFor(ctx, rec)
	agentConfig, modelNotice, err := m.enforceResumeModel(ctx, rec, restoredAgentConfigUnder(decision, rec, project.Config))
	if err != nil {
		return RestoreResult{}, fmt.Errorf("%s %s: %w", operation, rec.ID, err)
	}
	m.recordPolicy(ctx, rec.ID, decision, restoredAgentConfig(rec, project.Config).Permissions, agentConfig.Permissions, launchFacts(decision, project.Config.Env), m.fenceGapFacts(decision, rec.Harness, agentConfig.Permissions))
	additionalDirectories, err := m.restoredWorkspaceProjectDirectories(ctx, rec, project, ws.Path)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("%s %s: workspace roots: %w", operation, rec.ID, err)
	}
	env := m.chatRuntimeEnv(rec, project)
	historyMode, providerHandoff, err := m.chatProviderHandoff(ctx, rec, requireNativeHistory)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("%s %s: recover provider ownership: %w", operation, rec.ID, err)
	}
	freshIfMissing := !requireNativeHistory && !reconnectOnly && providerHandoff == nil && m.providerNeverPersisted(ctx, rec)
	awakeTarget, err := awakeReconnectTarget(rec, reconnectOnly, awakeTargets)
	if err != nil {
		return RestoreResult{}, err
	}
	var completionErr error
	_, err = m.chat.StartChat(ctx, ChatStart{
		Policy:                  decision,
		AwakeReconnectTarget:    awakeTarget,
		ReconnectOnly:           reconnectOnly,
		SessionID:               rec.ID,
		ProjectID:               rec.ProjectID,
		Kind:                    rec.Kind,
		Harness:                 rec.Harness,
		DataDir:                 m.dataDir,
		WorkspacePath:           ws.Path,
		Env:                     env,
		Model:                   agentConfig.Model,
		ModelNotice:             modelNotice,
		Effort:                  agentConfig.Effort,
		Permissions:             agentConfig.Permissions,
		SystemPrompt:            systemPrompt,
		AdditionalDirectories:   additionalDirectories,
		MCPServers:              m.tallpineMCPServers(env),
		ExpectedControllerOwner: rec.ControllerOwner(),
		PrepareControllerEnv: func(launchCtx context.Context, expected domain.SessionControllerOwner, harness domain.AgentHarness) (map[string]string, error) {
			prepared, launchEnv, prepareErr := m.prepareChatControllerEnv(
				launchCtx, rec, project.Config.Env, expected, harness,
			)
			if prepareErr != nil {
				return nil, prepareErr
			}
			if agent, ok := m.agents.Agent(harness); ok {
				m.augmentAgentRuntimeEnv(agent, launchEnv)
			}
			rec = prepared
			return launchEnv, nil
		},
		// The handle that makes this a resume rather than a new conversation.
		ProviderConversationID:             rec.Metadata.ProviderConversationID,
		FreshIfProviderConversationMissing: freshIfMissing,
		ProviderHandoff:                    providerHandoff,
		// Ordinary resumes allocate a fresh generation. Switch recovery reuses
		// the saga's reserved generation until delivery is durably settled so a
		// second restart can still prove exact target ownership.
		ControllerGeneration: controllerGeneration,
		HistoryMode:          historyMode,
		HistoryPolicy:        historyPolicy,
		ControllerReady: func(started ChatStarted) (ChatControllerCommit, error) {
			metadata := rec.Metadata
			metadata.WorkspacePath = ws.Path
			metadata.WorkspaceRepoPath = ws.RepoPath
			if ws.Branch != "" {
				metadata.Branch = ws.Branch
			}
			if freshIfMissing && started.ProviderConversationID != rec.Metadata.ProviderConversationID {
				// The provider started fresh in place of a conversation it never
				// persisted. Move the session and its untouched root together.
				if err := m.replaceUnpersistedChatProvider(ctx, rec, started.ProviderConversationID); err != nil {
					completionErr = err
					return ChatControllerCommit{}, err
				}
				// The swap is one-shot: any later call with this id must not retry it.
				rec.Metadata.ProviderConversationID = started.ProviderConversationID
				freshIfMissing = false
			}
			metadata.ProviderConversationID = started.ProviderConversationID
			// A fresh generation per launch: events still arriving from the
			// controller this one replaced carry the old one and are rejected.
			metadata.ControllerGeneration = started.ControllerGeneration

			committedConversation, commitErr := m.markChatControllerSpawned(
				ctx, rec.ID, metadata, started.Conversation, started.ProviderBoundary,
				started.CommitProviderHistory, providerHandoff, started.LiveReconnect,
			)
			if commitErr == nil {
				// Chat retains this callback for controller rebuilds. Consume the
				// reservation after publication so those rebuilds resume normally.
				providerHandoff = nil
			}
			completionErr = commitErr
			return ChatControllerCommit{
				Conversation: committedConversation,
				ControllerOwner: chatControllerOwner(
					rec, rec.Harness, started.ProviderConversationID, started.ControllerGeneration,
				),
			}, completionErr
		},
	})
	if err != nil {
		return RestoreResult{}, m.chatStartError(ctx, operation, rec, project, err, completionErr)
	}

	restored, err := m.getRecord(ctx, rec.ID)
	if err != nil {
		// StartChat has published the new controller. A failed follow-up read
		// cannot establish that it is safe to put the session back to sleep.
		return RestoreResult{}, fmt.Errorf("%w: load resumed chat after native start: %w", ports.ErrChatRecoveryInconclusive, err)
	}
	// Native continuity: the provider still holds the conversation, so the agent
	// resumes with its own history rather than a replayed prompt.
	return RestoreResult{Session: restored, Mode: RestoreModeNative}, nil
}

type unpersistedChatProviderStore interface {
	ReplaceUnpersistedChatProvider(ctx context.Context, id domain.SessionID, expectedProviderConversationID, providerConversationID string) error
}

// providerNeverPersisted reports durable proof that the stored provider
// conversation was reserved but never started: the adapter finds no persisted
// history behind the id and Tallpine recorded no conversation activity. Only then may
// a provider that cannot find the id start fresh in its place.
func (m *Manager) providerNeverPersisted(ctx context.Context, rec domain.SessionRecord) bool {
	id := rec.Metadata.ProviderConversationID
	if id == "" {
		return false
	}
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return false
	}
	handoff, ok := agent.(ports.AgentInterfaceHandoff)
	if !ok {
		return false
	}
	if _, ok := m.store.(unpersistedChatProviderStore); !ok {
		return false
	}
	persisted, err := m.persistedNativeConversationID(ctx, rec, id, handoff)
	return err == nil && persisted == ""
}

func (m *Manager) replaceUnpersistedChatProvider(ctx context.Context, rec domain.SessionRecord, providerConversationID string) error {
	store, ok := m.store.(unpersistedChatProviderStore)
	if !ok {
		return errors.New("replace unpersisted Chat provider: storage is unavailable")
	}
	return store.ReplaceUnpersistedChatProvider(ctx, rec.ID, rec.Metadata.ProviderConversationID, providerConversationID)
}

func (m *Manager) markChatControllerSpawned(
	ctx context.Context,
	id domain.SessionID,
	metadata domain.SessionMetadata,
	conversation domain.ConversationRecord,
	providerBoundary *domain.ConversationBranch,
	commitProviderHistory func(context.Context) error,
	handoff *domain.ChatProviderHandoff,
	liveReconnect bool,
) (domain.ConversationRecord, error) {
	if liveReconnect {
		if providerBoundary != nil {
			return domain.ConversationRecord{}, errors.New("live Chat reconnect cannot replace the provider boundary")
		}
		return conversation, m.lcm.MarkChatReconnected(ctx, id, metadata)
	}
	if handoff != nil && (providerBoundary == nil || commitProviderHistory == nil) {
		return domain.ConversationRecord{}, errors.New("native Chat handoff requires atomic history publication")
	}
	if providerBoundary == nil {
		return conversation, m.lcm.MarkSpawned(ctx, id, metadata)
	}
	var err error
	if commitProviderHistory == nil {
		err = m.lcm.MarkChatSpawned(ctx, id, metadata, *providerBoundary)
	} else {
		prepared, ok := m.lcm.(interface {
			MarkChatSpawnedPrepared(
				context.Context,
				domain.SessionID,
				domain.SessionMetadata,
				domain.ConversationBranch,
				*domain.ChatProviderHandoff,
				func(context.Context) error,
			) error
		})
		if !ok {
			return domain.ConversationRecord{}, errors.New(
				"atomic Chat provider-history persistence is unavailable",
			)
		}
		err = prepared.MarkChatSpawnedPrepared(
			ctx, id, metadata, *providerBoundary, handoff, commitProviderHistory,
		)
	}
	if err != nil {
		return domain.ConversationRecord{}, err
	}
	conversation.ActiveBranchID = providerBoundary.ID
	conversation.SessionID = id
	conversation.UpdatedAt = providerBoundary.CreatedAt
	return conversation, nil
}

func workspaceProjectDirectories(root string, project *ports.WorkspaceProjectInfo) []string {
	if project == nil {
		return nil
	}
	root = filepath.Clean(root)
	directories := make([]string, 0, len(project.Worktrees))
	for _, worktree := range project.Worktrees {
		if worktree.Path == "" || filepath.Clean(worktree.Path) == root {
			continue
		}
		directories = append(directories, worktree.Path)
	}
	return directories
}

func (m *Manager) restoredWorkspaceProjectDirectories(
	ctx context.Context,
	rec domain.SessionRecord,
	project domain.ProjectRecord,
	root string,
) ([]string, error) {
	if project.Kind.WithDefault() != domain.ProjectKindWorkspace {
		return nil, nil
	}
	rows, err := m.store.ListSessionWorktrees(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	root = filepath.Clean(root)
	directories := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.WorktreePath == "" || filepath.Clean(row.WorktreePath) == root {
			continue
		}
		directories = append(directories, row.WorktreePath)
	}
	return directories, nil
}

// chatResumeQueuer is implemented by launchers that can queue a relayed
// message for a Chat whose agent is not running.
type chatResumeQueuer interface {
	QueueRelayForResume(ctx context.Context, id domain.SessionID, text, clientMessageID string, options ports.MessageDeliveryOptions) (ports.MessageReceipt, error)
}

// queueSendForResume accepts a send to a Chat whose agent went down
// involuntarily (its host died with the previous daemon, recorded as
// AgentExitHostLost, and the startup resume has not succeeded yet): the message is queued durably and the
// session resumed on the bounded startup schedule, whose resume drains the
// queue. Sleeping and starting sessions keep their own wake/provisioning paths.
func (m *Manager) queueSendForResume(ctx context.Context, rec domain.SessionRecord, message, clientMessageID string, options ports.MessageDeliveryOptions, relayErr error) (ports.MessageReceipt, bool) {
	queuer, ok := m.chat.(chatResumeQueuer)
	if !ok || rec.HibernatedAt != nil || rec.ProvisionState.WithDefault() != domain.SessionProvisionReady ||
		m.chat.HasLiveChatController(rec.ID) || m.agentExitReason(ctx, rec.ID) != domain.AgentExitHostLost {
		return ports.MessageReceipt{}, false
	}
	receipt, err := queuer.QueueRelayForResume(ctx, rec.ID, message, clientMessageID, options)
	if err != nil {
		m.logger.Warn("chat send: could not queue for resume", "sessionID", rec.ID, "sendError", relayErr, "error", err)
		return ports.MessageReceipt{}, false
	}
	m.logger.Info("chat send queued until the agent resumes", "sessionID", rec.ID, "cause", relayErr)
	m.resumeStartupChats(m.backgroundContext, []domain.SessionID{rec.ID})
	return receipt, true
}

// chatProviderHandoff picks the history mode and prepares the provider handoff
// for a chat resume.
func (m *Manager) chatProviderHandoff(ctx context.Context, rec domain.SessionRecord, requireNativeHistory bool) (ports.ChatHistoryMode, *domain.ChatProviderHandoff, error) {
	historyMode := ports.ChatHistoryImport
	var providerHandoff *domain.ChatProviderHandoff
	var err error
	if requireNativeHistory {
		historyMode = ports.ChatHistoryRequired
		providerHandoff, err = m.prepareLiveChatProviderHandoff(ctx, rec)
	} else {
		providerHandoff, err = m.prepareRecoveredChatProviderHandoff(ctx, rec)
	}
	return historyMode, providerHandoff, err
}

// awakeReconnectTarget validates the single awake-reconnect target against the
// session's current controller owner.
func awakeReconnectTarget(rec domain.SessionRecord, reconnectOnly bool, awakeTargets []domain.AwakeAgentTarget) (*domain.AwakeAgentTarget, error) {
	var awakeTarget *domain.AwakeAgentTarget
	if len(awakeTargets) > 0 {
		if len(awakeTargets) != 1 || !reconnectOnly || awakeTargets[0].SessionID != rec.ID || awakeTargets[0].Owner != rec.ControllerOwner() {
			return nil, fmt.Errorf("awake reconnect ownership changed")
		}
		target := awakeTargets[0]
		awakeTarget = &target
	}
	return awakeTarget, nil
}

// chatRuntimeEnv is the environment handed to a resumed chat controller.
func (m *Manager) chatRuntimeEnv(rec domain.SessionRecord, project domain.ProjectRecord) map[string]string {
	env := m.runtimeEnv(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env)
	if agent, ok := m.agents.Agent(rec.Harness); ok {
		m.augmentAgentRuntimeEnv(agent, env)
	}
	return env
}

// chatStartError reports a failed chat resume, stopping the controller when the
// failure came after it started.
func (m *Manager) chatStartError(ctx context.Context, operation string, rec domain.SessionRecord, project domain.ProjectRecord, err, completionErr error) error {
	if completionErr != nil {
		m.stopChatBestEffort(ctx, rec.ID)
		return fmt.Errorf("%s %s: completed: %w", operation, rec.ID, completionErr)
	}
	return fmt.Errorf("%s %s: resume chat: %w", operation, rec.ID, agentlaunch.RedactError(err, project.Config.Env))
}
