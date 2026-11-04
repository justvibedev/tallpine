package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// EditMessage forks immediately before one durable human prompt, swaps the live
// controller to that provider branch, then sends the edited text with the exact
// structured content stored for the original prompt.
func (s *Service) EditMessage(
	ctx context.Context,
	id domain.SessionID,
	turnID string,
	msg ports.ChatUserMessage,
) (EditMessageResult, error) {
	requestJSON, err := encodeEditDeliveryRequest(turnID, msg)
	if err != nil {
		return EditMessageResult{}, err
	}
	if res, handled, err := s.replayPriorEdit(ctx, id, msg, requestJSON); handled {
		return res, err
	}
	_, release, err := s.workingController(ctx, id)
	if err != nil {
		return EditMessageResult{}, err
	}
	defer release()
	gate := s.controllerGate(domain.SessionConversationOwner(id))
	if err := gate.lock(ctx); err != nil {
		return EditMessageResult{}, err
	}
	defer gate.unlock()
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return EditMessageResult{}, err
	}
	source, err := s.Controller(id)
	if err != nil {
		return EditMessageResult{}, err
	}
	if res, handled, err := s.reserveEdit(ctx, source, msg, requestJSON); handled {
		return res, err
	}
	run := &editRun{s: s, id: id, source: source, msg: msg, abortSource: true}
	return run.execute(ctx, turnID)
}

// replayPriorEdit answers a retried client message from its stored delivery
// before any controller is touched. handled reports that res/err are final.
func (s *Service) replayPriorEdit(
	ctx context.Context,
	id domain.SessionID,
	msg ports.ChatUserMessage,
	requestJSON string,
) (res EditMessageResult, handled bool, err error) {
	if msg.ClientMessageID == "" {
		return EditMessageResult{}, false, nil
	}
	conversation, loadErr := s.store.ConversationForSession(ctx, id)
	if loadErr != nil {
		return EditMessageResult{}, true, fmt.Errorf("%w: load conversation: %w", ErrEditDeliveryUncertain, loadErr)
	}
	delivery, found, loadErr := s.store.EditDelivery(ctx, conversation.ID, msg.ClientMessageID)
	if loadErr != nil {
		return EditMessageResult{}, true, fmt.Errorf("%w: load prior result: %w", ErrEditDeliveryUncertain, loadErr)
	}
	if !found || !editDeliveryIsSettled(delivery, requestJSON) {
		return EditMessageResult{}, false, nil
	}
	if delivery.RequestJSON == requestJSON && delivery.State == domain.ConversationEditReserved {
		if err := s.store.RecoverCompletedEditDelivery(ctx, conversation.ID, msg.ClientMessageID, s.now()); err != nil {
			return EditMessageResult{}, true, fmt.Errorf("%w: %w", ErrEditDeliveryUncertain, err)
		}
		delivery, _, loadErr = s.store.EditDelivery(ctx, conversation.ID, msg.ClientMessageID)
		if loadErr != nil {
			return EditMessageResult{}, true, fmt.Errorf("%w: %w", ErrEditDeliveryUncertain, loadErr)
		}
	}
	res, err = replayEditDelivery(delivery, requestJSON)
	return res, true, err
}

// editDeliveryIsSettled reports that a stored delivery must be replayed rather
// than re-run: a different request, a finished state, or provider work in flight.
func editDeliveryIsSettled(delivery domain.ConversationEditDelivery, requestJSON string) bool {
	return delivery.RequestJSON != requestJSON ||
		delivery.State != domain.ConversationEditReserved ||
		delivery.ProviderWorkStarted
}

// reserveEdit records the delivery handle under the controller gate. handled
// reports that the stored delivery decides the outcome.
func (s *Service) reserveEdit(
	ctx context.Context,
	source *Controller,
	msg ports.ChatUserMessage,
	requestJSON string,
) (res EditMessageResult, handled bool, err error) {
	if msg.ClientMessageID == "" {
		return EditMessageResult{}, false, nil
	}
	delivery, created, reserveErr := s.store.ReserveEditDelivery(
		ctx, source.conversation.ID, msg.ClientMessageID, requestJSON, s.now())
	if reserveErr != nil {
		return EditMessageResult{}, true, fmt.Errorf("%w: reserve delivery: %w",
			ErrEditDeliveryUncertain, reserveErr)
	}
	if !created && editDeliveryIsSettled(delivery, requestJSON) {
		res, err = replayEditDelivery(delivery, requestJSON)
		return res, true, err
	}
	return EditMessageResult{}, false, nil
}

// editRun carries the state of one EditMessage handoff from the fenced source
// controller to its replacement branch.
type editRun struct {
	s      *Service
	id     domain.SessionID
	source *Controller
	msg    ports.ChatUserMessage
	anchor domain.ConversationEditAnchor

	sourceBranch domain.ConversationBranch
	cfg          StartConfig
	driver       ports.ChatDriver

	provider               ports.ChatConversation
	providerConversationID string
	providerScopeID        string
	replayContent          ports.ChatContent
	replayTruncated        bool

	operationCtx        context.Context
	cancel              context.CancelFunc
	sourceStopInitiated bool
	abortSource         bool
}

func (r *editRun) reject(ctx context.Context, cause error) (EditMessageResult, error) {
	return r.s.rejectEditDelivery(ctx, r.source.conversation.ID, r.msg.ClientMessageID, EditMessageResult{}, cause)
}

func (r *editRun) rejectUndispatched(ctx context.Context, cause error) (EditMessageResult, error) {
	return r.s.rejectUndispatchedEditDelivery(ctx, r.source.conversation.ID, r.msg.ClientMessageID, cause)
}

// restore puts the closed source writer back after a failed edit and joins any
// restore failure to err. Once restored the handoff fence no longer aborts.
func (r *editRun) restore(ctx context.Context, err error) error {
	if !r.sourceStopInitiated {
		return err
	}
	if restoreErr := r.s.restoreClosedSourceController(
		ctx, r.id, r.source, r.sourceBranch, r.cfg, r.driver); restoreErr != nil {
		return errors.Join(err, restoreErr)
	}
	r.abortSource = false
	return err
}

// beginProviderWork marks the delivery as past the point of no silent retry.
func (r *editRun) beginProviderWork(ctx context.Context) error {
	if r.msg.ClientMessageID == "" {
		return nil
	}
	if err := r.s.store.BeginEditProviderWork(
		ctx, r.source.conversation.ID, r.msg.ClientMessageID, r.source.generation); err != nil {
		return fmt.Errorf("%w: %w", ErrEditDeliveryUncertain, err)
	}
	return nil
}

func (r *editRun) execute(ctx context.Context, turnID string) (EditMessageResult, error) {
	if res, handled, err := r.loadAnchor(ctx, turnID); handled {
		return res, err
	}
	canReplay := supportsApproximateReplay(r.source.conv)
	if r.anchor.RetryActiveBranch {
		return r.retryActiveBranch(ctx, canReplay)
	}
	sourceBranch, err := r.s.store.ConversationBranch(ctx, r.source.conversation.ID, r.anchor.SourceBranchID)
	if err != nil {
		return r.reject(ctx, fmt.Errorf("load source conversation branch: %w", err))
	}
	r.sourceBranch = sourceBranch
	forker, canFork := r.source.conv.(ports.ChatForker)
	canNativeFork := r.anchor.PreviousProviderTurnID != "" && canFork
	needsReplay := !canNativeFork && r.anchor.HasPriorContext
	if needsReplay && !canReplay {
		return r.reject(ctx, ErrForkUnsupported)
	}
	if err := r.source.BeginIdleBranchHandoff(ctx); err != nil {
		return r.reject(ctx, err)
	}
	defer func() {
		if r.cancel != nil {
			r.cancel()
		}
		if r.abortSource {
			r.source.AbortHandoff()
		}
	}()
	if r.cfg, r.driver, err = r.s.branchLaunchConfig(r.source); err != nil {
		return r.reject(ctx, err)
	}
	r.operationCtx = ctx
	if err := r.beginProviderWork(ctx); err != nil {
		return EditMessageResult{}, err
	}
	if canNativeFork {
		err = r.forkProvider(ctx, forker)
	} else {
		if res, handled, prepErr := r.prepareFreshReplay(ctx, needsReplay); handled {
			return res, prepErr
		}
		err = r.startFreshProvider(ctx)
	}
	if res, handled, failErr := r.checkProvider(ctx, err); handled {
		return res, failErr
	}
	return r.activateAndSend(ctx)
}

// loadAnchor loads the edit anchor and the stored prompt content into r.msg.
func (r *editRun) loadAnchor(ctx context.Context, turnID string) (EditMessageResult, bool, error) {
	anchor, err := r.s.store.ConversationEditAnchor(ctx, r.source.conversation.ID, turnID)
	if err != nil {
		if !errors.Is(err, domain.ErrNoConversationTurn) {
			return EditMessageResult{}, true, fmt.Errorf("%w: load edit anchor: %w", ErrEditDeliveryUncertain, err)
		}
		res, rejErr := r.reject(ctx, fmt.Errorf("%w: %w", ErrEditTurnInvalid, err))
		return res, true, rejErr
	}
	r.anchor = anchor
	var content []ports.ChatContent
	if anchor.OriginalDeliveryContentJSON != "" {
		if err := json.Unmarshal([]byte(anchor.OriginalDeliveryContentJSON), &content); err != nil {
			res, rejErr := r.reject(ctx,
				fmt.Errorf("%w: decode stored prompt content: %w", ErrEditTurnInvalid, err))
			return res, true, rejErr
		}
	}
	r.msg.Content = withoutInternalReplayContent(content)
	return EditMessageResult{}, false, nil
}

// retryActiveBranch resends into the branch an earlier attempt already created.
func (r *editRun) retryActiveBranch(ctx context.Context, canReplay bool) (EditMessageResult, error) {
	if err := r.beginProviderWork(ctx); err != nil {
		return EditMessageResult{}, err
	}
	branch, err := r.s.store.ConversationBranch(ctx, r.source.conversation.ID, r.anchor.SourceBranchID)
	if err != nil {
		return r.reject(ctx, fmt.Errorf("load pending edited conversation: %w", err))
	}
	if domain.NormalizeConversationBranchStrategy(branch.Strategy) == domain.ConversationBranchStrategyApproximateContext {
		if !canReplay {
			return r.rejectUndispatched(ctx, ErrForkUnsupported)
		}
		replay, _, replayErr := r.s.approximateReplayContent(
			ctx, r.source.conversation.ID, r.anchor.ReplayFloorSequence, branch.ReplayCutoffSequence)
		if replayErr != nil {
			return r.rejectUndispatched(ctx, fmt.Errorf("prepare edited conversation retry: %w", replayErr))
		}
		r.msg.Content = append([]ports.ChatContent{replay}, r.msg.Content...)
	}
	return r.s.sendEditedMessage(ctx, branch.ParentBranchID, branch.ID, r.source, r.msg, true)
}

// detach lets the irreversible source-writer handoff finish or recover even if
// the request is cancelled.
func (r *editRun) detach(ctx context.Context) {
	r.operationCtx, r.cancel = context.WithTimeout(context.WithoutCancel(ctx), nativeEditHandoffLimit)
	r.sourceStopInitiated = true
}

func (r *editRun) forkProvider(ctx context.Context, forker ports.ChatForker) error {
	forkAnchor := r.anchor.PreviousProviderTurnID
	providerConversationID, err := forker.Fork(ctx, &forkAnchor)
	r.providerConversationID = providerConversationID
	if err != nil {
		return err
	}
	// Fork succeeded, so closing the source writer is now irreversible.
	// Finish or recover this boundary independently of request cancellation.
	r.detach(ctx)
	// Codex loads a fork into the source app-server, which remains that
	// child's active writer until the process exits. Close the fenced, idle
	// source before another driver process resumes the child.
	if closeErr := r.source.closeForBranchHandoff(r.operationCtx); closeErr != nil {
		return fmt.Errorf("close source writer after fork: %w", closeErr)
	}
	cfg := r.cfg
	launchEnv, err := r.s.prepareBranchControllerEnv(r.operationCtx, cfg)
	if err != nil {
		return err
	}
	r.provider, err = r.driver.Resume(r.operationCtx, ports.ChatResumeConfig{
		Policy:    cfg.Policy,
		SessionID: cfg.SessionID, ProviderConversationID: providerConversationID,
		DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, Env: launchEnv,
		Model: cfg.Model, Effort: cfg.Effort,
		Permissions: cfg.Permissions, SystemPrompt: withHandoffNote(cfg.SystemPrompt, r.sourceBranch.HandoffNote),
		ProviderScopeID:       r.sourceBranch.ProviderScopeID,
		ProviderIDsScoped:     r.sourceBranch.ProviderIDsScoped,
		AdditionalDirectories: cfg.AdditionalDirectories, MCPServers: cfg.MCPServers,
	})
	return err
}

// prepareFreshReplay allocates the provider namespace for a fresh Start and, when
// prior context cannot be forked, builds the approximate replay prompt.
func (r *editRun) prepareFreshReplay(ctx context.Context, needsReplay bool) (EditMessageResult, bool, error) {
	// Every Start owns a fresh namespace for provider-issued turn/item IDs,
	// even when editing the first prompt requires no replay payload.
	r.providerScopeID = r.s.newID()
	if !needsReplay {
		return EditMessageResult{}, false, nil
	}
	var replayErr error
	r.replayContent, r.replayTruncated, replayErr = r.s.approximateReplayContent(
		ctx, r.source.conversation.ID, r.anchor.ReplayFloorSequence, r.anchor.ForkAfterSequence)
	if replayErr != nil {
		res, err := r.rejectUndispatched(ctx, fmt.Errorf("prepare edited conversation: %w", replayErr))
		return res, true, err
	}
	return EditMessageResult{}, false, nil
}

func (r *editRun) startFreshProvider(ctx context.Context) error {
	// A fresh replay process is also a controller replacement. Stop the old
	// bearer holder before rotating its verifier and launching the child.
	r.detach(ctx)
	if closeErr := r.source.closeForBranchHandoff(r.operationCtx); closeErr != nil {
		return fmt.Errorf("close source writer before edited conversation: %w", closeErr)
	}
	cfg := r.cfg
	launchEnv, err := r.s.prepareBranchControllerEnv(r.operationCtx, cfg)
	if err != nil {
		return err
	}
	r.provider, err = r.driver.Start(r.operationCtx, ports.ChatStartConfig{
		Policy:    cfg.Policy,
		SessionID: cfg.SessionID, DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath,
		Env: launchEnv, Model: cfg.Model, Effort: cfg.Effort,
		Permissions:  cfg.Permissions,
		SystemPrompt: withHandoffNote(cfg.SystemPrompt, r.sourceBranch.HandoffNote), AdditionalDirectories: cfg.AdditionalDirectories,
		MCPServers: cfg.MCPServers, ProviderScopeID: r.providerScopeID, ProviderIDsScoped: true,
	})
	if err == nil {
		r.providerConversationID = r.provider.ProviderConversationID()
	}
	return err
}

// checkProvider turns a failed or unusable provider launch into a rejected
// delivery, restoring the source writer when it was already closed.
func (r *editRun) checkProvider(ctx context.Context, err error) (EditMessageResult, bool, error) {
	if err != nil {
		res, rejErr := r.reject(ctx,
			r.restore(ctx, classify(fmt.Errorf("prepare edited conversation: %w", err))))
		return res, true, rejErr
	}
	if r.provider == nil || r.providerConversationID == "" {
		if r.provider != nil {
			_ = r.provider.Close()
		}
		res, rejErr := r.reject(ctx,
			r.restore(ctx, errors.New("replacement provider conversation is not ready")))
		return res, true, rejErr
	}
	if r.replayContent.Type != "" && !supportsApproximateReplay(r.provider) {
		_ = r.provider.Close()
		res, rejErr := r.rejectUndispatched(ctx, r.restore(ctx, ErrForkUnsupported))
		return res, true, rejErr
	}
	return EditMessageResult{}, false, nil
}

// newBranch describes the edited branch hanging off the source branch.
func (r *editRun) newBranch(branchID string) domain.ConversationBranch {
	branch := domain.ConversationBranch{
		ID: branchID, ConversationID: r.source.conversation.ID, SessionID: r.id,
		ProviderConversationID: r.providerConversationID, ParentBranchID: r.anchor.SourceBranchID,
		ReplacedTurnID: r.anchor.ReplacedTurnID, ForkAfterSequence: r.anchor.ForkAfterSequence,
		CreatedAt: r.s.now(), Strategy: domain.ConversationBranchStrategyNative,
		ProviderScopeID:   r.providerScopeID,
		ProviderIDsScoped: r.providerScopeID != "" || r.sourceBranch.ProviderIDsScoped,
		// An edit after a switch is still on the agent the switch brought in.
		HandoffNote: r.sourceBranch.HandoffNote,
	}
	if r.replayContent.Type != "" {
		branch.Strategy = domain.ConversationBranchStrategyApproximateContext
		branch.ReplayCutoffSequence = r.anchor.ForkAfterSequence
		branch.ReplayTruncated = r.replayTruncated
	}
	return branch
}

// activateAndSend makes the new branch durable and sends the edited prompt
// through its replacement controller, rolling back to the source on rejection.
func (r *editRun) activateAndSend(ctx context.Context) (EditMessageResult, error) {
	s, id, source := r.s, r.id, r.source
	branchID := s.newID()
	generation := s.newID()
	branch := r.newBranch(branchID)
	conversation := source.conversation
	conversation.ActiveBranchID = branchID
	replacement := s.newReplacementController(id, source, conversation, generation, r.provider)
	if err := s.store.CreateAndActivateConversationBranch(
		r.operationCtx, id, branch, generation, s.now(),
	); err != nil {
		_ = r.provider.Close()
		return r.reject(ctx,
			r.restore(ctx, fmt.Errorf("activate edited conversation: %w", err)))
	}
	// Consume the replacement privately while its bootstrap prompt is recorded.
	// The source remains the registry owner with its intake fence installed, so a
	// concurrent ordinary send cannot become the first turn on the new branch.
	replacement.start()
	if r.replayContent.Type != "" {
		r.msg.Content = append([]ports.ChatContent{r.replayContent}, r.msg.Content...)
	}
	result, sendErr := s.sendEditedMessage(
		r.operationCtx, r.anchor.SourceBranchID, branchID, replacement, r.msg, false)
	if result.Turn.ID == "" || errors.Is(sendErr, ErrProviderRefused) {
		return r.rollbackRejectedEdit(ctx, replacement, result, sendErr)
	}
	if err := s.installStartedBranchController(
		r.operationCtx, id, source, replacement, r.anchor.SourceBranchID); err != nil {
		return result, errors.Join(sendErr, err)
	}
	r.abortSource = false
	return result, sendErr
}

// rollbackRejectedEdit reactivates the source branch after the replacement
// refused or never recorded the edited prompt.
func (r *editRun) rollbackRejectedEdit(
	ctx context.Context,
	replacement *Controller,
	result EditMessageResult,
	sendErr error,
) (EditMessageResult, error) {
	s, source := r.s, r.source
	if err := s.store.ActivateConversationBranch(r.operationCtx, r.id, source.conversation.ID,
		r.anchor.SourceBranchID, source.ProviderConversationID(), source.generation, s.now()); err != nil {
		sendErr = errors.Join(sendErr, fmt.Errorf("restore source after rejected edit: %w", err))
		// The durable head still belongs to the child. Publish its controller
		// rather than reopening a source generation the store just rejected.
		if installErr := s.installStartedBranchController(
			r.operationCtx, r.id, source, replacement, r.anchor.SourceBranchID); installErr != nil {
			return result, errors.Join(sendErr, installErr)
		}
		r.abortSource = false
		return result, sendErr
	}
	if err := replacement.Close(r.operationCtx); err != nil {
		sendErr = errors.Join(sendErr, fmt.Errorf("close rejected edited conversation: %w", err))
	}
	return result, r.restore(ctx, sendErr)
}
