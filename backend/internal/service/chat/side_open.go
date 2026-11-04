package chat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (m *sideManager) open(side domain.SideConversation, cfg StartConfig, driver ports.ChatDriver) {
	if side.PolicyVersion != sidePolicyVersion {
		m.failOpen(side, ErrSidePolicyRecreate)
		return
	}
	m.mu.Lock()
	current, lookupErr := m.store.SideConversation(m.ctx, side.ID)
	if lookupErr != nil || current.AppRunID != m.runID || m.restoring[side.ID] != nil {
		m.mu.Unlock()
		return
	}
	if current.State == "failed" {
		_ = m.store.SetSideRecovering(m.ctx, side.ID, side.Generation, m.service.now())
	}
	ctx, cancel := context.WithTimeout(m.ctx, 60*time.Second)
	m.restoring[side.ID] = cancel
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.restoring, side.ID); m.mu.Unlock() }()
	defer cancel()
	attached := false
	var providerID string
	var err error
	var conv ports.ChatConversation
	defer func() {
		if attached {
			return
		}
		if conv != nil {
			_ = conv.Close()
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if m.service.stopProviderHost != nil {
			if err := m.service.stopProviderHost(cleanupCtx, domain.SessionID(side.ProviderHostID)); err != nil {
				m.service.log.Warn("side host cleanup after opening failed", "side", side.ID, "error", err)
				return
			}
		}
		m.store.CompleteSideHostCleanup(cleanupCtx, side.ID)
		m.deleteRegisteredFork(cleanupCtx, side, providerID)
	}()
	failOpen := func(err error) {
		if providerID != "" && !errors.Is(err, ErrSidePolicyRecreate) {
			err = fmt.Errorf("%w: %w", ErrSidePolicyRecreate, err)
		}
		m.failOpen(side, err)
	}
	recordFork := func(recordCtx context.Context, id string) error {
		providerID = id
		side.ProviderForkID = id
		if err := m.writeForkRecord(recordCtx, side, id, false); err != nil {
			return err
		}
		if err := m.store.RegisterSideFork(recordCtx, side.ID, side.Generation, id, m.service.now()); err != nil {
			return err
		}
		m.announce(side.ID)
		return nil
	}
	if side.ContextMode == "native" {
		if forker, ok := driver.(ports.ChatIsolatedForker); ok {
			anchor := side.NativeAnchorID
			forkConfig := ports.ChatStartConfig{
				Policy:    cfg.Policy,
				SessionID: domain.SessionID(side.ProviderHostID), DataDir: cfg.DataDir,
				WorkspacePath: cfg.WorkspacePath, Env: cfg.Env, Model: side.Model, Effort: side.Effort,
				Permissions: cfg.Permissions, ReadOnly: cfg.ReadOnly, SystemPrompt: sideIdentityPrompt(cfg.SystemPrompt),
				ProviderScopeID: side.ID, ProviderIDsScoped: true, RecordFork: recordFork,
				AdditionalDirectories: cfg.AdditionalDirectories, MCPServers: cfg.MCPServers,
			}
			if anchor == "" {
				forkConfig.AcquireForkSource = func(ctx context.Context) (context.Context, func(), error) {
					return m.service.lockCurrentSide(ctx, side)
				}
			}
			conv, err = forker.ForkIntoHost(ctx, side.SourceProviderID, anchor, forkConfig)
			if errors.Is(err, ErrSideAnchorUnavailable) && anchor == "" {
				if side.SeedHistory == "" {
					err = ErrSidePolicyRecreate
				} else {
					err = m.store.SetSideReconstructed(ctx, side.ID, side.Generation, m.service.now())
					if err == nil {
						side.ContextMode = "reconstructed"
					}
				}
			}
			if err == nil {
				if conv != nil {
					providerID = conv.ProviderConversationID()
				}
			}
		} else {
			err = ErrSideProviderUnsupported
		}
	}
	if err != nil {
		failOpen(err)
		return
	}
	if providerID != "" {
		if err := recordFork(ctx, providerID); err != nil {
			failOpen(err)
			return
		}
	}
	if conv == nil && providerID != "" {
		conv, err = driver.Resume(ctx, ports.ChatResumeConfig{
			Policy:    cfg.Policy,
			SessionID: domain.SessionID(side.ProviderHostID), ProviderConversationID: providerID,
			DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, Env: cfg.Env,
			Model: side.Model, Effort: side.Effort, Permissions: cfg.Permissions,
			ReadOnly: cfg.ReadOnly, SystemPrompt: sideIdentityPrompt(cfg.SystemPrompt),
			AdditionalDirectories: cfg.AdditionalDirectories, MCPServers: cfg.MCPServers,
			ProviderScopeID: side.ID, ProviderIDsScoped: true,
		})
	} else if conv == nil {
		conv, err = driver.Start(ctx, ports.ChatStartConfig{
			Policy:    cfg.Policy,
			SessionID: domain.SessionID(side.ProviderHostID), DataDir: cfg.DataDir,
			WorkspacePath: cfg.WorkspacePath, Env: cfg.Env, Model: side.Model, Effort: side.Effort,
			Permissions: cfg.Permissions, ReadOnly: cfg.ReadOnly,
			SystemPrompt: sideIdentityPrompt(cfg.SystemPrompt), ProviderScopeID: side.ID, ProviderIDsScoped: true,
			AdditionalDirectories: cfg.AdditionalDirectories, MCPServers: cfg.MCPServers,
		})
		if err == nil {
			providerID = conv.ProviderConversationID()
			err = m.store.RegisterSideFork(ctx, side.ID, side.Generation, providerID, m.service.now())
		}
	}
	if err != nil {
		failOpen(err)
		return
	}
	side.ProviderForkID = providerID
	if err := m.writeForkRecord(ctx, side, providerID, true); err != nil {
		failOpen(err)
		return
	}
	attached = m.attach(side, conv)
	m.announce(side.ID)
}

func (m *sideManager) failOpen(side domain.SideConversation, err error) {
	m.service.log.Warn("side chat opening failed", "side", side.ID, "error", err)
	if side.ProviderForkID == "" && errors.Is(err, ports.ErrChatRecoveryInconclusive) && !errors.Is(err, ErrSidePolicyRecreate) {
		err = fmt.Errorf("%w: %w", ErrSidePolicyRecreate, err)
	}
	_ = m.store.SetSideFailed(context.Background(), side.ID, side.Generation, err.Error(), m.service.now())
	m.announce(side.ID)
}

func (m *sideManager) ensureRestore(side domain.SideConversation) error {
	if side.PolicyVersion != sidePolicyVersion || side.BoundaryState == "uncertain" {
		return ErrSidePolicyRecreate
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runtimes[side.ID] != nil || m.restoring[side.ID] != nil {
		return nil
	}
	if side.BoundaryState == "pending" {
		return ErrSidePolicyRecreate
	}
	if m.ctx.Err() != nil || side.ProviderForkID == "" {
		return ErrSideNotReady
	}
	if err := m.store.SetSideRecovering(m.ctx, side.ID, side.Generation, m.service.now()); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(m.ctx, 45*time.Second)
	m.restoring[side.ID] = cancel
	go m.restore(ctx, side)
	return nil
}

// RetrySideConnection resumes the registered fork without replacing its context.
func (s *Service) RetrySideConnection(ctx context.Context, session domain.SessionID, sideID string) error {
	if s.sides == nil {
		return ErrSideUnavailable
	}
	side, err := s.sides.ownedSide(ctx, session, sideID)
	if err != nil {
		return err
	}
	if side.RecreateRequired || side.PolicyVersion != sidePolicyVersion || (side.BoundaryState == "uncertain" || side.BoundaryState == "pending") {
		return ErrSidePolicyRecreate
	}
	if side.ProviderForkID == "" {
		cfg, driver, err := s.sideLaunchConfig(side)
		if err != nil {
			return err
		}
		go s.sides.open(side, cfg, driver)
		return nil
	}
	if err := s.sides.ensureRestore(side); err != nil {
		return err
	}
	s.sides.announce(sideID)
	return nil
}

func (m *sideManager) restore(ctx context.Context, side domain.SideConversation) {
	defer func() {
		m.mu.Lock()
		if cancel := m.restoring[side.ID]; cancel != nil {
			cancel()
		}
		delete(m.restoring, side.ID)
		m.mu.Unlock()
	}()
	connected := false
	defer func() {
		if !connected && m.ctx.Err() == nil {
			m.failOpen(side, errors.New("side provider could not resume; retry the connection"))
		}
	}()
	var source *Controller
	var err error
	for ctx.Err() == nil {
		source, err = m.service.Controller(side.SessionID)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
	if source == nil {
		return
	}
	cfg, driver, err := m.service.sideLaunchConfig(side)
	if err != nil {
		m.service.log.Warn("side restore: main controller unavailable", "side", side.ID, "error", err)
		return
	}
	conv, err := driver.Resume(ctx, ports.ChatResumeConfig{
		Policy:    cfg.Policy,
		SessionID: domain.SessionID(side.ProviderHostID), ProviderConversationID: side.ProviderForkID,
		DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, Env: cfg.Env,
		Model: side.Model, Effort: side.Effort, Permissions: cfg.Permissions,
		ReadOnly: cfg.ReadOnly, SystemPrompt: sideIdentityPrompt(cfg.SystemPrompt),
		AdditionalDirectories: cfg.AdditionalDirectories, MCPServers: cfg.MCPServers,
		ProviderScopeID: side.ID, ProviderIDsScoped: true,
	})
	if err != nil {
		m.service.log.Warn("side restore failed", "side", side.ID, "error", err)
		return
	}
	if err := m.reconcileRestoredTurns(ctx, side, conv); err != nil {
		_ = conv.Close()
		m.failOpen(side, err)
		connected = true
		return
	}
	if activator, ok := conv.(ports.ChatLiveReconnectActivator); ok {
		activeProviderID := ""
		turns, _, _ := m.store.SideTurns(ctx, side.ID, time.Time{}, 0)
		for _, turn := range turns {
			if turn.State == "running" {
				activeProviderID = turn.ProviderTurnID
				break
			}
		}
		if err := activator.ActivateLiveReconnect(ctx, activeProviderID); err != nil {
			_ = conv.Close()
			m.service.log.Warn("side provider replay activation failed", "side", side.ID, "error", err)
			_ = m.store.SetSideFailed(context.Background(), side.ID, side.Generation,
				"Side provider could not resume; close and reopen the side chat.", m.service.now())
			m.announce(side.ID)
			return
		}
	}
	connected = m.attach(side, conv)
	if connected {
		m.announce(side.ID)
	}
}

// Reconcile against a fresh native observation; an ID alone is not live ownership.
func (m *sideManager) reconcileRestoredTurns(ctx context.Context, side domain.SideConversation, conv ports.ChatConversation) error {
	turns, _, err := m.store.SideTurns(ctx, side.ID, time.Time{}, 0)
	if err != nil {
		return err
	}
	for _, turn := range turns {
		if turn.State != "running" {
			continue
		}
		// Codex performs a fresh thread/read. ACP ReadHistory is cached replay
		// from session/load and cannot verify the current native outcome.
		if side.Harness != domain.HarnessCodex {
			return errors.New("could not verify the interrupted side turn; close and reopen the side chat; queued messages have not been sent")
		}
		history, ok := conv.(ports.ChatHistoryReader)
		if !ok {
			return errors.New("could not verify the interrupted side turn; close and reopen the side chat; queued messages have not been sent")
		}
		readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		events, readErr := history.ReadHistory(readCtx)
		cancel()
		if readErr != nil {
			return fmt.Errorf("could not verify the interrupted side turn; close and reopen the side chat: %w", readErr)
		}
		var completion *ports.ChatEvent
		for i := range events {
			if events[i].ProviderTurnID == turn.ProviderTurnID && events[i].Kind == ports.ChatEventTurnCompleted {
				completion = &events[i]
			}
		}
		if completion == nil {
			return errors.New("the interrupted side turn has no verified outcome; close and reopen the side chat; queued messages have not been sent")
		}
		runtime := &sideRuntime{side: side, activeTurnID: turn.ID, providerTurnID: turn.ProviderTurnID, messageIDs: map[string]string{}, messageText: map[string]string{}, activityIDs: map[string]string{}, activityText: map[string]string{}}
		messages, err := m.store.SideMessages(ctx, side.ID, []string{turn.ID})
		if err != nil {
			return err
		}
		for _, message := range messages {
			if message.ProviderItemID != "" {
				runtime.messageIDs[message.ProviderItemID] = message.ID
			}
		}
		for _, activity := range m.store.SideActivities(side.ID, []string{turn.ID}) {
			key := activity.ProviderItemID
			if activity.RequestID != "" {
				key = "request:" + activity.RequestID
			}
			if key != "" {
				runtime.activityIDs[key] = activity.ID
			}
		}
		for _, event := range events {
			if event.ProviderTurnID != turn.ProviderTurnID {
				continue
			}
			switch event.Kind {
			case ports.ChatEventMessageCompleted:
				m.projectSideMessage(runtime, event)
			case ports.ChatEventActivityStarted, ports.ChatEventActivityCompleted, ports.ChatEventApprovalResolved, ports.ChatEventInputResolved:
				m.projectSideActivity(runtime, event)
			}
		}
		state, message := "completed", ""
		if completion.TurnState != domain.TurnStateCompleted {
			state = "failed"
			if completion.Err != nil {
				message = completion.Err.Error()
			}
		}
		if err := m.store.SettleSideTurn(ctx, side.ID, turn.ID, side.Generation, state, turn.ProviderTurnID, message, m.service.now()); err != nil {
			return err
		}
		if state != "completed" {
			return errors.New("the interrupted side turn failed; retry the connection before sending queued messages")
		}
	}
	return nil
}
