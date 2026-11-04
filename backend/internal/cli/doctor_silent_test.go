package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func decodeSilent(t *testing.T, body string) silentFailuresResponse {
	t.Helper()
	var resp silentFailuresResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestDoctorSilentFailuresHealthyPassesEveryCheck(t *testing.T) {
	checks := silentFailureChecks(decodeSilent(t, `{"healthy":true,"chatNotAttached":[],"reportBatches":[],"turnFallbacks":[]}`))
	if len(checks) != 3 {
		t.Fatalf("checks = %+v, want 3", checks)
	}
	for _, c := range checks {
		if c.Level != doctorPass {
			t.Fatalf("healthy check = %+v, want PASS", c)
		}
	}
}

func TestDoctorSilentFailuresEachConditionWarnsWithFixHint(t *testing.T) {
	cases := []struct {
		name, body, check, want, hint string
	}{
		{"chat", `{"chatNotAttached":[{"sessionId":"mer-3"}]}`, "chat-agents", "mer-3", "send the session a message"},
		{"reports pending", `{"reportBatches":[{"projectId":"proj","pending":2,"claimed":0,"undeliverable":false}]}`, "report-delivery", "proj (pending=2 claimed=0)", "start the project's orchestrator"},
		{"reports claimed", `{"reportBatches":[{"projectId":"proj","pending":0,"claimed":1,"undeliverable":false}]}`, "report-delivery", "claimed=1", "flush pending reports"},
		{"reports unconfirmed", `{"reportBatches":[{"projectId":"proj","pending":0,"claimed":0,"undeliverable":true}]}`, "report-delivery", "undeliverable", "orchestrator"},
		{"fallbacks", `{"turnFallbacks":[{"id":"f","sessionId":"mer-9"}]}`, "turn-fallbacks", "mer-9", "tallpine stop"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checks := silentFailureChecks(decodeSilent(t, tc.body))
			for _, c := range checks {
				if c.Name != tc.check {
					if c.Level != doctorPass {
						t.Fatalf("unrelated check %s = %+v, want PASS", c.Name, c)
					}
					continue
				}
				if c.Level != doctorWarn || !strings.Contains(c.Message, tc.want) || !strings.Contains(c.Message, tc.hint) {
					t.Fatalf("%s = %+v, want WARN containing %q and hint %q", c.Name, c, tc.want, tc.hint)
				}
			}
		})
	}
}

func TestDoctorChecksSilentFailuresFromDaemon(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/health/silent-failures" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"healthy":false,"chatNotAttached":[{"sessionId":"mer-1"}],"reportBatches":[],"turnFallbacks":[]}`)
	}))
	defer srv.Close()
	writeRunFileFor(t, cfg, srv)
	c := doctorContext(t, map[string]string{}, nil)
	c.deps.ProcessAlive = func(int) bool { return true }
	checks := c.checkSilentFailures(t.Context())
	if got := findDoctorCheck(t, checks, "chat-agents"); got.Level != doctorWarn || !strings.Contains(got.Message, "mer-1") {
		t.Fatalf("chat-agents = %+v", got)
	}
	if got := findDoctorCheck(t, checks, "report-delivery"); got.Level != doctorPass {
		t.Fatalf("report-delivery = %+v", got)
	}
}
