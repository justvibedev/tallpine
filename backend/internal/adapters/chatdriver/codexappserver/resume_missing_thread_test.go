package codexappserver

import (
	"context"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// A switch target that never had a turn left Codex an empty rollout. Waking it
// must start a new thread when the caller proves it never started, and must
// still refuse otherwise.
func TestCodexResumeOfAThreadThatNeverStarted(t *testing.T) {
	const empty = "failed to read thread: thread-store internal error: failed to read session metadata /x/rollout.jsonl: rollout at /x/rollout.jsonl is empty"
	for _, tt := range []struct {
		name           string
		freshIfMissing bool
	}{
		{"started fresh when it never had a turn", true},
		{"refused without that proof", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d, srv := newTestDriver(t)
			srv.replyError("thread/resume", -32603, empty)
			srv.respondTo("thread/start", `{"thread":{"id":"thread-new"},"model":"gpt-test"}`)
			conv, err := d.Resume(context.Background(), ports.ChatResumeConfig{
				SessionID: "unanswered-codex", ProviderConversationID: "thread-empty", WorkspacePath: t.TempDir(),
				FreshIfMissing: tt.freshIfMissing,
			})
			if !tt.freshIfMissing {
				if !errors.Is(err, ports.ErrChatResumeFailed) || !errors.Is(err, ports.ErrChatConversationMissing) {
					t.Fatalf("resume = %v, want ErrChatResumeFailed marked ErrChatConversationMissing", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resume: %v", err)
			}
			defer func() { _ = conv.Close() }()
			if got := conv.ProviderConversationID(); got != "thread-new" {
				t.Fatalf("thread = %q, want the new thread-new", got)
			}
		})
	}
}

// A thread/resume failure that does not say the thread is gone may succeed on a
// retry, so it is not marked missing (B678).
func TestCodexResumeFailureOtherThanMissingIsNotMarkedMissing(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.replyError("thread/resume", -32603, "transport hiccup")
	_, err := d.Resume(context.Background(), ports.ChatResumeConfig{
		SessionID: "codex-hiccup", ProviderConversationID: "thread-1", WorkspacePath: t.TempDir(),
	})
	if !errors.Is(err, ports.ErrChatResumeFailed) || errors.Is(err, ports.ErrChatConversationMissing) {
		t.Fatalf("resume = %v, want ErrChatResumeFailed without ErrChatConversationMissing", err)
	}
}
