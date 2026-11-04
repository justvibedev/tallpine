package sessionmanager

import (
	"context"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// spawnCreateSeed claims a prepared task or creates the seed row.
func (m *Manager) spawnCreateSeed(ctx context.Context, st *spawnState) *spawnExit {
	cfg := &st.cfg
	project := st.project
	mode := st.mode
	st.asyncChat = cfg.Async && mode == domain.SessionModeChat && m.chat != nil && (cfg.Kind == domain.KindOrchestrator || cfg.Kind == domain.KindWorker)

	var prep *taskPreparation
	if cfg.AutomationRunID == nil && cfg.Branch == "" {
		prep = m.claimTaskPreparation(cfg.TaskPreparation, cfg.ProjectID)
	}
	var rec domain.SessionRecord
	if prep != nil {
		rec = prep.record
	} else {
		var err error
		seed := seedRecord(st.policy, *cfg, project.Config, m.workflowDefaults(ctx), m.clock())
		lane, laneErr := m.resolveLane(ctx, cfg.ProjectID, *cfg)
		if laneErr != nil {
			// An idempotent replay must still return the original session even
			// when the lane can no longer be resolved (e.g. the parent is gone).
			if cfg.ClientRequestID != "" {
				if prior, found, err := m.store.GetSessionByClientRequestID(ctx, cfg.ClientRequestID); err == nil && found {
					replay, replayErr := replayClientRequest(prior, cfg.ClientRequestHash)
					return &spawnExit{rec: replay, err: replayErr}
				}
			}
			return spawnFail(wrapSpawnStageEarly(ErrSpawnCreate, laneErr))
		}
		seed.Lane = lane
		if mode == domain.SessionModeChat {
			seed.Metadata.Model = cfg.AgentConfig.Model
			seed.Metadata.Effort = cfg.AgentConfig.Effort
		}
		if cfg.ClientRequestID != "" {
			var fresh bool
			rec, fresh, err = m.store.CreateClientRequestSession(ctx, seed)
			if err == nil && !fresh {
				replay, replayErr := replayClientRequest(rec, cfg.ClientRequestHash)
				return &spawnExit{rec: replay, err: replayErr}
			}
		} else if cfg.AutomationRunID != nil {
			var fresh bool
			rec, fresh, err = m.store.CreateAutomationSession(ctx, seed)
			if err == nil && !fresh {
				if rec.AutomationLaunchCompleted {
					// The prior launch already built and delivered a prompt;
					// this call built none, since prompt construction happens
					// further down (it needs the session id for the artifact
					// directory path) and this branch returns before reaching it.
					return &spawnExit{rec: rec}
				}
				return spawnFail(fmt.Errorf("spawn: automation session %s has an incomplete prior launch", rec.ID))
			}
		} else {
			rec, err = m.store.CreateSession(ctx, seed)
		}
		if err != nil {
			return spawnFail(wrapSpawnStageEarly(ErrSpawnCreate, err))
		}
	}
	st.prep = prep
	st.rec = rec
	st.id = rec.ID
	if prep != nil {
		// Only a prepared task's existing row needs a separate lane write; a
		// fresh row got its lane in the create transaction.
		if err := m.recordLane(ctx, rec.ID, rec.ProjectID, *cfg); err != nil {
			return spawnFail(wrapSpawnStageEarly(ErrSpawnCreate, err))
		}
	}
	return nil
}

// spawnBindAndBuildPrompts binds the agent account, reserves the artifact
// directory, and builds the prompt texts and system prompt file.
func (m *Manager) spawnBindAndBuildPrompts(ctx context.Context, st *spawnState) *spawnExit {
	cfg := &st.cfg
	prep := st.prep
	id := st.id
	if m.agentAccounts != nil {
		if err := m.agentAccounts.BindSession(ctx, id, cfg.Harness, cfg.AgentAccountID); err != nil {
			if prep != nil {
				cleanupCtx, cancel := spawnRollbackContext(ctx)
				m.discardClaimedTaskPreparation(cleanupCtx, prep)
				cancel()
			} else {
				m.rollbackSpawnSeedRow(ctx, id)
			}
			return spawnFail(wrapSpawnStage(id, ErrSpawnAgentAccount, err))
		}
	}
	// The system prompt embeds the artifact directory path, so the directory
	// must exist and be known before buildSpawnTexts runs. For a prep-derived
	// session the value is threaded through the later promoteTaskPreparation
	// seed instead of written here, since that call replaces the whole row.
	m.recordPolicy(ctx, id, st.policy, st.requestedPermissions, st.agentConfig.Permissions, launchFacts(st.policy, st.project.Config.Env), m.fenceGapFacts(st.policy, cfg.Harness, st.agentConfig.Permissions))
	st.artifactDir = m.reserveArtifactDir(id)
	if prep == nil {
		st.rec.Metadata.ArtifactDir = st.artifactDir
		if _, err := m.store.UpdateSessionArtifactOutput(ctx, id, st.artifactDir, st.rec.OutputType); err != nil {
			m.cleanupArtifactDir(id)
			m.rollbackSpawnSeedRow(ctx, id)
			return spawnFail(wrapSpawnStage(id, ErrSpawnArtifactDir, err))
		}
	}
	prompt, systemPrompt, err := m.buildSpawnTexts(ctx, *cfg, id)
	if err != nil {
		m.cleanupArtifactDir(id)
		if prep != nil {
			cleanupCtx, cancel := spawnRollbackContext(ctx)
			m.discardClaimedTaskPreparation(cleanupCtx, prep)
			cancel()
		} else {
			m.rollbackSpawnSeedRow(ctx, id)
		}
		return spawnFail(wrapSpawnStage(id, ErrSpawnPrompt, err))
	}
	st.prompt = prompt
	st.systemPrompt = systemPrompt
	st.promptBytes = len(prompt)
	st.systemPromptBytes = len(systemPrompt)
	systemPromptFile, err := m.prepareSystemPromptFile(id, cfg.Harness, systemPrompt)
	if err != nil {
		m.cleanupArtifactDir(id)
		if prep != nil {
			cleanupCtx, cancel := spawnRollbackContext(ctx)
			m.discardClaimedTaskPreparation(cleanupCtx, prep)
			cancel()
		} else {
			m.rollbackSpawnSeedRowAfterFailure(ctx, id)
		}
		return spawnFail(wrapSpawnStage(id, ErrSpawnSystemPrompt, err))
	}
	st.systemPromptFile = systemPromptFile
	return nil
}

// spawnPromotePreparation turns a claimed task preparation into the session row.
// It does nothing for a spawn that created its own seed row.
func (m *Manager) spawnPromotePreparation(ctx context.Context, st *spawnState) *spawnExit {
	prep := st.prep
	if prep == nil {
		return nil
	}
	cfg := &st.cfg
	id := st.id
	// A synchronous spawn can be promoted as ready only after the hidden
	// preparation has finished publishing its worktree. Otherwise its late
	// publication loses the provisioning-state fence and deletes the worktree.
	if !st.asyncChat {
		if _, _, err := m.awaitTaskPreparation(ctx, prep); err != nil && ctx.Err() != nil {
			cleanupCtx, cancel := spawnRollbackContext(ctx)
			m.discardClaimedTaskPreparation(cleanupCtx, prep)
			cancel()
			m.cleanupArtifactDir(id)
			return spawnFail(wrapSpawnStage(id, ErrWorkspaceCreate, err))
		}
	}
	seed := seedRecord(st.policy, *cfg, st.project.Config, m.workflowDefaults(ctx), m.clock())
	seed.ID = id
	seed.Metadata.ArtifactDir = st.artifactDir
	if st.mode == domain.SessionModeChat {
		seed.Metadata.Model = cfg.AgentConfig.Model
		seed.Metadata.Effort = cfg.AgentConfig.Effort
	}
	if st.asyncChat {
		seed.ProvisionState = domain.SessionProvisionProvisioning
	}
	rec, err := m.promoteTaskPreparation(ctx, prep, seed)
	st.rec = rec
	if err != nil {
		cleanupCtx, cancel := spawnRollbackContext(ctx)
		m.discardClaimedTaskPreparation(cleanupCtx, prep)
		cancel()
		m.cleanupArtifactDir(id)
		if cfg.ClientRequestID != "" {
			existing, found, lookupErr := m.store.GetSessionByClientRequestID(ctx, cfg.ClientRequestID)
			if lookupErr == nil && found {
				replay, replayErr := replayClientRequest(existing, cfg.ClientRequestHash)
				return &spawnExit{rec: replay, err: replayErr}
			}
		}
		return spawnFail(wrapSpawnStageEarly(ErrSpawnCreate, err))
	}
	// PromoteTaskPreparation does not write the artifact columns and the
	// full-row UpdateSession leaves them alone, so persist the directory
	// through the dedicated write.
	st.rec.Metadata.ArtifactDir = st.artifactDir
	if _, err := m.store.UpdateSessionArtifactOutput(ctx, id, st.artifactDir, st.rec.OutputType); err != nil {
		m.logger.Warn("persist artifact dir for prepared session", "session", id, "err", err)
	}
	return nil
}

// spawnResolveBranch picks the session branch name.
func (m *Manager) spawnResolveBranch(st *spawnState) {
	cfg := &st.cfg
	branch := cfg.Branch
	if st.prep != nil {
		branch = st.prep.record.Metadata.Branch
	} else if branch == "" {
		branch = DefaultSpawnBranch(st.id, cfg.Kind, sessionPrefix(st.project), st.projectKind, m.dataDir)
		branch = LaneBranch(branch, cfg.Kind, cfg.LaneItemID)
	}
	st.branch = branch
}

// spawnBeginAsync hands an asynchronous Chat spawn to its background half and
// answers the caller; it returns nil for every other spawn.
func (m *Manager) spawnBeginAsync(ctx context.Context, st *spawnState) *spawnExit {
	if !st.asyncChat {
		return nil
	}
	cfg := &st.cfg
	// An asynchronous Chat spawn stops here and answers the caller. Everything
	// below — the remote refresh, the worktree, the controller — is the same
	// work, run in the background while the user already has the session open.
	// The attachments are named now (spawnAttachmentRefs is derived, not read
	// from disk) so the opening prompt is complete before the files land.
	started, promptBytes, systemPromptBytes, err := m.beginAsyncChatSpawn(ctx, asyncChatSpawn{
		policy:            st.policy,
		cfg:               *cfg,
		project:           st.project,
		projectKind:       st.projectKind,
		record:            st.rec,
		branch:            st.branch,
		prompt:            appendAttachmentReferences(st.prompt, spawnAttachmentRefs(cfg.Attachments)),
		systemPrompt:      st.systemPrompt,
		systemPromptFile:  st.systemPromptFile,
		promptBytes:       st.promptBytes,
		systemPromptBytes: st.systemPromptBytes,
		preparation:       st.prep,
		releaseHarness:    st.releaseHarness,
	})
	if err == nil {
		st.releaseHarness = nil // background start now owns the installer guard
		if cfg.ClientRequestID != "" {
			if commitErr := m.store.CommitClientRequestSession(ctx, started.ID); commitErr != nil {
				return spawnFail(wrapSpawnStage(started.ID, ErrSpawnCommit, commitErr))
			}
		}
	}
	return &spawnExit{rec: started, promptBytes: promptBytes, systemPromptBytes: systemPromptBytes, err: err}
}
