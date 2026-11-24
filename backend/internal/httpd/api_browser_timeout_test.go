package httpd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/browserruntime"
	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// browserBudgetProbe runs each command for runFor and reports the deadline it
// saw and whether its context outlived the run.
type browserBudgetProbe struct {
	runFor time.Duration
	budget chan time.Duration
	live   chan bool
}

func (p *browserBudgetProbe) Status(context.Context, domain.SessionID, string) (browserruntime.Status, string, error) {
	return browserruntime.Status{}, "", nil
}

func (p *browserBudgetProbe) Execute(ctx context.Context, _ domain.SessionID, _, action string, _ map[string]interface{}) (browserruntime.Result, string, error) {
	p.budget <- remainingRequestBudget(ctx)
	select {
	case <-time.After(p.runFor):
	case <-ctx.Done():
	}
	p.live <- ctx.Err() == nil
	if err := ctx.Err(); err != nil {
		return browserruntime.Result{}, action, err
	}
	return browserruntime.Result{RequestID: "r1", Value: map[string]any{"outcome": "continued"}}, action, nil
}

func browserCommand(t *testing.T, router http.Handler, action string) *httptest.ResponseRecorder {
	t.Helper()
	req := loopbackRequest(http.MethodPost, "/api/v1/browser/commands", bytes.NewBufferString(`{"sessionId":"tp-1","action":"`+action+`"}`))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	return response
}

// A handoff waits on the person far longer than one REST request (B858); every
// other browser command keeps the ordinary budget.
func TestBrowserHandoffOutlivesTheRequestTimeout(t *testing.T) {
	const requestTimeout = 250 * time.Millisecond
	probe := &browserBudgetProbe{runFor: 3 * requestTimeout, budget: make(chan time.Duration, 1), live: make(chan bool, 1)}
	router := NewRouterWithControl(config.Config{RequestTimeout: requestTimeout}, discardLogger(), nil, APIDeps{Browser: probe}, ControlDeps{})

	response := browserCommand(t, router, " Handoff ")
	if response.Code != http.StatusOK {
		t.Fatalf("handoff status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if budget := <-probe.budget; budget < 31*time.Minute {
		t.Fatalf("handoff budget = %s, want past the 30 minute handoff limit", budget)
	}
	if !<-probe.live {
		t.Fatal("the handoff was cut at the ordinary request timeout")
	}

	response = browserCommand(t, router, "snapshot")
	if budget := <-probe.budget; budget <= 0 || budget > requestTimeout {
		t.Fatalf("snapshot budget = %s, want at most %s", budget, requestTimeout)
	}
	if <-probe.live {
		t.Fatal("an ordinary browser command outlived the request timeout")
	}
	if response.Code != http.StatusGatewayTimeout {
		t.Fatalf("snapshot status = %d, want 504; body=%s", response.Code, response.Body.String())
	}
}
