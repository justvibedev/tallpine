package chat

import (
	"encoding/json"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// nativeHistoryTurn is the durable identity Tallpine already assigned to one native
// turn. ProviderTurnID is the value every replay event must use before it reaches
// the projector; the other fields are progressively stronger ways to recognize
// it when an ACP agent assigned a different replay turn id.
type nativeHistoryTurn struct {
	providerTurnID string
	state          domain.TurnState
	clientMessage  string
	providerItem   string
	text           string
	messages       map[string]int
	activities     map[string]int
}

func nativeHistoryMessageFingerprint(role domain.MessageRole, text string) string {
	return string(role) + "\x00" + text
}

func nativeHistoryEventMessageFingerprint(event ports.ChatEvent) (string, bool) {
	switch event.Kind {
	case ports.ChatEventUserMessageCompleted:
		return nativeHistoryMessageFingerprint(domain.MessageRoleUser, event.Text), true
	case ports.ChatEventMessageCompleted:
		return nativeHistoryMessageFingerprint(domain.MessageRoleAssistant, event.Text), true
	default:
		return "", false
	}
}

func nativeHistoryActivityFingerprint(
	kind domain.ActivityKind,
	status domain.ActivityStatus,
	summary string,
	detail []byte,
) string {
	return string(kind) + "\x00" + string(status) + "\x00" + summary + "\x00" + string(detail)
}

// mapNativeHistoryTurns identifies which durable Tallpine turn owns each raw replay
// turn before reconciliation rewrites provider turn ids or drops duplicates.
func mapNativeHistoryTurns(
	events []ports.ChatEvent,
	existingTurns []domain.ConversationTurn,
	existingMessages []domain.ConversationMessage,
	existingActivities []domain.ConversationActivity,
) (map[string]*nativeHistoryTurn, map[string]*nativeHistoryTurn) {
	return indexNativeHistoryTurns(existingTurns, existingMessages, existingActivities).mapReplay(events)
}

// The durable snapshot is invariant during history settling. Keep its index
// separate from per-replay matching so refreshes do not rebuild it.
type nativeHistoryTurnIndex struct {
	byProviderTurnID map[string]*nativeHistoryTurn
	providerItems    map[string]*nativeHistoryTurn
	ordered          []*nativeHistoryTurn
}

// nativeHistoryMessageText restores Tallpine-owned text context for native transcript
// matching. Public message text stays short; opaque provider IDs can change on
// load, so reconciliation must compare the exact prompt that was dispatched.
func nativeHistoryMessageText(message domain.ConversationMessage) string {
	if message.Role != domain.MessageRoleUser || !message.Continuation || message.DeliveryContentJSON == "" {
		return message.Text
	}
	var content []ports.ChatContent
	if json.Unmarshal([]byte(message.DeliveryContentJSON), &content) != nil {
		return message.Text
	}
	internal := make([]ports.ChatContent, 0, len(content))
	for _, block := range content {
		if block.Internal && block.Type == "text" {
			internal = append(internal, block)
		}
	}
	return excerptDeliveryMessage(ports.ChatUserMessage{Text: message.Text, Content: internal}).Text
}

func indexNativeHistoryTurns(
	existingTurns []domain.ConversationTurn,
	existingMessages []domain.ConversationMessage,
	existingActivities []domain.ConversationActivity,
) *nativeHistoryTurnIndex {
	if len(existingTurns) == 0 {
		return nil
	}

	byTallpineTurnID := make(map[string]*nativeHistoryTurn, len(existingTurns))
	byProviderTurnID := make(map[string]*nativeHistoryTurn, len(existingTurns))
	ordered := make([]*nativeHistoryTurn, 0, len(existingTurns))
	for _, turn := range existingTurns {
		if turn.ProviderTurnID == "" || turn.RolledBackAt != nil {
			continue
		}
		candidate := &nativeHistoryTurn{
			providerTurnID: turn.ProviderTurnID,
			state:          turn.State,
			messages:       make(map[string]int),
			activities:     make(map[string]int),
		}
		if turn.ProviderInputText != "" {
			candidate.text = turn.ProviderInputText
			candidate.messages[nativeHistoryMessageFingerprint(domain.MessageRoleUser, turn.ProviderInputText)]++
		}
		byTallpineTurnID[turn.ID] = candidate
		byProviderTurnID[turn.ProviderTurnID] = candidate
		ordered = append(ordered, candidate)
	}
	if len(ordered) == 0 {
		return nil
	}

	// A provider item is useful only when it identifies exactly one durable turn.
	// Treat collisions as ambiguous rather than guessing across turns.
	providerItems := make(map[string]*nativeHistoryTurn)
	ambiguousItems := make(map[string]bool)
	rememberProviderItem := func(itemID string, candidate *nativeHistoryTurn) {
		if itemID == "" || candidate == nil || ambiguousItems[itemID] {
			return
		}
		if previous, exists := providerItems[itemID]; exists && previous != candidate {
			delete(providerItems, itemID)
			ambiguousItems[itemID] = true
			return
		}
		providerItems[itemID] = candidate
	}
	for _, message := range existingMessages {
		candidate := byTallpineTurnID[message.TurnID]
		if candidate == nil {
			continue
		}
		candidate.messages[nativeHistoryMessageFingerprint(message.Role, nativeHistoryMessageText(message))]++
		rememberProviderItem(message.ProviderItemID, candidate)
		if message.Role == domain.MessageRoleUser && candidate.text == "" {
			candidate.text = nativeHistoryMessageText(message)
			candidate.clientMessage = message.ClientMessageID
			candidate.providerItem = message.ProviderItemID
		}
	}
	for _, activity := range existingActivities {
		candidate := byTallpineTurnID[activity.TurnID]
		if candidate == nil {
			continue
		}
		var steerInput struct {
			ProviderInputText         string `json:"providerInputText"`
			ProviderInputStoredOnTurn bool   `json:"providerInputStoredOnTurn"`
		}
		if json.Unmarshal(activity.Detail, &steerInput) == nil &&
			steerInput.ProviderInputText != "" && !steerInput.ProviderInputStoredOnTurn {
			candidate.messages[nativeHistoryMessageFingerprint(domain.MessageRoleUser, steerInput.ProviderInputText)]++
		}
		candidate.activities[nativeHistoryActivityFingerprint(
			activity.Kind, activity.Status, activity.Summary, activity.Detail,
		)]++
		rememberProviderItem(activity.ProviderItemID, candidate)
	}
	return &nativeHistoryTurnIndex{byProviderTurnID: byProviderTurnID, providerItems: providerItems, ordered: ordered}
}

func (index *nativeHistoryTurnIndex) mapReplay(events []ports.ChatEvent) (map[string]*nativeHistoryTurn, map[string]*nativeHistoryTurn) {
	mapped := make(map[string]*nativeHistoryTurn)
	if index == nil || len(events) == 0 {
		return mapped, nil
	}
	byProviderTurnID, providerItems, ordered := index.byProviderTurnID, index.providerItems, index.ordered
	used := make(map[*nativeHistoryTurn]bool, len(ordered))

	bind := func(replayTurnID string, candidate *nativeHistoryTurn) {
		if replayTurnID == "" || candidate == nil {
			return
		}
		if previous, exists := mapped[replayTurnID]; exists && previous != candidate {
			// One replay turn claiming stable items from two durable turns is
			// malformed/ambiguous. Preserve the first strong observation rather than
			// moving already-associated events between turns.
			return
		}
		mapped[replayTurnID] = candidate
		used[candidate] = true
	}
	providerItemCandidate := func(event ports.ChatEvent) *nativeHistoryTurn {
		var match *nativeHistoryTurn
		identities := make([]string, 0, 1+len(event.ProviderItemAliases))
		identities = append(identities, event.ProviderItemID)
		identities = append(identities, event.ProviderItemAliases...)
		for _, identity := range identities {
			candidate := providerItems[identity]
			if candidate == nil {
				continue
			}
			if match != nil && match != candidate {
				return nil
			}
			match = candidate
		}
		return match
	}
	// Gather mappings from the complete replay before rewriting TurnStarted, which
	// necessarily arrives before the assistant/tool item that can identify it.
	for _, event := range events {
		if candidate := byProviderTurnID[event.ProviderTurnID]; candidate != nil {
			bind(event.ProviderTurnID, candidate)
		}
		if candidate := providerItemCandidate(event); candidate != nil {
			bind(event.ProviderTurnID, candidate)
		}
	}
	for _, event := range events {
		if event.Kind != ports.ChatEventUserMessageCompleted || event.ProviderTurnID == "" {
			continue
		}
		if mapped[event.ProviderTurnID] != nil {
			continue
		}
		var match *nativeHistoryTurn
		clientIdentities := make([]string, 0, 1+len(event.ProviderItemAliases))
		clientIdentities = append(clientIdentities, event.ClientMessageID)
		clientIdentities = append(clientIdentities, event.ProviderItemAliases...)
		if event.ClientMessageID != "" || len(event.ProviderItemAliases) > 0 {
			for _, candidate := range ordered {
				if used[candidate] || candidate.clientMessage == "" {
					continue
				}
				for _, identity := range clientIdentities {
					if candidate.clientMessage == identity {
						match = candidate
						break
					}
				}
				if match != nil {
					break
				}
			}
		}
		if match == nil && event.ProviderItemID != "" {
			for _, candidate := range ordered {
				if !used[candidate] && candidate.providerItem == event.ProviderItemID {
					match = candidate
					break
				}
			}
		}
		if match == nil && event.Text != "" {
			for _, candidate := range ordered {
				if !used[candidate] && candidate.text == event.Text {
					match = candidate
					break
				}
			}
		}
		bind(event.ProviderTurnID, match)
	}
	return mapped, providerItems
}

// reconcileNativeHistory maps a provider replay onto Tallpine's existing durable
// turns before any event is projected.
//
// ACP intentionally treats message ids as opaque. Well-behaved agents may echo
// the client's id, while others persist their own user uuid. Assistant/tool item
// ids are usually replay-stable, so they are the strongest cross-restart signal;
// the original client id is next, and exact prompt text in conversation order is
// the compatibility fallback. This belongs above every driver: Codex history is
// already identity-stable, while all ACP bindings need the same reconciliation.
func reconcileNativeHistory(
	events []ports.ChatEvent,
	existingTurns []domain.ConversationTurn,
	existingMessages []domain.ConversationMessage,
	existingActivities []domain.ConversationActivity,
) []ports.ChatEvent {
	if len(events) == 0 || len(existingTurns) == 0 {
		return events
	}

	mapped, providerItems := mapNativeHistoryTurns(events, existingTurns, existingMessages, existingActivities)
	if len(mapped) == 0 {
		return events
	}
	providerItemAliasCandidate := func(event ports.ChatEvent) *nativeHistoryTurn {
		var match *nativeHistoryTurn
		for _, identity := range event.ProviderItemAliases {
			candidate := providerItems[identity]
			if candidate == nil {
				continue
			}
			if match != nil && match != candidate {
				return nil
			}
			match = candidate
		}
		return match
	}
	// A native provider may omit persisted item ids even though its live stream
	// supplied them. Codex does this today: a live assistant message can be
	// `msg_...`, while thread/read later calls the same item `item-2`. Stable turn
	// identity still tells us which Tallpine turn owns the replay, so suppress already
	// projected message/activity facts by semantic fingerprint. Counts preserve
	// legitimate repeated identical items within one turn.
	dropEvent := make([]bool, len(events))
	dropItem := make(map[string]bool)
	itemKey := func(event ports.ChatEvent) string {
		if event.ProviderItemID == "" {
			return ""
		}
		return event.ProviderTurnID + "\x00" + event.ProviderItemID
	}
	for i, event := range events {
		candidate := mapped[event.ProviderTurnID]
		if candidate == nil {
			continue
		}
		matched := false
		if aliased := providerItemAliasCandidate(event); aliased != nil && aliased == candidate {
			// The adapter identified the exact pre-namespacing item. Its detail can
			// legitimately differ only because nested provider ids inside that JSON
			// were namespaced too; do not re-import the same durable item under its
			// new outer id.
			matched = true
		}
		if fingerprint, ok := nativeHistoryEventMessageFingerprint(event); ok && candidate.messages[fingerprint] > 0 {
			candidate.messages[fingerprint]--
			matched = true
		}
		if event.Kind == ports.ChatEventActivityCompleted {
			fingerprint := nativeHistoryActivityFingerprint(
				event.ActivityKind, event.ActivityStatus, event.Summary, event.Detail,
			)
			if candidate.activities[fingerprint] > 0 {
				candidate.activities[fingerprint]--
				matched = true
			}
		}
		if !matched {
			continue
		}
		dropEvent[i] = true
		if key := itemKey(event); key != "" {
			dropItem[key] = true
		}
	}

	reconciled := make([]ports.ChatEvent, 0, len(events))
	for i, event := range events {
		if dropEvent[i] || dropItem[itemKey(event)] {
			continue
		}
		candidate := mapped[event.ProviderTurnID]
		if candidate == nil {
			reconciled = append(reconciled, event)
			continue
		}
		event.ProviderTurnID = candidate.providerTurnID
		// A replay may have no portable turn-outcome field. Preserve Tallpine's stronger
		// known result only when the adapter reports recovered. Conversely, a known
		// replay outcome upgrades an older recovered observation.
		if event.Kind == ports.ChatEventTurnCompleted &&
			event.TurnState == domain.TurnStateRecovered && knownTurnOutcome(candidate.state) {
			event.TurnState = candidate.state
		}
		reconciled = append(reconciled, event)
	}
	return reconciled
}

func knownTurnOutcome(state domain.TurnState) bool {
	switch state {
	case domain.TurnStateCompleted, domain.TurnStateInterrupted, domain.TurnStateFailed:
		return true
	default:
		return false
	}
}
