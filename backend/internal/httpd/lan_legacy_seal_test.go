package httpd

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

func TestSealedLegacyPasswordAuthenticatesOnlyWithTheInstallKey(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	sealed := mobilebridge.SealLegacyHash(key, mobilebridge.HashPassword("Ab3dEf9h"))
	m := NewLANManager(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }), &authState{}, 0, nil)
	m.SetLegacyPassword(sealed, time.Now().Add(time.Hour))
	call := func() int {
		w := httptest.NewRecorder()
		m.dispatch().ServeHTTP(w, shellRequest("192.168.1.20:5000", "/api/v1/sessions", "Ab3dEf9h"))
		return w.Code
	}
	if got := call(); got != http.StatusUnauthorized {
		t.Fatalf("sealed hash without a key = %d, want 401", got)
	}
	m.SetLegacyKey(key)
	if got := call(); got != http.StatusOK {
		t.Fatalf("sealed hash with its key = %d, want 200", got)
	}
}
