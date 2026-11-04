package chat

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// ActivateBranch resumes a durable provider branch in the same worktree and
// swaps controllers without sending a new prompt.
func (s *Service) ActivateBranch(ctx context.Context, id domain.SessionID, branchID string) (string, error) {
	_, release, err := s.workingController(ctx, id)
	if err != nil {
		return "", err
	}
	defer release()
	gate := s.controllerGate(domain.SessionConversationOwner(id))
	if err := gate.lock(ctx); err != nil {
		return "", err
	}
	defer gate.unlock()
	return s.activateBranchLocked(ctx, id, branchID)
}

func (s *Service) activateBranchLocked(ctx context.Context, id domain.SessionID, branchID string) (string, error) {
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return "", err
	}
	source, err := s.Controller(id)
	if err != nil {
		return "", err
	}
	branch, err := s.store.ConversationBranch(ctx, source.conversation.ID, branchID)
	if err != nil {
		return "", err
	}
	activeBranch, err := s.store.ConversationBranch(
		ctx, source.conversation.ID, source.conversation.ActiveBranchID)
	if err != nil {
		return "", fmt.Errorf("load active conversation branch: %w", err)
	}
	if branch.Active {
		return branch.ID, nil
	}
	cfg, driver, err := s.branchLaunchConfig(source)
	if err != nil {
		return "", err
	}
	if branch.ProviderBindingID != activeBranch.ProviderBindingID {
		// Provider scopes may differ between approximate siblings, but their
		// durable binding epoch must match. Crossing it could hand an opaque
		// conversation handle to a different adapter/configuration owner.
		return "", ErrBranchProviderMismatch
	}
	if err := source.BeginIdleBranchHandoff(ctx); err != nil {
		return "", err
	}
	abortSource := true
	defer func() {
		if abortSource {
			source.AbortHandoff()
		}
	}()
	operationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), nativeEditHandoffLimit)
	defer cancel()
	if err := source.closeForBranchHandoff(operationCtx); err != nil {
		closeErr := fmt.Errorf("close source before branch activation: %w", err)
		if source.State() == ports.ChatControllerStopped {
			if restoreErr := s.restoreClosedSourceController(
				operationCtx, id, source, activeBranch, cfg, driver); restoreErr != nil {
				closeErr = errors.Join(closeErr, restoreErr)
			} else {
				abortSource = false
			}
		}
		return "", closeErr
	}
	launchEnv, err := s.prepareBranchControllerEnv(operationCtx, cfg)
	if err != nil {
		if restoreErr := s.restoreClosedSourceController(
			operationCtx, id, source, activeBranch, cfg, driver); restoreErr != nil {
			err = errors.Join(err, restoreErr)
		} else {
			abortSource = false
		}
		return "", err
	}
	provider, err := driver.Resume(operationCtx, ports.ChatResumeConfig{
		Policy:    cfg.Policy,
		SessionID: cfg.SessionID, ProviderConversationID: branch.ProviderConversationID,
		DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, Env: launchEnv,
		Model: cfg.Model, Effort: cfg.Effort,
		Permissions: cfg.Permissions, SystemPrompt: withHandoffNote(cfg.SystemPrompt, branch.HandoffNote),
		ProviderScopeID:       branch.ProviderScopeID,
		ProviderIDsScoped:     branch.ProviderIDsScoped,
		AdditionalDirectories: cfg.AdditionalDirectories, MCPServers: cfg.MCPServers,
	})
	if err != nil {
		resumeErr := fmt.Errorf("resume conversation branch %s: %w", branchID, err)
		if restoreErr := s.restoreClosedSourceController(
			operationCtx, id, source, activeBranch, cfg, driver); restoreErr != nil {
			resumeErr = errors.Join(resumeErr, restoreErr)
		} else {
			abortSource = false
		}
		return "", resumeErr
	}
	generation := s.newID()
	conversation := source.conversation
	conversation.ActiveBranchID = branch.ID
	replacement := s.newReplacementController(id, source, conversation, generation, provider)
	if err := s.store.ActivateConversationBranch(operationCtx, id, conversation.ID, branch.ID,
		branch.ProviderConversationID, generation, s.now()); err != nil {
		_ = cleanupUnpublishedConversation(provider, true)
		activateErr := err
		if restoreErr := s.restoreClosedSourceController(
			operationCtx, id, source, activeBranch, cfg, driver); restoreErr != nil {
			activateErr = errors.Join(activateErr, restoreErr)
		} else {
			abortSource = false
		}
		return "", activateErr
	}
	if err := s.installBranchController(operationCtx, id, source, replacement, source.conversation.ActiveBranchID); err != nil {
		return "", err
	}
	abortSource = false
	return branch.ID, nil
}

func (s *Service) branchLaunchConfig(
	source *Controller,
) (StartConfig, ports.ChatDriver, error) {
	s.mu.RLock()
	cfg, ok := s.startConfigs[source.owner()]
	// Controllers are registered by typed owner. Session history operations still
	// address the worker by session id, so use the source's owner here rather
	// than treating the legacy session index as the authority. In particular, a
	// branch replacement must update the typed entry before the old controller's
	// cleanup goroutine runs, otherwise that goroutine can remove the new
	// session controller.
	current := s.ownerControllers[source.owner()]
	s.mu.RUnlock()
	if !ok || current != source {
		return StartConfig{}, nil, ErrControllerHandoff
	}
	driver, err := s.drivers.Driver(cfg.Harness)
	if err != nil {
		return StartConfig{}, nil, fmt.Errorf("chat driver for %s: %w", cfg.Harness, err)
	}
	if err := admitChatFence(driver, cfg.Harness, cfg.Policy, cfg.Permissions); err != nil {
		return StartConfig{}, nil, err
	}
	return cloneStartConfig(cfg), driver, nil
}

func (s *Service) prepareBranchControllerEnv(ctx context.Context, cfg StartConfig) (map[string]string, error) {
	if cfg.PrepareControllerEnv == nil {
		return cloneStartConfig(cfg).Env, nil
	}
	env, err := cfg.PrepareControllerEnv(ctx, cfg.ExpectedControllerOwner, cfg.Harness)
	if err != nil {
		return nil, fmt.Errorf("prepare replacement controller environment: %w", err)
	}
	return env, nil
}

// restoreClosedSourceController reopens the original provider branch when a
// native edit had to release its writer but the replacement could not be kept.
// The new generation fences any late events from the process that was closed.
func (s *Service) restoreClosedSourceController(
	ctx context.Context,
	id domain.SessionID,
	source *Controller,
	branch domain.ConversationBranch,
	cfg StartConfig,
	driver ports.ChatDriver,
) (returnErr error) {
	defer func() {
		if returnErr != nil {
			source.reportFailedBranchHandoff(ctx)
		}
	}()
	recoveryCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx), nativeEditHandoffLimit)
	defer cancel()
	// A closed event stream can project "stopped" before host termination
	// succeeds. Preserve any termination error before rotating its credentials.
	if err := source.closeForBranchHandoff(recoveryCtx); err != nil {
		return fmt.Errorf("confirm source stopped before failed native edit recovery: %w", err)
	}
	providerConversationID := source.ProviderConversationID()
	launchEnv, err := s.prepareBranchControllerEnv(recoveryCtx, cfg)
	if err != nil {
		return fmt.Errorf("rotate source browser capability after failed native edit: %w", err)
	}
	// The source comes back with the settings it had when it stopped, not the
	// ones it was launched with: the user may have changed them since.
	conversation := source.conversation
	conversation.Settings = source.Settings()
	provider, err := driver.Resume(recoveryCtx, ports.ChatResumeConfig{
		Policy:    cfg.Policy,
		SessionID: cfg.SessionID, ProviderConversationID: providerConversationID,
		// A switch target that never answered may have no native session to resume.
		FreshIfMissing: s.switchBranchUnanswered(recoveryCtx, conversation.ID, branch),
		DataDir:        cfg.DataDir, WorkspacePath: cfg.WorkspacePath, Env: launchEnv,
		Model: cmp.Or(conversation.Settings.Model, cfg.Model), Effort: conversation.Settings.ReasoningEffort,
		Permissions:           cmp.Or(conversation.Settings.ApprovalMode, cfg.Permissions),
		SystemPrompt:          withHandoffNote(cfg.SystemPrompt, branch.HandoffNote),
		ProviderScopeID:       branch.ProviderScopeID,
		ProviderIDsScoped:     branch.ProviderIDsScoped,
		AdditionalDirectories: cfg.AdditionalDirectories, MCPServers: cfg.MCPServers,
	})
	if err != nil {
		return fmt.Errorf("resume source after failed native edit: %w", err)
	}
	// A fresh start in place of the missing session has its own id, and the
	// branch must name it before it can be activated.
	if fresh := provider.ProviderConversationID(); fresh != "" && fresh != providerConversationID {
		if rebinder, ok := s.store.(branchProviderRebinder); ok {
			if err := rebinder.RebindUnansweredBranchProvider(recoveryCtx, conversation.ID, branch.ID, providerConversationID, fresh); err != nil {
				_ = provider.Close()
				return fmt.Errorf("rebind source after failed native edit: %w", err)
			}
		}
		providerConversationID = fresh
	}
	generation := s.newID()
	conversation.ActiveBranchID = branch.ID
	replacement := s.newReplacementController(id, source, conversation, generation, provider)
	if err := s.store.ActivateConversationBranch(recoveryCtx, id, conversation.ID, branch.ID,
		providerConversationID, generation, s.now()); err != nil {
		_ = provider.Close()
		return fmt.Errorf("reactivate source after failed native edit: %w", err)
	}
	if err := s.installBranchController(recoveryCtx, id, source, replacement, branch.ID); err != nil {
		return fmt.Errorf("install source after failed native edit: %w", err)
	}
	return nil
}

func (s *Service) installBranchController(
	ctx context.Context,
	id domain.SessionID,
	source, replacement *Controller,
	sourceBranchID string,
) error {
	replacement.start()
	return s.installStartedBranchController(ctx, id, source, replacement, sourceBranchID)
}

// installStartedBranchController publishes a controller whose provider event
// stream is already being consumed. Edit bootstraps use this after recording the
// replacement prompt privately; ordinary branch activation starts and delegates
// through installBranchController above.
func (s *Service) installStartedBranchController(
	ctx context.Context,
	id domain.SessionID,
	source, replacement *Controller,
	sourceBranchID string,
) error {
	owner := source.owner()
	s.mu.Lock()
	if s.ownerControllers[owner] != source {
		s.mu.Unlock()
		_ = replacement.Terminate(ctx)
		if err := s.store.ActivateConversationBranch(ctx, id, source.conversation.ID,
			sourceBranchID, source.ProviderConversationID(), source.generation, s.now()); err != nil {
			return fmt.Errorf("restore source branch after controller swap conflict: %w", err)
		}
		return ErrControllerHandoff
	}
	source.prepareBranchHandoffStop()
	s.ownerControllers[owner] = replacement
	// Keep the session index as the backwards-compatible lookup path used by
	// worker chat commands. Review owners never share this index, so replacing a
	// worker controller cannot affect a reviewer controller for the same session.
	if owner.Kind == domain.ConversationOwnerSession {
		s.controllers[id] = replacement
	}
	if cfg, ok := s.startConfigs[owner]; ok {
		// Mirror the owner ActivateConversationBranchSession just made durable,
		// agent-session identity included. A stale field here fails the next
		// credential rotation's ownership fence, so a second edit or activation
		// would report an uncertain delivery and stop the session.
		cfg.ExpectedControllerOwner.Harness = cfg.Harness
		cfg.ExpectedControllerOwner.Mode = domain.SessionModeChat
		cfg.ExpectedControllerOwner.IsTerminated = false
		cfg.ExpectedControllerOwner.RuntimeLaunchID = ""
		cfg.ExpectedControllerOwner.AgentSessionID = replacement.ProviderConversationID()
		cfg.ExpectedControllerOwner.AgentSessionIDLaunchID = ""
		cfg.ExpectedControllerOwner.ProviderConversationID = replacement.ProviderConversationID()
		cfg.ExpectedControllerOwner.ControllerGeneration = replacement.Generation()
		s.startConfigs[owner] = cfg
	}
	s.mu.Unlock()

	go func() {
		replacement.Wait()
		replacement.waitForBranchHandoff()
		s.mu.Lock()
		if current := s.ownerControllers[owner]; current == replacement {
			delete(s.ownerControllers, owner)
			if owner.Kind == domain.ConversationOwnerSession {
				delete(s.controllers, id)
			}
		}
		s.mu.Unlock()
	}()
	source.completeBranchHandoff()
	if err := source.closeForBranchHandoff(ctx); err != nil {
		s.log.Error("close source controller after branch swap", "session", id, "error", err)
	}
	return nil
}
