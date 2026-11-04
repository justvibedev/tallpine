package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func TestSendCarriesCooperativeSessionIdentity(t *testing.T) {
	for _, sender := range []string{"", "project-1"} {
		t.Run("sender="+sender, func(t *testing.T) {
			t.Setenv("TALLPINE_SESSION_ID", sender)
			cfg := setConfigEnv(t)
			srv, capture := sendServer(t, http.StatusOK, `{"ok":true}`)
			t.Cleanup(srv.Close)
			writeRunFileFor(t, cfg, srv)
			_, stderr, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "send", "--session", "demo-1", "--message", "continue")
			if err != nil {
				t.Fatalf("err=%v stderr=%s", err, stderr)
			}
			var req apitypes.SendSessionMessageRequest
			if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
				t.Fatal(err)
			}
			if req.SenderSessionID != sender || req.UserAuthored != (sender == "") {
				t.Fatalf("request=%+v", req)
			}
		})
	}
}

func TestSend_PrintsDeliveryOutcome(t *testing.T) {
	for _, tc := range []struct {
		name, resp, want string
	}{
		{"delivered", `{"ok":true,"outcome":"delivered","deliveryId":"session-send:abc"}`, "delivered to demo-1 (id session-send:abc)"},
		{"queued", `{"ok":true,"outcome":"queued","deliveryId":"turn-7"}`, "queued for next turn in demo-1 (id turn-7)"},
		{"steered past an open question", `{"ok":true,"outcome":"delivered","deliveryId":"act-1","waitingOnInput":true}`, "delivered to demo-1 (id act-1) into its running turn, which was waiting on an in-chat question"},
		{"queued behind an open question", `{"ok":true,"outcome":"queued","deliveryId":"turn-8","waitingOnInput":true}`, "NOT delivered yet: its running turn is waiting on an in-chat question"},
		{"duplicate", `{"ok":true,"outcome":"duplicate"}`, "not sent to demo-1: a message with this id was already accepted"},
		{"older daemon", `{"ok":true}`, "sent to demo-1; delivery outcome not reported by the daemon"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TALLPINE_SESSION_ID", "")
			cfg := setConfigEnv(t)
			srv, _ := sendServer(t, http.StatusOK, tc.resp)
			writeRunFileFor(t, cfg, srv)

			out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
				"send", "--session", "demo-1", "--message", "hi")
			if err != nil {
				t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("stdout = %q, want to contain %q", out, tc.want)
			}
		})
	}
}
