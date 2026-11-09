package controllers_test

import (
	"net/http"
	"testing"
)

// TestSessionsAPI_ClaimPRHasNoBranchGate pins the route behind
// `tallpine session claim-pr`: the worker prompt sends custom-branch sessions
// here for names outside their namespace, so it must not gate on the head branch.
func TestSessionsAPI_ClaimPRHasNoBranchGate(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/tp-1/pr/claim", `{"pr":"142"}`)
	if status != http.StatusOK {
		t.Fatalf("claim = %d body=%s", status, body)
	}
	if svc.lastClaimOptions.RequireSessionBranch || svc.lastClaimOptions.RefuseOtherSessionBranch {
		t.Fatalf("explicit claim set a branch gate: %+v", svc.lastClaimOptions)
	}
}
