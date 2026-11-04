package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

const workerSendPrefix = domain.WorkerSendClientMessagePrefix

// prepareWorkerQueue runs under the controller's dispatch lock. It batches only
// server-attributed, text-only worker sends to their own project orchestrator.
// Human sends, steering, attachments, different senders and other tasks retain
// their queue boundary. Nothing is inferred from phrases such as "blocked".
func (s *Service) prepareWorkerQueue(ctx context.Context, c *Controller, queued domain.QueuedTurn) (domain.QueuedTurn, error) {
	if c.reviewID != "" || queued.Origin != domain.MessageOriginAutomation || s.sessions == nil {
		return queued, nil
	}
	target, found, err := s.sessions.GetSession(ctx, c.sessionID)
	if err != nil || !found || target.Kind != domain.KindOrchestrator {
		return queued, err
	}
	first, err := c.store.QueuedDeliveryMessage(ctx, c.conversation.ID, queued.TurnID)
	if err != nil {
		return queued, err
	}
	// Queues created by older versions may already contain frozen reports.
	if reports := s.reports.Load(); first.SenderSessionID == "" && strings.HasPrefix(first.ClientMessageID, "report-batch:") && reports != nil {
		text, found, err := reports.RefreshDelivery(ctx, c.sessionID, first.ClientMessageID)
		if err != nil || !found {
			return queued, err
		}
		if text == "" {
			return domain.QueuedTurn{}, c.store.CancelQueuedTurnByID(ctx, c.conversation.ID, queued.TurnID, s.now())
		}
		if text != first.Text {
			if err := c.store.UpdateQueuedTurnMessage(ctx, c.conversation.ID, queued.TurnID, text, first.DeliveryContentJSON,
				first.Revision, s.now(), domain.ConversationQueuedEditDelivery{}); err != nil {
				return queued, err
			}
			queued.Text = text
		}
		return queued, nil
	}
	if first.SenderSessionID == "" || first.DeliveryContentJSON != "" || first.Revision != 0 {
		return queued, nil
	}
	sender, found, err := s.sessions.GetSession(ctx, domain.SessionID(first.SenderSessionID))
	if err != nil || !found || sender.Kind != domain.KindWorker || sender.ProjectID != target.ProjectID {
		return queued, err
	}
	messages := []domain.ConversationMessage{first}
	// Legacy sends have no durable authorship marker. Enrich those individually;
	// only newly marked coordination sends can absorb their adjacent peers.
	if strings.HasPrefix(first.ClientMessageID, workerSendPrefix) {
		prefix, err := c.store.QueuedMessages(ctx, c.conversation.ID, 32)
		if err != nil {
			return queued, err
		}
		if len(prefix) == 0 || prefix[0].TurnID != first.TurnID {
			return queued, fmt.Errorf("worker queue changed before dispatch")
		}
		bytes := len(first.Text)
		for _, next := range prefix[1:] {
			if next.SenderSessionID != first.SenderSessionID || next.Origin != domain.MessageOriginAutomation ||
				!strings.HasPrefix(next.ClientMessageID, workerSendPrefix) || next.DeliveryContentJSON != "" || next.Revision != 0 ||
				bytes+len(next.Text) > 64<<10 ||
				(first.CreatedAt.Before(sender.Metadata.LatestInteractionAt) && !next.CreatedAt.Before(sender.Metadata.LatestInteractionAt)) {
				break
			}
			messages = append(messages, next)
			bytes += len(next.Text)
		}
	}
	var body strings.Builder
	fmt.Fprintf(&body, "Queued worker messages from %s (authored times below). Read these together; older status prose is historical, not a new blocker. Preserve unanswered requests and outputs.\n", sender.ID)
	for _, message := range messages {
		fmt.Fprintf(&body, "\n[sent %s]\n%s\n", message.CreatedAt.UTC().Format(time.RFC3339Nano), message.Text)
	}
	fmt.Fprintf(&body, "\nCurrent worker facts observed at %s: activity=%s, terminated=%t. These facts do not establish task completion.\n", s.now().UTC().Format(time.RFC3339Nano), sender.Activity.State, sender.IsTerminated)
	if reader, ok := s.sessions.(interface {
		LastAssistantMessage(context.Context, domain.SessionID) (string, time.Time, bool, error)
	}); ok {
		final, at, found, err := reader.LastAssistantMessage(ctx, sender.ID)
		if err != nil {
			return queued, err
		}
		if found && at.After(first.CreatedAt) {
			fmt.Fprintf(&body, "\nNewer recorded final assistant message (%s; not a submitted report):\n%s\n", at.UTC().Format(time.RFC3339Nano), final)
		}
	}
	queued.Text = body.String()
	if err := c.store.CombineQueuedMessages(ctx, c.conversation.ID, messages, queued.Text, s.now()); err != nil {
		return queued, err
	}
	return queued, nil
}
