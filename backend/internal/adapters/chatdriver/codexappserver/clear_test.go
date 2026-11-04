package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"tallpine.eu/tallpine/backend/internal/contextwindow"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func clearer(t *testing.T, conv ports.ChatConversation) ports.ChatContextClearer {
	t.Helper()
	c, ok := conv.(ports.ChatContextClearer)
	if !ok {
		t.Fatal("conversation does not implement ChatContextClearer")
	}
	return c
}

// /clear opens a new thread with the launch settings of the old one (model,
// effort, auto-compact window, approvals, standing instructions) and switches
// the conversation to it, for a fresh start and for a resumed thread alike.
func TestClearContextStartsFreshThreadWithSameSettings(t *testing.T) {
	env := map[string]string{contextwindow.CodexAutoCompactLimitEnv: "300000"}
	for _, method := range []string{"thread/start", "thread/resume"} {
		t.Run(method, func(t *testing.T) {
			d, srv := newTestDriver(t)
			var conv ports.ChatConversation
			var err error
			if method == "thread/start" {
				conv, err = d.Start(context.Background(), ports.ChatStartConfig{
					SessionID: "tp-1", WorkspacePath: "/tmp/ws", Model: "gpt-test", Effort: "high",
					Env: env, SystemPrompt: "standing rules", Permissions: ports.PermissionModeDefault,
				})
			} else {
				conv, err = d.Resume(context.Background(), ports.ChatResumeConfig{
					SessionID: "tp-1", ProviderConversationID: "thread-1", WorkspacePath: "/tmp/ws",
					Model: "gpt-test", Effort: "high", Env: env, SystemPrompt: "standing rules",
					Permissions: ports.PermissionModeDefault,
				})
			}
			if err != nil {
				t.Fatalf("%s: %v", method, err)
			}
			defer func() { _ = conv.Close() }()
			first := srv.awaitFrame(func(f frame) bool { return f.Method == method })
			srv.respondSequence("thread/start", `{"thread":{"id":"thread-2"},"model":"gpt-test"}`)

			var committed string
			if err := clearer(t, conv).ClearContext(context.Background(), func(id string) error {
				committed = id
				if got := conv.ProviderConversationID(); got != "thread-1" {
					t.Errorf("conversation switched before commit: %q", got)
				}
				return nil
			}); err != nil {
				t.Fatalf("ClearContext: %v", err)
			}
			if committed != "thread-2" || conv.ProviderConversationID() != "thread-2" {
				t.Fatalf("committed %q, now on %q; want thread-2", committed, conv.ProviderConversationID())
			}
			second := srv.awaitFrame(func(f frame) bool { return f.Method == "thread/start" })
			want := decodeParams(t, first.Params)
			delete(want, "threadId")
			if got := decodeParams(t, second.Params); !reflect.DeepEqual(got, want) {
				t.Fatalf("replacement thread/start params = %v, want the launch settings %v", got, want)
			}
		})
	}
}

// A failed durable rebind must leave the conversation on the old thread, so the
// in-memory id never runs ahead of what restore would load.
func TestClearContextKeepsOldThreadWhenCommitFails(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{SessionID: "tp-1", WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conv.Close() }()
	state := conv.(*conversation)
	state.sendMu.Lock()
	state.fastTier, state.planMode, state.tierUnknown, state.planUnknown = true, true, true, true
	state.threadModel, state.threadEffort = "gpt-old", "high"
	state.sendMu.Unlock()
	srv.respondSequence("thread/start", `{"thread":{"id":"thread-2"},"model":"gpt-new","reasoningEffort":"low"}`)
	boom := errors.New("disk full")
	if err := clearer(t, conv).ClearContext(context.Background(), func(string) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("ClearContext = %v, want the commit error", err)
	}
	if got := conv.ProviderConversationID(); got != "thread-1" {
		t.Fatalf("conversation moved to %q despite a failed commit", got)
	}
	state.sendMu.Lock()
	defer state.sendMu.Unlock()
	if !state.fastTier || !state.planMode || !state.tierUnknown || !state.planUnknown ||
		state.threadModel != "gpt-old" || state.threadEffort != "high" {
		t.Fatalf("failed commit changed thread state: fast=%v plan=%v tierUnknown=%v planUnknown=%v model=%q effort=%q",
			state.fastTier, state.planMode, state.tierUnknown, state.planUnknown, state.threadModel, state.threadEffort)
	}
}

// The reconnect path rebuilds the replacement thread's parameters without the
// launch handshake; they must equal what a fresh launch with the same settings sends.
func TestResumeFreshThreadMatchesLaunchParams(t *testing.T) {
	env := map[string]string{contextwindow.CodexAutoCompactLimitEnv: "300000"}
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{
		SessionID: "tp-1", WorkspacePath: "/tmp/ws", Model: "gpt-test", Effort: "high",
		Env: env, SystemPrompt: "standing rules", Permissions: ports.PermissionModeDefault,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conv.Close() }()
	first := srv.awaitFrame(func(f frame) bool { return f.Method == "thread/start" })
	got := resumeFreshThread(ports.ChatResumeConfig{
		WorkspacePath: "/tmp/ws", Model: "gpt-test", Effort: "high",
		Env: env, SystemPrompt: "standing rules", Permissions: ports.PermissionModeDefault,
	})
	raw, _ := json.Marshal(got)
	if want := decodeParams(t, first.Params); !reflect.DeepEqual(decodeParams(t, raw), want) {
		t.Fatalf("resumeFreshThread = %s, launch sent %v", raw, want)
	}
}

func decodeParams(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
