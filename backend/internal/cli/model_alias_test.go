package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateModelResolvesClaudeAlias(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"agentId":"claude-code","allowCustom":true,"models":[{"id":"claude-haiku-4-5"},{"id":"claude-haiku-5-5"},{"id":"claude-sonnet-5-5"}]}`))
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	c := &commandContext{deps: Deps{}.withDefaults()}
	for in, want := range map[string]string{"haiku": "claude-haiku-5-5", "Sonnet": "claude-sonnet-5-5", "sonnet[1m]": "claude-sonnet-5-5[1m]", "SONNET[1M]": "claude-sonnet-5-5[1m]", "claude-haiku-4-5": "claude-haiku-4-5"} {
		got, err := c.validateModel(t.Context(), "claude-code", in, "")
		if err != nil || got != want {
			t.Errorf("%s -> %q, %v; want %q", in, got, err, want)
		}
	}
	_, err := c.validateModel(t.Context(), "claude-code", "opus", "")
	if err == nil || !strings.Contains(err.Error(), "valid model IDs: claude-haiku-4-5") {
		t.Errorf("alias without catalog model should fail listing ids, got %v", err)
	}
	if _, err := c.validateModel(t.Context(), "claude-code", "bogus", ""); err == nil {
		t.Error("unknown model accepted")
	}
}
