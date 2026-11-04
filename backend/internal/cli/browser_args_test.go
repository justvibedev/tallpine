package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func TestBrowserRequiresSessionAndValidWait(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "")
	if _, _, err := executeCLI(t, Deps{}, "browser", "status"); ExitCode(err) != 2 {
		t.Fatalf("status error = %v code=%d", err, ExitCode(err))
	}
	t.Setenv("TALLPINE_SESSION_ID", "tp-1")
	if _, _, err := executeCLI(t, Deps{}, "browser", "status"); ExitCode(err) != 2 {
		t.Fatalf("missing capability error = %v code=%d", err, ExitCode(err))
	}
	t.Setenv("TALLPINE_BROWSER_CAPABILITY", "capability-1")
	if _, _, err := executeCLI(t, Deps{}, "browser", "wait", "--text", "x", "--url", "y"); ExitCode(err) != 2 {
		t.Fatalf("wait error = %v code=%d", err, ExitCode(err))
	}
	if _, _, err := executeCLI(t, Deps{}, "browser", "wait", "--dom-stable", "5000", "--timeout", "1000"); ExitCode(err) != 2 {
		t.Fatalf("dom-stable timeout error = %v code=%d", err, ExitCode(err))
	}
	if _, _, err := executeCLI(t, Deps{}, "browser", "get"); ExitCode(err) != 2 {
		t.Fatalf("get error = %v code=%d", err, ExitCode(err))
	}
}

func TestBrowserPreservesDaemonErrorEnvelopeAndRequestID(t *testing.T) {
	setBrowserIdentity(t)
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"conflict","code":"SESSION_TERMINATED","message":"Session is terminated","requestId":"req-browser-1"}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "browser", "snapshot")
	if ExitCode(err) != 1 {
		t.Fatalf("exit code = %d, error = %v", ExitCode(err), err)
	}
	if !strings.Contains(err.Error(), "Session is terminated (SESSION_TERMINATED)") ||
		!strings.Contains(err.Error(), "[request req-browser-1]") {
		t.Fatalf("error did not preserve daemon envelope: %v", err)
	}
}

func TestBrowserViewportArguments(t *testing.T) {
	setBrowserIdentity(t)
	cfg := setConfigEnv(t)
	capture := &browserRequestCapture{}
	srv := browserCLIServer(t, capture)
	writeRunFileFor(t, cfg, srv)
	deps := Deps{ProcessAlive: func(int) bool { return true }}

	if _, _, err := executeCLI(t, deps, "browser", "viewport", "390x844", "--dpr", "2", "--mobile"); err != nil {
		t.Fatal(err)
	}
	a := capture.body.Args
	if capture.body.Action != "viewport" || a["width"] != float64(390) || a["height"] != float64(844) ||
		a["deviceScaleFactor"] != float64(2) || a["mobile"] != true {
		t.Fatalf("viewport = %#v", capture.body)
	}
	capture.body = apitypes.BrowserCommandRequest{} // decoding merges into a stale Args map
	if _, _, err := executeCLI(t, deps, "browser", "viewport", "1440x900", "--dpr", "0", "--mobile=false"); err != nil {
		t.Fatal(err)
	}
	if _, ok := capture.body.Args["deviceScaleFactor"]; ok || capture.body.Args["mobile"] != false {
		t.Fatalf("default viewport = %#v", capture.body)
	}
	if _, _, err := executeCLI(t, deps, "browser", "viewport", "reset"); err != nil {
		t.Fatal(err)
	}
	if capture.body.Action != "viewport" || capture.body.Args["reset"] != true {
		t.Fatalf("reset = %#v", capture.body)
	}
	for _, bad := range []string{"390", "0x10", "axb", "20000x10", "390x"} {
		if _, _, err := executeCLI(t, deps, "browser", "viewport", bad); err == nil {
			t.Fatalf("viewport %q accepted", bad)
		}
	}
	if _, _, err := executeCLI(t, deps, "browser", "viewport", "reset", "--mobile"); err == nil {
		t.Fatal("reset --mobile accepted")
	}
}
