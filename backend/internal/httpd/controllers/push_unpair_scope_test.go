package controllers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

// scopedPushRegistry is a PushRegistry that also scopes unpairs by credential.
type scopedPushRegistry struct {
	fakePushRegistry
	scopedIDs   []string
	scopedCreds []string
}

func (s *scopedPushRegistry) UnpairFrom(ctx context.Context, id string) error {
	cred, _ := mobilebridge.CredentialFrom(ctx)
	s.scopedIDs = append(s.scopedIDs, id)
	s.scopedCreds = append(s.scopedCreds, cred)
	return nil
}

// The phone's unpair must take the credential-scoped path; the unscoped Unpair
// revokes every token of the install.
func TestPhoneUnpairRoutesThroughTheCredentialScopedPath(t *testing.T) {
	reg := &scopedPushRegistry{}
	r := chi.NewRouter()
	(&controllers.PushController{Registry: reg}).Register(r)

	req := httptest.NewRequest(http.MethodDelete, "/push/pairings/install-1", nil)
	req = req.WithContext(mobilebridge.WithCredential(req.Context(), mobilebridge.LegacyCredential))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("unpair = %d %s, want 204", w.Code, w.Body)
	}
	if len(reg.unpaired) != 0 {
		t.Fatalf("unscoped Unpair ran for %v", reg.unpaired)
	}
	if len(reg.scopedIDs) != 1 || reg.scopedIDs[0] != "install-1" || reg.scopedCreds[0] != mobilebridge.LegacyCredential {
		t.Fatalf("UnpairFrom got ids %v creds %v, want install-1 with the request credential", reg.scopedIDs, reg.scopedCreds)
	}
}
