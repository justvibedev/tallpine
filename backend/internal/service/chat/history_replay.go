package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// approximateReplayContent renders only the durable textual transcript before the
// edited prompt. Tool calls, approvals, and file changes are intentionally not
// replayed: doing so could repeat side effects. Providers that support native
// fork remain on that exact-history path instead.
const approximateReplayBudget = 24 * 1024

func (s *Service) approximateReplayContent(
	ctx context.Context,
	conversationID string,
	floor, cutoff int64,
) (ports.ChatContent, bool, error) {
	if s.reader == nil {
		return ports.ChatContent{}, false, errors.New("conversation snapshot reader is unavailable")
	}
	rows, err := s.reader.LoadConversationSnapshot(ctx, conversationID)
	if err != nil {
		return ports.ChatContent{}, false, fmt.Errorf("load conversation for replay: %w", err)
	}
	seed, truncated, err := buildApproximateReplayContext(rows.Messages, floor, cutoff)
	if err != nil {
		return ports.ChatContent{}, false, err
	}
	return ports.ChatContent{
		Type: "resource", URI: ports.ChatInternalReplayResourceURI, Name: "approximate conversation context",
		MIMEType: "application/json", Text: seed, Internal: true,
	}, truncated, nil
}

func supportsApproximateReplay(conversation ports.ChatConversation) bool {
	capabilities := conversation.Capabilities()
	return capabilities.Has(ports.ChatCapabilityPromptReplay) &&
		capabilities.Has(ports.ChatCapabilityEmbeddedContext)
}

func buildApproximateReplayContext(rows []domain.ConversationMessage, floor, cutoff int64) (string, bool, error) {
	type replayMessage struct {
		Sequence int64              `json:"sequence"`
		Role     domain.MessageRole `json:"role"`
		Text     string             `json:"text"`
	}
	type replayEnvelope struct {
		Kind      string          `json:"kind"`
		Messages  []replayMessage `json:"messages"`
		Truncated bool            `json:"truncated"`
	}
	encode := func(selected []replayMessage, truncated bool) ([]byte, error) {
		return json.Marshal(replayEnvelope{
			Kind: "approximate_conversation_context", Messages: selected, Truncated: truncated,
		})
	}
	messages := make([]replayMessage, 0, len(rows))
	for _, message := range rows {
		if message.Sequence <= floor || message.Sequence > cutoff || message.Streaming || strings.TrimSpace(message.Text) == "" {
			continue
		}
		switch message.Role {
		case domain.MessageRoleUser, domain.MessageRoleAssistant:
		default:
			continue
		}
		messages = append(messages, replayMessage{message.Sequence, message.Role, strings.TrimSpace(message.Text)})
	}
	sort.SliceStable(messages, func(i, j int) bool { return messages[i].Sequence < messages[j].Sequence })
	selected := make([]replayMessage, 0, len(messages))
	truncated := false
	for index := len(messages) - 1; index >= 0; index-- {
		candidate := make([]replayMessage, len(selected)+1)
		candidate[0] = messages[index]
		copy(candidate[1:], selected)
		encoded, marshalErr := encode(candidate, true)
		if marshalErr != nil {
			return "", false, fmt.Errorf("encode replay context: %w", marshalErr)
		}
		if len(encoded) > approximateReplayBudget {
			truncated = true
			continue
		}
		selected = candidate
	}
	encoded, err := encode(selected, truncated)
	if err != nil {
		return "", false, fmt.Errorf("encode replay context: %w", err)
	}
	// The all-messages envelope uses `false`, which is one byte larger than the
	// pessimistic `true` admission check above. Drop oldest context if that final
	// byte crosses the hard wire budget.
	for len(encoded) > approximateReplayBudget && len(selected) > 0 {
		selected = selected[1:]
		truncated = true
		encoded, err = encode(selected, true)
		if err != nil {
			return "", false, fmt.Errorf("encode replay context: %w", err)
		}
	}
	return string(encoded), truncated, nil
}

func withoutInternalReplayContent(content []ports.ChatContent) []ports.ChatContent {
	filtered := make([]ports.ChatContent, 0, len(content))
	for _, item := range content {
		if item.Internal {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}
