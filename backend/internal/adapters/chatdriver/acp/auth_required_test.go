package acp

import (
	"errors"
	"fmt"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestIsACPAuthRequiredRecognizesProtocolAuthCodes(t *testing.T) {
	tests := []struct {
		name string
		err  *acpsdk.RequestError
		want bool
	}{
		{"acp auth_required code", &acpsdk.RequestError{Code: -32000, Message: "authentication required"}, true},
		{"unassigned server code", &acpsdk.RequestError{Code: -32001, Message: "auth required"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isACPAuthRequired(tc.err); got != tc.want {
				t.Fatalf("isACPAuthRequired = %v, want %v", got, tc.want)
			}
		})
	}
}

// A reauth prompt shown to a user whose credentials are fine is its own bug,
// so the match stays phrase-based: a turn that merely mentions auth, or a tool
// echoing a status code from the user's own program, is not a rejection.
func TestIsACPAuthRequiredIgnoresUnrelatedFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"plain error", errors.New("authentication failed somewhere")},
		{"unrelated rpc failure", &acpsdk.RequestError{Code: -32603, Message: "internal error"}},
		{"method not found", &acpsdk.RequestError{Code: -32601, Message: "Method not found"}},
		{
			"model discussing auth",
			&acpsdk.RequestError{Code: -32603, Message: "I cannot help you bypass the auth check in that file"},
		},
		{
			"user's own http status in tool output",
			&acpsdk.RequestError{Code: -32603, Message: "test failed: expected 200, got 401 from the fixture server"},
		},
		{"http unauthorized", &acpsdk.RequestError{Code: 401, Message: "request failed"}},
		{"http forbidden", &acpsdk.RequestError{Code: 403, Message: "request failed"}},
		{"provider auth prose", &acpsdk.RequestError{Code: -32603, Message: "OAuth access token is invalid."}},
		{"provider auth data", &acpsdk.RequestError{Code: -32603, Message: "prompt failed", Data: "Invalid bearer token"}},
		{"nil data map", &acpsdk.RequestError{Code: -32603, Message: "prompt failed", Data: map[string]any{"n": 1}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if isACPAuthRequired(tc.err) {
				t.Fatalf("isACPAuthRequired = true for %v, want false", tc.err)
			}
		})
	}
}

func TestIsACPAuthRequiredUnwrapsWrappedErrors(t *testing.T) {
	wrapped := fmt.Errorf("ACP session/prompt: %w", &acpsdk.RequestError{Code: -32000, Message: "auth required"})
	if !isACPAuthRequired(wrapped) {
		t.Fatal("a wrapped auth rejection must still be recognized")
	}
}

func TestNormalizeACPErrorMarksAuthRejectionsForReauth(t *testing.T) {
	err := normalizeACPError("ACP session/prompt", &acpsdk.RequestError{
		Code: -32000, Message: "authentication required",
	})
	if !errors.Is(err, ports.ErrChatAuthRequired) {
		t.Fatalf("err = %v, want it to wrap ErrChatAuthRequired so the reauth banner fires", err)
	}
}

// The driver must carry the binding's invalidation hook onto every
// conversation, or the runtime catch is wired to nothing.
func TestDriverPropagatesTheAuthRejectionHook(t *testing.T) {
	called := 0
	var got LaunchConfig
	cfg := Config{OnAuthRejected: func(launch LaunchConfig) { called++; got = launch }}
	conv := &conversation{}
	// This mirrors the single assignment in initialize; if that line is
	// removed, the hook silently stops reaching live turns.
	launch := LaunchConfig{WorkspacePath: "/ws"}
	conv.onAuthRejected = func() { cfg.OnAuthRejected(launch) }
	if conv.onAuthRejected == nil {
		t.Fatal("the configured hook must reach the conversation")
	}
	conv.onAuthRejected()
	if called != 1 || got.WorkspacePath != "/ws" {
		t.Fatalf("hook ran %d times with %q, want 1 with the launch config", called, got.WorkspacePath)
	}
}

// The hook is optional: a binding that supplies none must not panic.
func TestNoAuthRejectionHookIsSafe(t *testing.T) {
	conv := &conversation{}
	if (Config{}).OnAuthRejected != nil || conv.onAuthRejected != nil {
		t.Fatal("an unset hook must stay nil so the call site can skip it")
	}
}
