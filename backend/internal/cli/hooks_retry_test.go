package cli

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHooks_RetryOnlyUncommittedActivityProjection(t *testing.T) {
	// Real 1s sleeps made the 5s budget race with machine load (B490).
	prevDelay := activityRetryDelay
	activityRetryDelay = time.Millisecond
	t.Cleanup(func() { activityRetryDelay = prevDelay })
	for _, tt := range []struct {
		name      string
		code      string
		failures  int32
		wantCalls int32
	}{
		{"transient contention", "ACTIVITY_PROJECTION_BUSY", 2, 3},
		{"persistent contention", "ACTIVITY_PROJECTION_BUSY", 9, 4},
		{"other unavailable error is not safe to repeat", "SERVICE_UNAVAILABLE", 2, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TALLPINE_SESSION_ID", "tp-7")
			cfg := setConfigEnv(t)
			var calls atomic.Int32
			payloads := make(chan string, 10)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				payloads <- string(body)
				if calls.Add(1) <= tt.failures {
					w.WriteHeader(http.StatusServiceUnavailable)
					_ = json.NewEncoder(w).Encode(map[string]string{"code": tt.code, "message": "try again", "requestId": "hook-request"})
					return
				}
				_, _ = io.WriteString(w, `{"ok":true}`)
			}))
			t.Cleanup(srv.Close)
			writeRunFileFor(t, cfg, srv)
			_, _, err := executeCLI(t, Deps{In: strings.NewReader(`{"session_id":"native-1","last_assistant_message":"answer"}`),
				ProcessAlive: func(int) bool { return true }}, "hooks", "claude-code", "stop")
			if err != nil || calls.Load() != tt.wantCalls {
				t.Fatalf("hook delivery = %d calls, %v; want %d", calls.Load(), err, tt.wantCalls)
			}
			first := <-payloads
			for i := int32(1); i < tt.wantCalls; i++ {
				if got := <-payloads; got != first {
					t.Fatalf("retry changed original signal: %s != %s", got, first)
				}
			}
			failure, logErr := os.ReadFile(filepath.Join(cfg.dataDir, "hooks.log"))
			if tt.failures < tt.wantCalls {
				if !errors.Is(logErr, fs.ErrNotExist) {
					t.Fatalf("successful retry logged a failed delivery: %s %v", failure, logErr)
				}
			} else if logErr != nil || !strings.Contains(string(failure), "hook-request") {
				t.Fatalf("exhausted retry lost request-correlated evidence: %s %v", failure, logErr)
			}
		})
	}
}
