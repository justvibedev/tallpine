package httpd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// P2-L1: a device token skips a spent global lockout; a known install id
// presented alongside a wrong credential does not.
func TestDeviceTokenPassesSpentLockoutButInstallIDDoesNot(t *testing.T) {
	m, tokens := newPairingManager(t, "Code1234")
	tok, _, _ := tokens.Issue("phone-1", "")
	for i := range globalFailureLimit {
		m.dispatch().ServeHTTP(httptest.NewRecorder(), reqFrom(fmt.Sprintf("10.4.%d.%d:5000", i/250, i%250+1), "Bearer wrong"))
	}
	if got := callAPI(m, "100.80.1.9:5000", tok); got != http.StatusOK {
		t.Fatalf("device token during a spent budget = %d, want 200", got)
	}
	r := reqFrom("100.80.1.10:5000", "Bearer wrong")
	r.Header.Set(InstallIDHeader, "phone-1")
	w := httptest.NewRecorder()
	m.dispatch().ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("install id with a bad credential during a spent budget = %d, want 429", w.Code)
	}
}

func TestRetiredPairingHashesAreCapped(t *testing.T) {
	st := &authState{}
	for i := range 50 {
		st.setHash(fmt.Sprintf("hash-%d", i))
	}
	if got := len(st.hashes.Load().retired); got != maxRetiredHashes {
		t.Fatalf("retired hashes = %d, want %d", got, maxRetiredHashes)
	}
}
