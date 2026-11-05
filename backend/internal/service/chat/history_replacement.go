package chat

import (
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// newReplacementController builds the controller a history operation (edit,
// rollback, branch) installs in place of source. It keeps the session facts
// the launch set on source: the session kind, which gives an orchestrator its
// own compaction default (B193), and the window the project's environment
// sets. Without them the replacement would resolve the worker default.
func (s *Service) newReplacementController(id domain.SessionID, source *Controller, conversation domain.ConversationRecord, generation string, provider ports.ChatConversation) *Controller {
	replacement := newController(id, source.owner(), conversation, generation, source.harness, provider, s.store, s.activity, s.log, s.newID, s.now, s.onAccountChanged, s.recheckAccount, s.onCodexCapacityChanged, s.reader)
	replacement.onAsyncTask = s.onAsyncTask
	replacement.onAsyncTasksGone = s.onAsyncTasksGone
	replacement.kind = source.kind
	replacement.compactBaseline = source.projectCompactBaseline()
	return replacement
}
