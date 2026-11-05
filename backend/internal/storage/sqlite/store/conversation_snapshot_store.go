package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// ConversationSnapshot is the durable read model for one conversation.
type ConversationSnapshot struct {
	Conversation                     domain.ConversationRecord
	ActiveBranch                     domain.ConversationBranch
	EditFloorSequence                int64
	NativeForkAvailableAfterSequence int64
	Turns                            []domain.ConversationTurn
	Messages                         []domain.ConversationMessage
	Activities                       []domain.ConversationActivity
	BranchPoints                     []domain.ConversationBranchPoint
	BranchedFromEarlierMessage       bool
	OldestSequence                   int64
	HasMoreBefore                    bool
}

// DefaultConversationPageSize is the standard bounded read size for conversation snapshots.
const DefaultConversationPageSize = 200

// conversationSnapshotTxKey carries the read transaction a snapshot's queries
// share; see beginSnapshotRead.
type conversationSnapshotTxKey struct{}

// beginSnapshotRead opens the one read transaction a conversation snapshot's
// queries share. Without it a projection that commits between two of them tears
// the snapshot, such as activities naming a turn that the turns query ran too
// early to see. The returned end func may be called more than once.
func (s *Store) beginSnapshotRead(ctx context.Context) (context.Context, func(), error) {
	tx, err := s.readDB.BeginTx(ctx, nil)
	if err != nil {
		return ctx, func() {}, fmt.Errorf("begin conversation snapshot read: %w", err)
	}
	return context.WithValue(ctx, conversationSnapshotTxKey{}, s.qr.WithTx(tx)), func() { _ = tx.Rollback() }, nil
}

// snapshotReader reads through the snapshot's shared transaction when ctx
// carries one, and through the reader pool otherwise.
func (s *Store) snapshotReader(ctx context.Context) *gen.Queries {
	if q, ok := ctx.Value(conversationSnapshotTxKey{}).(*gen.Queries); ok {
		return q
	}
	return s.qr
}

// LoadConversationSnapshotPage reads a bounded slice ending immediately before
// beforeSequence. Zero starts at the live edge. Messages and activities share one
// sequence, so the store merges their bounded reads and trims them as one page;
// a noisy command cannot crowd prose out by making either table unbounded.
func (s *Store) LoadConversationSnapshotPage(
	ctx context.Context,
	conversationID string,
	beforeSequence int64,
	limit int64,
) (ConversationSnapshot, error) {
	parent := ctx
	ctx, endRead, err := s.beginSnapshotRead(parent)
	if err != nil {
		return ConversationSnapshot{}, err
	}
	defer endRead()
	conv, err := s.snapshotReader(ctx).SelectConversationByID(ctx, conversationID)
	if errors.Is(err, sql.ErrNoRows) {
		return ConversationSnapshot{}, ErrConversationNotFound
	}
	if err != nil {
		return ConversationSnapshot{}, fmt.Errorf("select conversation %s: %w", conversationID, err)
	}

	// Targeted lazy reconciliation of legacy demands, using durable provider
	// evidence. Ordinary snapshots remain read-only and avoid the writer lock.
	if account := conversationToDomain(conv).Account; account != nil && account.ReauthRequiredAt != nil {
		// Reconcile outside the read transaction: it writes, and it reads from
		// the pool that transaction holds a connection of.
		endRead()
		if _, reconcileErr := s.ReconcileConversationAuthentication(parent, conversationID, "", "", time.Now()); reconcileErr != nil {
			return ConversationSnapshot{}, reconcileErr
		}
		ctx, endRead, err = s.beginSnapshotRead(parent)
		if err != nil {
			return ConversationSnapshot{}, err
		}
		defer endRead()
		conv, err = s.snapshotReader(ctx).SelectConversationByID(ctx, conversationID)
		if err != nil {
			return ConversationSnapshot{}, err
		}
	}
	if limit <= 0 {
		limit = DefaultConversationPageSize
	}
	if limit > 500 {
		limit = 500
	}
	if beforeSequence <= 0 || beforeSequence > conv.LatestSequence+1 {
		beforeSequence = conv.LatestSequence + 1
	}
	visibleAfterSequence, err := s.conversationVisibleAfterSequence(ctx, conv)
	if err != nil {
		return ConversationSnapshot{}, err
	}
	presentation := s.conversationHistoryPresentation(ctx, conversationID)
	if beforeSequence <= visibleAfterSequence {
		snapshot := ConversationSnapshot{
			Conversation:                     conversationToDomain(conv),
			ActiveBranch:                     presentation.activeBranch,
			EditFloorSequence:                presentation.editFloorSequence,
			NativeForkAvailableAfterSequence: presentation.nativeForkAvailableAfterSequence,
			BranchPoints:                     presentation.branchPoints,
			BranchedFromEarlierMessage:       presentation.branchedFromEarlierMessage,
			OldestSequence:                   visibleAfterSequence,
			HasMoreBefore:                    false,
		}
		return snapshot, nil
	}
	messageRows, activityRows, oldest, hasMore, err := s.loadSnapshotPageWindow(ctx, conversationID, beforeSequence, visibleAfterSequence, limit)
	if err != nil {
		return ConversationSnapshot{}, err
	}

	turnRows, err := s.snapshotReader(ctx).SelectConversationTurnsPage(ctx, gen.SelectConversationTurnsPageParams{
		ConversationID: conversationID,
		OldestSequence: oldest,
		BeforeSequence: beforeSequence,
	})
	if err != nil {
		return ConversationSnapshot{}, fmt.Errorf("select turn page: %w", err)
	}
	retriedSources, err := s.conversationRetriedSources(ctx, conversationID)
	if err != nil {
		return ConversationSnapshot{}, err
	}
	humanPromptTurns, err := s.conversationHumanPromptTurns(ctx, conversationID)
	if err != nil {
		return ConversationSnapshot{}, err
	}

	snapshot := ConversationSnapshot{
		Conversation:                     conversationToDomain(conv),
		ActiveBranch:                     presentation.activeBranch,
		EditFloorSequence:                presentation.editFloorSequence,
		NativeForkAvailableAfterSequence: presentation.nativeForkAvailableAfterSequence,
		BranchPoints:                     presentation.branchPoints,
		BranchedFromEarlierMessage:       presentation.branchedFromEarlierMessage,
		OldestSequence:                   oldest,
		HasMoreBefore:                    hasMore,
	}
	for _, row := range turnRows {
		if !conversationTurnVisibleAfterSequence(row, conv, visibleAfterSequence) {
			continue
		}
		turn := turnToDomain(row)
		turn.HasRetryAttempt = retriedSources[turn.ID]
		turn.HasHumanPrompt = humanPromptTurns[turn.ID]
		presentation.filterInactiveProviderTurn(&turn)
		snapshot.Turns = append(snapshot.Turns, turn)
	}
	// SQL returns newest-first so LIMIT is useful; the API contract remains
	// oldest-first inside each page.
	for i := len(messageRows) - 1; i >= 0; i-- {
		if messageRows[i].Sequence >= oldest && messageRows[i].Sequence > visibleAfterSequence {
			snapshot.Messages = append(snapshot.Messages, messageToDomain(messageRows[i]))
		}
	}
	for i := len(activityRows) - 1; i >= 0; i-- {
		if activityRows[i].Sequence >= oldest && activityRows[i].Sequence > visibleAfterSequence {
			snapshot.Activities = append(snapshot.Activities, activityToDomain(activityRows[i]))
		}
	}
	return snapshot, nil
}

func (s *Store) conversationVisibleAfterSequence(
	ctx context.Context,
	conv gen.Conversation,
) (int64, error) {
	if conv.Scope != domain.ConversationScopeProject || conv.CurrentSessionID == nil || *conv.CurrentSessionID == "" {
		return 0, nil
	}
	sequence, err := s.snapshotReader(ctx).SelectConversationContextResetSequence(ctx, gen.SelectConversationContextResetSequenceParams{
		ConversationID: conv.ID,
		ProviderItemID: domain.ConversationContextResetProviderItemID(*conv.CurrentSessionID),
	})
	if err != nil {
		return 0, fmt.Errorf("select context reset boundary for conversation %s: %w", conv.ID, err)
	}
	if sequence == 0 {
		pending, pendingErr := s.projectConversationResetPending(ctx, conv)
		if pendingErr != nil {
			return 0, pendingErr
		}
		if pending {
			return conv.LatestSequence, nil
		}
	}
	return sequence, nil
}

func (s *Store) projectConversationResetPending(ctx context.Context, conv gen.Conversation) (bool, error) {
	if conv.Scope != domain.ConversationScopeProject ||
		conv.CurrentSessionID == nil ||
		*conv.CurrentSessionID == "" ||
		conv.LatestSequence <= 0 {
		return false, nil
	}
	current, err := s.snapshotReader(ctx).GetSession(ctx, *conv.CurrentSessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("select current session %s: %w", *conv.CurrentSessionID, err)
	}
	if current.ProviderConversationID != "" {
		return false, nil
	}
	active, err := s.snapshotReader(ctx).SelectConversationBranch(ctx, gen.SelectConversationBranchParams{
		ConversationID: conv.ID,
		BranchID:       conv.ActiveBranchID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("select active branch %s: %w", conv.ActiveBranchID, err)
	}
	return active.SessionID.Valid && active.SessionID.String != string(*conv.CurrentSessionID), nil
}

func conversationTurnVisibleAfterSequence(row gen.ConversationTurn, conv gen.Conversation, visibleAfterSequence int64) bool {
	if visibleAfterSequence <= 0 || conv.CurrentSessionID == nil {
		return true
	}
	return row.HandledBySessionID == *conv.CurrentSessionID
}

// LoadConversationSnapshot reads a whole conversation. Items come back ordered by
// sequence so the caller never has to sort.
func (s *Store) LoadConversationSnapshot(
	ctx context.Context,
	conversationID string,
) (ConversationSnapshot, error) {
	parent := ctx
	ctx, endRead, err := s.beginSnapshotRead(parent)
	if err != nil {
		return ConversationSnapshot{}, err
	}
	defer endRead()
	conv, err := s.snapshotReader(ctx).SelectConversationByID(ctx, conversationID)
	if errors.Is(err, sql.ErrNoRows) {
		return ConversationSnapshot{}, ErrConversationNotFound
	}
	if err != nil {
		return ConversationSnapshot{}, fmt.Errorf("select conversation %s: %w", conversationID, err)
	}

	// Targeted lazy reconciliation of legacy demands, using durable provider
	// evidence. Ordinary snapshots remain read-only and avoid the writer lock.
	if account := conversationToDomain(conv).Account; account != nil && account.ReauthRequiredAt != nil {
		// Reconcile outside the read transaction: it writes, and it reads from
		// the pool that transaction holds a connection of.
		endRead()
		if _, reconcileErr := s.ReconcileConversationAuthentication(parent, conversationID, "", "", time.Now()); reconcileErr != nil {
			return ConversationSnapshot{}, reconcileErr
		}
		ctx, endRead, err = s.beginSnapshotRead(parent)
		if err != nil {
			return ConversationSnapshot{}, err
		}
		defer endRead()
		conv, err = s.snapshotReader(ctx).SelectConversationByID(ctx, conversationID)
		if err != nil {
			return ConversationSnapshot{}, err
		}
	}

	turnRows, err := s.snapshotReader(ctx).SelectConversationTurns(ctx, conversationID)
	if err != nil {
		return ConversationSnapshot{}, fmt.Errorf("select turns: %w", err)
	}
	retriedSources, err := s.conversationRetriedSources(ctx, conversationID)
	if err != nil {
		return ConversationSnapshot{}, err
	}
	humanPromptTurns, err := s.conversationHumanPromptTurns(ctx, conversationID)
	if err != nil {
		return ConversationSnapshot{}, err
	}
	// Messages and activities exclude anything attached to a rolled-back turn: the
	// agent has forgotten those, and a timeline that still showed them would be
	// describing a conversation the agent is not in.
	messageRows, err := s.snapshotReader(ctx).SelectConversationMessages(ctx, conversationID)
	if err != nil {
		return ConversationSnapshot{}, fmt.Errorf("select messages: %w", err)
	}
	activityRows, err := s.snapshotReader(ctx).SelectConversationActivities(ctx, conversationID)
	if err != nil {
		return ConversationSnapshot{}, fmt.Errorf("select activities: %w", err)
	}

	presentation := s.conversationHistoryPresentation(ctx, conversationID)
	snapshot := ConversationSnapshot{
		Conversation:                     conversationToDomain(conv),
		ActiveBranch:                     presentation.activeBranch,
		EditFloorSequence:                presentation.editFloorSequence,
		NativeForkAvailableAfterSequence: presentation.nativeForkAvailableAfterSequence,
		BranchPoints:                     presentation.branchPoints,
		BranchedFromEarlierMessage:       presentation.branchedFromEarlierMessage,
	}
	for _, row := range turnRows {
		turn := turnToDomain(row)
		turn.HasRetryAttempt = retriedSources[turn.ID]
		turn.HasHumanPrompt = humanPromptTurns[turn.ID]
		presentation.filterInactiveProviderTurn(&turn)
		snapshot.Turns = append(snapshot.Turns, turn)
	}
	for _, row := range messageRows {
		snapshot.Messages = append(snapshot.Messages, messageToDomain(row))
	}
	for _, row := range activityRows {
		snapshot.Activities = append(snapshot.Activities, activityToDomain(row))
	}
	return snapshot, nil
}

func (s *Store) conversationRetriedSources(ctx context.Context, conversationID string) (map[string]bool, error) {
	rows, err := s.snapshotReader(ctx).SelectConversationRetriedSourceTurnIDs(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("select retried conversation sources: %w", err)
	}
	sources := make(map[string]bool, len(rows))
	for _, sourceTurnID := range rows {
		sources[sourceTurnID] = true
	}
	return sources, nil
}

func (s *Store) conversationHumanPromptTurns(ctx context.Context, conversationID string) (map[string]bool, error) {
	rows, err := s.snapshotReader(ctx).SelectConversationHumanPromptTurnIDs(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("select conversation human prompts: %w", err)
	}
	turns := make(map[string]bool, len(rows))
	for _, turnID := range rows {
		turns[turnID] = true
	}
	return turns, nil
}

type conversationHistoryPresentation struct {
	activeBranch                     domain.ConversationBranch
	activeProviderScopeID            string
	activeProviderBindingID          string
	editFloorSequence                int64
	nativeForkAvailableAfterSequence int64
	branchProviderScopes             map[string]string
	branchPoints                     []domain.ConversationBranchPoint
	branchedFromEarlierMessage       bool
}

func (p conversationHistoryPresentation) filterInactiveProviderTurn(turn *domain.ConversationTurn) {
	if p.activeProviderScopeID == "" || turn.BranchID == "" {
		return
	}
	providerScopeID, known := p.branchProviderScopes[turn.BranchID]
	if known && providerScopeID != p.activeProviderScopeID {
		// The opaque id remains durable in SQLite, but exposing it on the active
		// snapshot would draw rollback/edit controls that the current provider can
		// never honor.
		turn.ProviderTurnID = ""
	}
}

// conversationHistoryPresentation scopes edit affordances to the provider that
// currently owns the conversation. Provider boundaries are parented lineage
// nodes, but unlike an edit branch they replace no human prompt.
func (s *Store) conversationHistoryPresentation(
	ctx context.Context,
	conversationID string,
) conversationHistoryPresentation {
	branches, err := s.ConversationBranches(ctx, conversationID)
	if err != nil {
		return conversationHistoryPresentation{}
	}
	presentation := conversationHistoryPresentation{
		branchProviderScopes: make(map[string]string, len(branches)),
	}
	for _, branch := range branches {
		presentation.branchProviderScopes[branch.ID] = branch.ProviderScopeID
		if branch.Active {
			presentation.activeBranch = branch
			presentation.activeProviderScopeID = branch.ProviderScopeID
			presentation.activeProviderBindingID = branch.ProviderBindingID
			presentation.branchedFromEarlierMessage =
				branch.ParentBranchID != "" && branch.ReplacedTurnID != ""
		}
	}
	if sequence, queryErr := s.snapshotReader(ctx).SelectConversationNativeForkAvailableAfterSequence(ctx, conversationID); queryErr == nil {
		presentation.nativeForkAvailableAfterSequence = sequence
	}
	for _, branch := range branches {
		if branch.ID == presentation.activeProviderBindingID {
			presentation.editFloorSequence = branch.ForkAfterSequence
			break
		}
	}
	presentation.branchPoints = conversationBranchPointsForProviderBinding(
		branches, presentation.activeProviderBindingID)
	return presentation
}

func conversationBranchPointsForProviderBinding(
	branches []domain.ConversationBranch,
	activeProviderBindingID string,
) []domain.ConversationBranchPoint {
	type groupKey struct {
		sourceBranchID string
		replacedTurnID string
	}
	type member struct {
		branchID string
		turnID   string
	}
	groups := make(map[groupKey][]member)
	groupByReplacement := make(map[string]groupKey)
	order := make([]groupKey, 0)
	for _, branch := range branches {
		if branch.ProviderBindingID != activeProviderBindingID ||
			branch.ParentBranchID == "" || branch.ReplacedTurnID == "" || branch.ReplacementTurnID == "" {
			continue
		}
		key := groupKey{sourceBranchID: branch.ParentBranchID, replacedTurnID: branch.ReplacedTurnID}
		if existing, ok := groupByReplacement[branch.ReplacedTurnID]; ok {
			key = existing
		}
		if _, exists := groups[key]; !exists {
			order = append(order, key)
			groups[key] = []member{{branchID: key.sourceBranchID, turnID: key.replacedTurnID}}
		}
		groups[key] = append(groups[key], member{branchID: branch.ID, turnID: branch.ReplacementTurnID})
		groupByReplacement[branch.ReplacementTurnID] = key
	}

	points := make([]domain.ConversationBranchPoint, 0, len(branches))
	for _, key := range order {
		members := groups[key]
		for index, current := range members {
			point := domain.ConversationBranchPoint{
				TurnID: current.turnID, Position: index + 1, Total: len(members),
			}
			if index > 0 {
				point.PreviousBranchID = members[index-1].branchID
			}
			if index+1 < len(members) {
				point.NextBranchID = members[index+1].branchID
			}
			points = append(points, point)
		}
	}
	return points
}

/* ---- helpers ---------------------------------------------------------- */

// turnIDFor resolves a provider turn id to Tallpine's turn id, or nil when the turn is
// unknown. An item with no turn still belongs in the timeline, so an unresolved
// lookup is not an error.
func (s *Store) turnIDFor(ctx context.Context, conversationID, providerTurnID string) sql.NullString {
	if providerTurnID == "" {
		return sql.NullString{}
	}
	row, err := s.conversationReader(ctx).SelectConversationTurnByProviderID(ctx,
		gen.SelectConversationTurnByProviderIDParams{
			ConversationID: conversationID,
			ProviderTurnID: providerTurnID,
		})
	if err != nil {
		return sql.NullString{}
	}
	return sql.NullString{String: row.ID, Valid: true}
}
