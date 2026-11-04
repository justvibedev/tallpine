package controllers_test

import (
	"fmt"
	"net/http"
	"testing"

	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

// A send steered into a running turn whose provider reply was lost may already
// be with the agent. A generic 500/503 invites a resend that delivers it twice,
// so the API says explicitly not to resend.
func TestSessionsAPI_SendWithUncertainSteerSaysDoNotResend(t *testing.T) {
	svc := newFakeSessionService()
	svc.sendErr = fmt.Errorf("send tp-1: %w: steer turn t1: transport lost", chatsvc.ErrSteerDeliveryUncertain)
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/tp-1/send", `{"message":"hold, do not push"}`)
	assertErrorCode(t, body, status, http.StatusConflict, "SEND_DELIVERY_UNCERTAIN")
}
