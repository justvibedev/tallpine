package httpd

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
)

func TestDeferredAwakeBrowserRouteIsNotMounted(t *testing.T) {
	router := chi.NewRouter()
	mountAwake(router, &controllers.AwakeController{})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, loopbackRequest(http.MethodPost, "/api/v1/awake/browser", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("deferred browser route is mounted: status=%d", w.Code)
	}
}
