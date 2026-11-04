package chat

import (
	"bytes"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type nativeHistoryHighWater struct {
	sequence       int64
	providerTurnID string
	providerItemID string
	kind           ports.ChatEventKind
	text           string
	activityKind   domain.ActivityKind
	activityStatus domain.ActivityStatus
	summary        string
	detail         []byte
}

// nativeHistoryCheckpoint combines the newest source-TUI facts with Tallpine's last
// settled projection of this same provider thread. The hook facts cover work
// completed while Chat was not attached; the Tallpine high-water mark covers an
// immediate round trip before a resumed TUI has emitted another hook.
type nativeHistoryCheckpoint struct {
	latestUserPromptAt      time.Time
	latestAssistantUpdateAt time.Time
	requireNewerTurn        bool
	nativeBoundary          *ports.NativeCheckpointBoundary
	latestUserPrompt        string
	latestAssistantUpdate   string
	completedUserPrompt     bool
	providerTurnID          string
	userMismatch            ports.ChatHistoryMismatchDimension
	assistantMismatch       ports.ChatHistoryMismatchDimension
	hardMismatches          []ports.ChatHistoryMismatchDimension
	tallpineHighWater       nativeHistoryHighWater
	tallpineHighWaterPeers  []nativeHistoryHighWater
	replayIndex             *nativeHistoryTurnIndex
}

// dropUnsettledHookFacts retires legacy checkpoint text that Tallpine recorded on a
// turn the provider never settled. A provider promises to reproduce settled work
// during history load, but a cancelled, interrupted or failed turn carries no
// such promise: Claude forks its next prompt from the pre-failure transcript
// entry, so `session/load` never replays that prompt as a completed user
// message. Requiring one is unsatisfiable — the settle loop then spends its full
// `nativeHistorySettleLimit` and the interface transition rolls back to Terminal
// on every future attempt, which is the same trap the high-water anchor below
// already avoids by only anchoring on completed turns.
//
// Evidence is required to drop a fact. A checkpoint Tallpine cannot tie to any of its
// own turns is left in place: absence of a row is not proof the work was
// unsettled, and these facts are what stop a stale replay from being imported as
// if it were current. Trusted source-TUI facts belong to a newer controller
// epoch; matching an old Chat turn's text cannot retire that evidence.
func (p *nativeHistoryCheckpoint) dropUnsettledHookFacts(
	turnsByID map[string]*domain.ConversationTurn,
	messages []domain.ConversationMessage,
) {
	settled := func(text string, role domain.MessageRole) bool {
		var newest *domain.ConversationTurn
		for _, message := range messages {
			if message.Role != role || !nativeHistoryTextMatches(text, message.Text) {
				continue
			}
			turn := turnsByID[message.TurnID]
			if turn == nil {
				continue
			}
			// The newest matching turn decides: the same prompt can be sent again
			// after a cancellation, and that later completed turn is replayable.
			if newest == nil || turn.RequestedAt.After(newest.RequestedAt) {
				newest = turn
			}
		}
		return newest == nil || newest.State == domain.TurnStateCompleted
	}
	if p.userMismatch == ports.ChatHistoryMismatchUntrustedUserText &&
		p.latestUserPrompt != "" && !settled(p.latestUserPrompt, domain.MessageRoleUser) {
		p.latestUserPrompt = ""
	}
	if p.assistantMismatch == ports.ChatHistoryMismatchUntrustedAssistantText &&
		p.latestAssistantUpdate != "" && !settled(p.latestAssistantUpdate, domain.MessageRoleAssistant) {
		p.latestAssistantUpdate = ""
	}
}

func (p *nativeHistoryCheckpoint) captureTallpineHighWater(
	sessionID domain.SessionID,
	turns []domain.ConversationTurn,
	messages []domain.ConversationMessage,
	activities []domain.ConversationActivity,
) {
	turnsByID := make(map[string]*domain.ConversationTurn, len(turns))
	for i := range turns {
		turnsByID[turns[i].ID] = &turns[i]
	}
	p.dropUnsettledHookFacts(turnsByID, messages)
	// An agent switch starts a new provider-native thread with a Tallpine coordination
	// turn. Completed turns before it belong to the previous provider: their
	// opaque ids remain useful timeline facts, but the new provider cannot replay
	// them and they must not gate this native-history import.
	var providerBoundary *domain.ConversationTurn
	var providerBoundarySequence int64
	turnEvidence := make(map[string]nativeHistoryHighWater, len(turns))
	userSequence := make(map[string]int64, len(turns))
	for _, message := range messages {
		kind := ports.ChatEventKind("")
		switch message.Role {
		case domain.MessageRoleUser:
			kind = ports.ChatEventUserMessageCompleted
		case domain.MessageRoleAssistant:
			kind = ports.ChatEventMessageCompleted
		}
		if !message.Streaming && kind != "" && message.Sequence > turnEvidence[message.TurnID].sequence {
			turnEvidence[message.TurnID] = nativeHistoryHighWater{
				sequence: message.Sequence, providerItemID: message.ProviderItemID, kind: kind, text: nativeHistoryMessageText(message),
			}
		}
		if message.Role == domain.MessageRoleUser && message.Sequence > 0 &&
			(userSequence[message.TurnID] == 0 || message.Sequence < userSequence[message.TurnID]) {
			userSequence[message.TurnID] = message.Sequence
		}
		turn := turnsByID[message.TurnID]
		if turn == nil || turn.RolledBackAt != nil || turn.HandledBySessionID != sessionID || message.Role != domain.MessageRoleUser ||
			!nativeHistoryCoordinationMessage(message.Text) {
			continue
		}
		if providerBoundary == nil || message.Sequence > providerBoundarySequence {
			providerBoundary = turn
			providerBoundarySequence = message.Sequence
		}
	}
	for _, activity := range activities {
		if activity.ProviderItemID == "" || activity.Sequence <= turnEvidence[activity.TurnID].sequence {
			continue
		}
		switch activity.Kind {
		case domain.ActivityKindCommand, domain.ActivityKindFileChange, domain.ActivityKindReasoning, domain.ActivityKindMCPTool:
		default:
			continue // Tallpine control-plane rows are not provider replay evidence.
		}
		if activity.Status == domain.ActivityStatusCompleted || activity.Status == domain.ActivityStatusFailed {
			turnEvidence[activity.TurnID] = nativeHistoryHighWater{
				sequence: activity.Sequence, providerItemID: activity.ProviderItemID, activityKind: activity.Kind,
				activityStatus: activity.Status, summary: activity.Summary, detail: append([]byte(nil), activity.Detail...),
			}
		}
	}
	evidence := func(turn *domain.ConversationTurn) nativeHistoryHighWater {
		boundary := turnEvidence[turn.ID]
		boundary.providerTurnID = turn.ProviderTurnID
		return boundary
	}
	// Queue reordering updates RequestedAt, not the message sequence. Preserve
	// that order. Tied turns require all completed boundaries: item sequences
	// alone cannot order an empty response after a reordered queued prompt.

	var latest *domain.ConversationTurn
	for i := range turns {
		turn := &turns[i]
		// Only completed turns anchor the high-water mark. A provider promises to
		// reproduce settled work during history load, but a failed or interrupted
		// turn's items carry no such promise: Claude forks its next prompt from the
		// pre-failure transcript entry, leaving the failed turn (e.g. a synthetic
		// auth-error message) on a dead branch that session/load never replays.
		// Requiring one of those items would make every future switch time out.
		if turn.HandledBySessionID != sessionID || turn.RolledBackAt != nil || turn.State != domain.TurnStateCompleted || turn.ProviderTurnID == "" {
			continue
		}
		if providerBoundary != nil && turn.ID != providerBoundary.ID {
			sequence := userSequence[turn.ID]
			if sequence == 0 {
				sequence = turnEvidence[turn.ID].sequence
			}
			if (sequence > 0 && sequence < providerBoundarySequence) ||
				(sequence == 0 && !turn.RequestedAt.After(providerBoundary.RequestedAt)) {
				continue
			}
		}
		if latest == nil || turn.RequestedAt.After(latest.RequestedAt) {
			latest = turn
			p.tallpineHighWaterPeers = nil
		} else if turn.RequestedAt.Equal(latest.RequestedAt) {
			if turnEvidence[turn.ID].sequence > turnEvidence[latest.ID].sequence {
				p.tallpineHighWaterPeers = append(p.tallpineHighWaterPeers, evidence(latest))
				latest = turn
				continue
			}
			p.tallpineHighWaterPeers = append(p.tallpineHighWaterPeers, evidence(turn))
		}
	}
	if latest == nil {
		return
	}
	p.retireSupersededLegacyFacts(sessionID, turnsByID, messages, latest)
	p.tallpineHighWater = evidence(latest)
	p.replayIndex = indexNativeHistoryTurns(turns, messages, activities)
}

func (p *nativeHistoryCheckpoint) retireSupersededLegacyFacts(sessionID domain.SessionID, turns map[string]*domain.ConversationTurn, messages []domain.ConversationMessage, latest *domain.ConversationTurn) {
	retire := func(text *string, observedAt time.Time, role domain.MessageRole, trust ports.ChatHistoryMismatchDimension) {
		if *text == "" || observedAt.IsZero() || (trust != ports.ChatHistoryMismatchUntrustedUserText && trust != ports.ChatHistoryMismatchUntrustedAssistantText) {
			return
		}
		var newest *domain.ConversationTurn
		for _, message := range messages {
			turn := turns[message.TurnID]
			if turn == nil || turn.HandledBySessionID != sessionID || turn.RolledBackAt != nil || message.Streaming || message.Role != role || !nativeHistoryTextMatches(*text, message.Text) {
				continue
			}
			if newest == nil || turn.RequestedAt.After(newest.RequestedAt) {
				newest = turn
			}
		}
		if newest == nil || newest.State != domain.TurnStateCompleted {
			return
		}
		if latest.CompletedAt != nil && observedAt.After(*latest.CompletedAt) {
			p.requireNewerTurn = true
			return
		}
		if observedAt.Before(latest.RequestedAt) && newest.RequestedAt.Before(latest.RequestedAt) {
			*text = ""
		}
	}
	retire(&p.latestUserPrompt, p.latestUserPromptAt, domain.MessageRoleUser, p.userMismatch)
	retire(&p.latestAssistantUpdate, p.latestAssistantUpdateAt, domain.MessageRoleAssistant, p.assistantMismatch)
}

func (p nativeHistoryCheckpoint) mismatches(
	events []ports.ChatEvent,
	existingTurns []domain.ConversationTurn,
	existingMessages []domain.ConversationMessage,
	existingActivities []domain.ConversationActivity,
) []ports.ChatHistoryMismatchDimension {
	completedTurns := make(map[string]bool)
	coordinationTurns := make(map[string]bool)
	for _, event := range events {
		if event.Kind == ports.ChatEventTurnCompleted && event.ProviderTurnID != "" {
			completedTurns[event.ProviderTurnID] = true
		}
		if event.Kind == ports.ChatEventUserMessageCompleted && nativeHistoryCoordinationMessage(event.Text) {
			coordinationTurns[event.ProviderTurnID] = true
		}
	}

	type replayTurnText struct {
		user      ports.ChatEvent
		assistant ports.ChatEvent
		nativeID  string
	}
	turnText := make(map[string]replayTurnText)
	latestCompletedTurnID := ""
	for _, event := range events {
		if event.Kind == ports.ChatEventTurnCompleted && event.ProviderTurnID != "" &&
			!coordinationTurns[event.ProviderTurnID] {
			latestCompletedTurnID = event.ProviderTurnID
		}
		if event.ProviderTurnID == "" {
			continue
		}
		text := turnText[event.ProviderTurnID]
		text.nativeID = event.NativeTurnID
		if text.nativeID == "" {
			text.nativeID = event.ProviderTurnID
		}
		switch event.Kind {
		case ports.ChatEventUserMessageCompleted:
			text.user = event
		case ports.ChatEventMessageCompleted:
			text.assistant = event
		}
		turnText[event.ProviderTurnID] = text
	}

	// A trusted checkpoint describes one main-thread turn. Selecting the latest
	// user and assistant independently can splice an older repeated answer onto a
	// newer incomplete turn and incorrectly admit a truncated provider replay.
	// Only an exact native turn ID can match an earlier occurrence. Repeated
	// prompt/answer pairs are not identities. Unidentified text stays latest-only.
	latestText := turnText[latestCompletedTurnID]
	checkpointMatched := p.latestUserPrompt == "" && p.latestAssistantUpdate == "" && p.providerTurnID == ""
	if p.completedUserPrompt && p.providerTurnID != "" {
		for turnID := range completedTurns {
			if coordinationTurns[turnID] || p.providerTurnID != turnText[turnID].nativeID {
				continue
			}
			text := turnText[turnID]
			if (p.latestUserPrompt == "" || nativeHistoryTextMatches(p.latestUserPrompt, text.user.Text)) &&
				(p.latestAssistantUpdate == "" || nativeHistoryTextMatches(p.latestAssistantUpdate, text.assistant.Text)) {
				checkpointMatched = true
				break
			}
		}
	} else {
		// Without an identified completion, retain the latest-turn gate.
		checkpointMatched =
			(p.latestUserPrompt == "" || nativeHistoryTextMatches(p.latestUserPrompt, latestText.user.Text)) &&
				(p.latestAssistantUpdate == "" || nativeHistoryTextMatches(p.latestAssistantUpdate, latestText.assistant.Text)) &&
				(p.providerTurnID == "" || p.providerTurnID == latestText.nativeID)
	}
	mismatches := append([]ports.ChatHistoryMismatchDimension(nil), p.hardMismatches...)
	if boundary := p.nativeBoundary; boundary != nil {
		matched := false
		for turnID, text := range turnText {
			if completedTurns[turnID] && boundary.UserMessageID != "" &&
				text.user.NativeUserMessageID == boundary.UserMessageID &&
				nativeHistoryTextMatches(boundary.UserText, text.user.Text) &&
				(boundary.Interrupted || nativeHistoryTextMatches(boundary.AssistantText, text.assistant.Text)) {
				matched = true
				break
			}
		}
		if !matched {
			mismatches = append(mismatches, ports.ChatHistoryMismatchTrustedTurn)
		}
	}
	if !checkpointMatched {
		if p.providerTurnID != "" {
			mismatches = append(mismatches, ports.ChatHistoryMismatchTrustedTurn)
		}
		if p.latestUserPrompt != "" && !nativeHistoryTextMatches(p.latestUserPrompt, latestText.user.Text) {
			mismatches = append(mismatches, p.userMismatch)
		}
		if p.latestAssistantUpdate != "" && !nativeHistoryTextMatches(p.latestAssistantUpdate, latestText.assistant.Text) {
			mismatches = append(mismatches, p.assistantMismatch)
		}
	}

	return p.highWaterMismatches(mismatches, events, completedTurns, existingTurns, existingMessages, existingActivities)
}

func nativeHistoryCoordinationMessage(text string) bool {
	text = strings.TrimSpace(text)
	_, coordination := domain.CoordinationDeliveryID(text)
	return coordination || strings.HasPrefix(text, "<ao-handoff-request") ||
		strings.HasPrefix(text, "Tallpine transferred the previous agent's context in hidden system instructions.") ||
		strings.HasPrefix(text, "AO transferred the previous agent's context in hidden system instructions.")
}

func nativeHistoryTextMatches(checkpoint, replayed string) bool {
	return domain.NativeCheckpointTextMatches(checkpoint, replayed)
}

// highWaterMismatches appends the mismatches of Tallpine's own high-water
// boundaries (and the newer-turn requirement) to the trusted-turn mismatches.
func (p nativeHistoryCheckpoint) highWaterMismatches(
	mismatches []ports.ChatHistoryMismatchDimension,
	events []ports.ChatEvent,
	completedTurns map[string]bool,
	existingTurns []domain.ConversationTurn,
	existingMessages []domain.ConversationMessage,
	existingActivities []domain.ConversationActivity,
) []ports.ChatHistoryMismatchDimension {
	highWater := p.tallpineHighWater
	if highWater.providerTurnID == "" {
		return mismatches
	}
	index := p.replayIndex
	if index == nil {
		index = indexNativeHistoryTurns(existingTurns, existingMessages, existingActivities)
	}
	mappedTurns, _ := index.mapReplay(events)
	boundaries := make(map[string]nativeHistoryHighWater, len(p.tallpineHighWaterPeers)+1)
	boundaries[highWater.providerTurnID] = highWater
	for _, peer := range p.tallpineHighWaterPeers {
		boundaries[peer.providerTurnID] = peer
	}
	for _, event := range events {
		candidate := mappedTurns[event.ProviderTurnID]
		if candidate == nil || !completedTurns[event.ProviderTurnID] {
			continue
		}
		boundary, required := boundaries[candidate.providerTurnID]
		if !required {
			continue
		}
		matched := boundary.providerItemID == "" && boundary.kind == "" && boundary.activityKind == ""
		if boundary.providerItemID != "" && event.ProviderItemID == boundary.providerItemID {
			matched = true
		}
		// ACP identifiers are opaque and can be reassigned by a conforming
		// provider during load. Exact settled content is the fallback identity
		// used by reconciliation for that same reason.
		if boundary.kind != "" && event.Kind == boundary.kind &&
			nativeHistoryTextMatches(boundary.text, event.Text) {
			matched = true
		}
		if boundary.activityKind != "" &&
			event.ActivityKind == boundary.activityKind &&
			event.ActivityStatus == boundary.activityStatus &&
			event.Summary == boundary.summary && bytes.Equal(event.Detail, boundary.detail) {
			matched = true
		}
		if matched {
			delete(boundaries, candidate.providerTurnID)
		}
	}
	if len(boundaries) > 0 {
		return append(mismatches, ports.ChatHistoryMismatchTallpineHighWater)
	}
	if p.requireNewerTurn {
		pastHighWater, newer := false, false
		for _, event := range events {
			if event.Kind != ports.ChatEventTurnCompleted {
				continue
			}
			candidate := mappedTurns[event.ProviderTurnID]
			if candidate != nil && candidate.providerTurnID == highWater.providerTurnID {
				pastHighWater = true
			} else if pastHighWater && (event.TurnState == domain.TurnStateCompleted || event.TurnState == domain.TurnStateRecovered) {
				newer = true
			}
		}
		if !newer {
			mismatches = append(mismatches, ports.ChatHistoryMismatchTallpineHighWater)
		}
	}
	return mismatches
}
