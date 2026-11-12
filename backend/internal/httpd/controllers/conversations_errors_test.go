package controllers

import (
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

// TestWriteConversationErrorMapping pins the status and code every chat
// sentinel maps to, wrapped the way services return them.
func TestWriteConversationErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{ports.ErrSessionNotFound, 404, "SESSION_NOT_FOUND"},
		{chatsvc.ErrNotChatMode, 409, "SESSION_MODE_MISMATCH"},
		{chatsvc.ErrNoController, 409, "CHAT_CONTROLLER_NOT_READY"},
		{chatsvc.ErrNotProvisioning, 409, "CHAT_CONTROLLER_NOT_READY"},
		{chatsvc.ErrContinuationUnavailable, 409, "CHAT_CONTINUATION_UNAVAILABLE"},
		{chatsvc.ErrExcerptInvalid, 400, "CHAT_EXCERPT_INVALID"},
		{chatsvc.ErrExcerptStale, 409, "CHAT_EXCERPT_STALE"},
		{chatsvc.ErrPromptSuggestionStale, 409, "CHAT_PROMPT_SUGGESTION_STALE"},
		{chatsvc.ErrControllerHandoff, 409, "CHAT_INTERFACE_TRANSITION"},
		{chatsvc.ErrClearWhileBusy, 409, "CHAT_CLEAR_WHILE_BUSY"},
		{chatsvc.ErrClearWithText, 400, "CHAT_CLEAR_WITH_TEXT"},
		// A Claude Code /clear runs as a handoff and is refused as a switch is.
		{fmt.Errorf("clear: %w", &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalPendingInteraction, Detail: "answer first"}), 409, "PROVIDER_SWITCH_pending_interaction"},
		{chatsvc.ErrCompactionUnsupported, 409, "CHAT_COMPACTION_UNSUPPORTED"},
		{chatsvc.ErrCompactionWhileBusy, 409, "CHAT_COMPACTION_BUSY"},
		{chatsvc.ErrNoActiveTurn, 409, "CHAT_NO_ACTIVE_TURN"},
		{domain.ErrNoConversationTurn, 404, "CHAT_TURN_NOT_FOUND"},
		{domain.ErrClientMessageConflict, 409, "CHAT_MESSAGE_IDEMPOTENCY_CONFLICT"},
		{chatsvc.ErrTurnRunning, 409, "CHAT_TURN_RUNNING"},
		{chatsvc.ErrTurnNotRollbackable, 409, "CHAT_TURN_NOT_ROLLBACKABLE"},
		{chatsvc.ErrTurnProviderMismatch, 409, "CHAT_TURN_PROVIDER_MISMATCH"},
		{chatsvc.ErrRollbackUnsupported, 409, "CHAT_ROLLBACK_UNSUPPORTED"},
		{chatsvc.ErrForkUnsupported, 409, "CHAT_FORK_UNSUPPORTED"},
		{chatsvc.ErrRenameUnsupported, 409, "CHAT_RENAME_UNSUPPORTED"},
		{chatsvc.ErrOrchestratorRename, 400, "ORCHESTRATOR_RENAME_UNSUPPORTED"},
		{chatsvc.ErrMCPReloadUnsupported, 409, "CHAT_MCP_RELOAD_UNSUPPORTED"},
		{chatsvc.ErrTitleRequired, 400, "CHAT_TITLE_REQUIRED"},
		{chatsvc.ErrProviderRefused, 409, "CHAT_PROVIDER_REFUSED"},
		{ports.ErrChatRequestNotPending, 409, "CHAT_REQUEST_NOT_PENDING"},
		{ports.ErrChatDecisionNotOffered, 400, "CHAT_DECISION_NOT_OFFERED"},
		{ports.ErrChatConfigOptionInvalid, 400, "CHAT_CONFIG_OPTION_INVALID"},
		{ports.ErrChatUnsupported, 409, "SESSION_MODE_UNSUPPORTED"},
		{ports.ErrChatDriverUnavailable, 409, "CHAT_DRIVER_UNAVAILABLE"},
		{ports.ErrChatDriverIncompatible, 409, "CHAT_DRIVER_INCOMPATIBLE"},
		{ports.ErrChatAuthRequired, 409, "CHAT_AUTH_REQUIRED"},
		{ports.ErrChatResumeFailed, 409, "CHAT_RESUME_FAILED"},
	} {
		t.Run(tc.code+"/"+tc.err.Error(), func(t *testing.T) {
			w := httptest.NewRecorder()
			writeConversationError(w, httptest.NewRequest(http.MethodGet, "/", nil), fmt.Errorf("wrapped: %w", tc.err))
			var body struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status || body.Code != tc.code {
				t.Fatalf("got %d %q, want %d %q", w.Code, body.Code, tc.status, tc.code)
			}
		})
	}
}

// TestConversationControllersDoNotImportStorage keeps the conversation and
// review controllers off the sqlite store; sentinels they match live in ports.
func TestConversationControllersDoNotImportStorage(t *testing.T) {
	files, err := filepath.Glob("conversation*.go")
	if err != nil {
		t.Fatal(err)
	}
	more, _ := filepath.Glob("reviews.go")
	for _, f := range append(files, more...) {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range parsed.Imports {
			if strings.Contains(imp.Path.Value, "/storage/") {
				t.Errorf("%s imports %s", f, imp.Path.Value)
			}
		}
	}
}
