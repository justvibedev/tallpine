package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestSend_RelayOwnerMessageRidesTheRequest(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "orch-1")
	cfg := setConfigEnv(t)
	srv, capture := sendServer(t, http.StatusOK, `{"ok":true,"sessionId":"site-2","message":"x"}`)
	writeRunFileFor(t, cfg, srv)
	if _, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"send", "--session", "site-2", "--message", "owner approved publishing", "--relay-owner-message", " last "); err != nil {
		t.Fatalf("send: %v\n%s", err, errOut)
	}
	var req struct {
		Relay string `json:"relayOwnerMessage"`
	}
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil || req.Relay != "last" {
		t.Fatalf("relayOwnerMessage = %q err %v body %s", req.Relay, err, capture.body)
	}
}

func TestSend_RelayOwnerMessageRefusesSteer(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "orch-1")
	setConfigEnv(t)
	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"send", "--session", "site-2", "--steer", "--message", "x", "--relay-owner-message", "last")
	if err == nil || !strings.Contains(err.Error(), "--relay-owner-message cannot be combined with --steer") {
		t.Fatalf("err = %v; want a usage error", err)
	}
}
