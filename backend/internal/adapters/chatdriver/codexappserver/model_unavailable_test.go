package codexappserver

import (
	"strings"
	"testing"
)

// A ChatGPT account rejecting the model pinned in ~/.codex/config.toml arrives
// as an error notification carrying the 400 model_not_found body.
func TestCodexModelNotFoundNamesConfigToml(t *testing.T) {
	event := normalizeOne(t, "error", `{"threadId":"th1","turnId":"tu1","willRetry":false,"error":{"message":"{\"error\":{\"message\":\"The model `+"`gpt-6.1-sol`"+` does not exist or you do not have access to it.\",\"type\":\"invalid_request_error\",\"code\":\"model_not_found\"}}","codexErrorInfo":{"httpConnectionFailed":{"httpStatusCode":400}}}}`)
	if event.Err == nil {
		t.Fatal("no error event")
	}
	msg := event.Err.Error()
	for _, want := range []string{"gpt-6.1-sol", "not available for this account", "config.toml", "model_not_found", "If you did not pick this model", "choose another model"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}

func TestCodexUnrelatedErrorUnchanged(t *testing.T) {
	event := normalizeOne(t, "error", `{"threadId":"th1","turnId":"tu1","error":{"message":"stream disconnected"}}`)
	if event.Err == nil || strings.Contains(event.Err.Error(), "config.toml") {
		t.Fatalf("err = %v", event.Err)
	}
}

// The wording Codex 0.154 returned on Windows for a ChatGPT account (B725
// verification): the quoted name comes before "model", and "model is not
// supported" must not be read as a model called "is".
func TestCodexModelNotSupportedNamesQuotedModel(t *testing.T) {
	event := normalizeOne(t, "error", `{"threadId":"th1","turnId":"tu1","willRetry":false,"error":{"message":"{\"type\":\"error\",\"status\":400,\"error\":{\"type\":\"invalid_request_error\",\"message\":\"The 'gpt-6.1-sol' model is not supported when using Codex with a ChatGPT account.\",\"code\":\"model_not_found\"}}"}}`)
	if event.Err == nil {
		t.Fatal("no error event")
	}
	msg := event.Err.Error()
	if !strings.HasPrefix(msg, "Model gpt-6.1-sol is not available for this account.") {
		t.Errorf("error %q does not name gpt-6.1-sol first", msg)
	}
}

func TestCodexRejectedModelName(t *testing.T) {
	for text, want := range map[string]string{
		"The model `gpt-6.1-sol` does not exist or you do not have access to it.": "gpt-6.1-sol",
		"The 'gpt-6.1-sol' model is not supported when using Codex.":              "gpt-6.1-sol",
		`The "o4-mini" model is not supported.`:                                   "o4-mini",
		"unknown model: gpt-5.6-sol":                                              "gpt-5.6-sol",
		"The model is not supported for this account.":                            "",
		`{"code":"model_not_found"}`:                                              "",
	} {
		if got := codexRejectedModelName(text); got != want {
			t.Errorf("codexRejectedModelName(%q) = %q, want %q", text, got, want)
		}
	}
}
