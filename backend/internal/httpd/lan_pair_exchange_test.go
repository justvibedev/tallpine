package httpd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

// newPairingManager builds a LANManager with a device-token store and a
// pairing code, plus an inner handler that answers 200 to anything it is let
// through to.
func newPairingManager(t *testing.T, code string) (*LANManager, *mobilebridge.DeviceTokens) {
	t.Helper()
	tokens, err := mobilebridge.LoadDeviceTokens(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewLANManager(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }), &authState{}, 0, nil)
	m.SetDeviceTokens(tokens)
	if code != "" {
		m.SetPasswordHash(mobilebridge.HashPassword(code))
	}
	return m, tokens
}

func exchange(m *LANManager, from, bearer, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, lanPairExchangePath, strings.NewReader(body))
	r.RemoteAddr = from
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	m.dispatch().ServeHTTP(w, overTLS(r))
	return w
}

func tokenFrom(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("exchange = %d %s", w.Code, w.Body)
	}
	var resp pairExchangeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.Token == "" {
		t.Fatalf("bad exchange body %q: %v", w.Body, err)
	}
	return resp.Token
}

func callAPI(m *LANManager, from, token string) int {
	r := reqFrom(from, "Bearer "+token)
	w := httptest.NewRecorder()
	m.dispatch().ServeHTTP(w, r)
	return w.Code
}

func TestPairingCodeIsSingleUseAndIssuesWorkingDeviceToken(t *testing.T) {
	m, _ := newPairingManager(t, "Code1234")
	var hooks atomic.Int32
	m.SetPairedHook(func() { hooks.Add(1) })
	tok := tokenFrom(t, exchange(m, "192.168.1.20:5000", "", `{"code":"Code1234","installId":"i-1","deviceName":"Pixel"}`))
	if got := callAPI(m, "192.168.1.20:5001", tok); got != http.StatusOK {
		t.Fatalf("device token request = %d, want 200", got)
	}
	if w := exchange(m, "192.168.1.21:5000", "", `{"code":"Code1234","installId":"i-2"}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("second redeem of the same code = %d, want 401", w.Code)
	}
	deadline := time.Now().Add(time.Second)
	for hooks.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if hooks.Load() != 1 {
		t.Fatalf("paired hook ran %d times, want 1", hooks.Load())
	}
}

func TestPairingCodeIsNotABearer(t *testing.T) {
	m, _ := newPairingManager(t, "Code1234")
	if got := callAPI(m, "192.168.1.20:5000", "Code1234"); got != http.StatusUnauthorized {
		t.Fatalf("pairing code as bearer = %d, want 401", got)
	}
}

func TestWrongPairingCodeCountsTowardLockout(t *testing.T) {
	m, _ := newPairingManager(t, "Code1234")
	for range sourceFailureLimit {
		if w := exchange(m, "192.168.1.30:5000", "", `{"code":"nope","installId":"i-9"}`); w.Code != http.StatusUnauthorized {
			t.Fatalf("wrong code = %d, want 401", w.Code)
		}
	}
	if w := exchange(m, "192.168.1.30:5000", "", `{"code":"Code1234","installId":"i-9"}`); w.Code != http.StatusTooManyRequests {
		t.Fatalf("correct code after lockout = %d, want 429", w.Code)
	}
}

func TestLegacyBearerUpgradesInPlaceThenExpires(t *testing.T) {
	m, _ := newPairingManager(t, "")
	m.SetLegacyPassword(mobilebridge.HashPassword("oldpass1"), time.Now().Add(time.Hour))
	if got := callAPI(m, "192.168.1.20:5000", "oldpass1"); got != http.StatusOK {
		t.Fatalf("legacy bearer inside the window = %d, want 200", got)
	}
	tok := tokenFrom(t, exchange(m, "192.168.1.20:5000", "oldpass1", `{"installId":"i-1"}`))
	if got := callAPI(m, "192.168.1.20:5001", tok); got != http.StatusOK {
		t.Fatalf("upgraded token = %d, want 200", got)
	}
	m.SetRequireDevicePairing(true)
	m.SetLegacyPassword(mobilebridge.HashPassword("oldpass1"), time.Now().Add(-time.Second))
	r := reqFrom("192.168.1.20:5002", "Bearer oldpass1")
	w := httptest.NewRecorder()
	m.dispatch().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "LEGACY_PAIRING_EXPIRED") {
		t.Fatalf("expired legacy bearer = %d %s", w.Code, w.Body)
	}
	if w := exchange(m, "192.168.1.20:5003", "oldpass1", `{"installId":"i-1"}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("expired legacy upgrade = %d, want 401", w.Code)
	}
}

func TestRevokedDeviceTokenStopsWorking(t *testing.T) {
	m, tokens := newPairingManager(t, "Code1234")
	tok := tokenFrom(t, exchange(m, "192.168.1.20:5000", "", `{"code":"Code1234","installId":"i-1"}`))
	hashes, err := tokens.RevokeInstall("i-1")
	if err != nil || len(hashes) != 1 {
		t.Fatalf("revoke = %v %v", hashes, err)
	}
	if got := callAPI(m, "192.168.1.20:5001", tok); got != http.StatusUnauthorized {
		t.Fatalf("revoked token = %d, want 401", got)
	}
}

// A token without an install id could never be revoked from the device list.
func TestExchangeRequiresAnInstallIDAndKeepsTheCode(t *testing.T) {
	m, tokens := newPairingManager(t, "Code1234")
	for _, body := range []string{`{"code":"Code1234"}`, `{"code":"Code1234","installId":"   "}`} {
		if w := exchange(m, "192.168.1.20:5000", "", body); w.Code != http.StatusBadRequest {
			t.Fatalf("exchange %s = %d, want 400", body, w.Code)
		}
	}
	if tokens.Count() != 0 {
		t.Fatal("a token was issued without an install id")
	}
	// The refused attempt did not burn the code.
	tokenFrom(t, exchange(m, "192.168.1.20:5001", "", `{"code":"Code1234","installId":"i-1"}`))
}

// The code's lifetime is enforced when it is redeemed, with no desktop status
// poll in between (a code nobody is shown, or one left on a closed panel).
func TestPairingCodeExpiresAtRedeemTimeWithoutAStatusCall(t *testing.T) {
	armedAt := time.Unix(1_800_000_000, 0)
	now := armedAt
	m, tokens := newPairingManager(t, "")
	m.state.now = func() time.Time { return now }
	m.SetPasswordHash(mobilebridge.HashPassword("Code1234"))

	now = armedAt.Add(mobilebridge.PairingCodeTTL + time.Second)
	if w := exchange(m, "192.168.1.20:5000", "", `{"code":"Code1234","installId":"i-1"}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("code older than the TTL = %d, want 401", w.Code)
	}
	if tokens.Count() != 0 {
		t.Fatal("an expired code issued a token")
	}
	now = armedAt.Add(mobilebridge.PairingCodeTTL - time.Second)
	m.SetPasswordHash(mobilebridge.HashPassword("Fresh5678")) // a re-mint restarts the clock from now
	now = now.Add(mobilebridge.PairingCodeTTL - time.Second)
	tokenFrom(t, exchange(m, "192.168.1.20:5001", "", `{"code":"Fresh5678","installId":"i-1"}`))
}

// Clients retry the legacy upgrade on every connect; once the window is over
// those retries must not count as guesses, or re-scanning the QR from the same
// phone would hit the lockout.
func TestExpiredLegacyBearerAtExchangeDoesNotFeedTheLockout(t *testing.T) {
	m, _ := newPairingManager(t, "Code1234")
	m.SetRequireDevicePairing(true)
	m.SetLegacyPassword(mobilebridge.HashPassword("oldpass1"), time.Now().Add(-time.Hour))
	for range 3 * sourceFailureLimit {
		w := exchange(m, "192.168.1.20:5000", "oldpass1", `{"installId":"i-1"}`)
		if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "LEGACY_PAIRING_EXPIRED") {
			t.Fatalf("expired legacy upgrade = %d %s", w.Code, w.Body)
		}
	}
	tokenFrom(t, exchange(m, "192.168.1.20:5001", "", `{"code":"Code1234","installId":"i-1"}`))
}

// TestRepairOnAPairedConnectionGetsItsResponse: a paired phone that scans a new
// code sends the exchange over the keep-alive connection its polls already
// authenticated with the old token. Re-pairing replaces that token, and closing
// the replaced token's connections used to cut this very connection before the
// 200 was written: the phone saw "connection lost" (-1005), retried, and the
// spent code answered 401 PAIRING_CODE_USED.
func TestRepairOnAPairedConnectionGetsItsResponse(t *testing.T) {
	m, _ := newPairingManager(t, "Code1234")
	port, err := m.Start(0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop(context.Background())
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	// One connection, reused for every request, like the phone's pooled session.
	client := &http.Client{Transport: &http.Transport{MaxConnsPerHost: 1, MaxIdleConnsPerHost: 1}}
	defer client.CloseIdleConnections()
	var conns atomic.Int32
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		if !info.Reused {
			conns.Add(1)
		}
	}}
	do := func(method, path, bearer, body string) (*http.Response, error) {
		req, _ := http.NewRequestWithContext(httptrace.WithClientTrace(context.Background(), trace), method, base+path, strings.NewReader(body))
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		return client.Do(req)
	}
	pair := func(code string) string {
		t.Helper()
		resp, err := do(http.MethodPost, lanPairExchangePath, "", `{"code":"`+code+`","installId":"i-1"}`)
		if err != nil {
			t.Fatalf("exchange %s lost its response: %v", code, err)
		}
		defer resp.Body.Close()
		var out pairExchangeResponse
		if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&out) != nil || out.Token == "" {
			t.Fatalf("exchange %s = %d", code, resp.StatusCode)
		}
		return out.Token
	}
	old := pair("Code1234")
	resp, err := do(http.MethodGet, "/api/v1/sessions", old, "")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("poll with the first token: %v %v", resp, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	m.SetPasswordHash(mobilebridge.HashPassword("Code5678"))
	fresh := pair("Code5678")
	if conns.Load() != 1 {
		t.Fatalf("test setup: %d connections, want the exchange to reuse the poll's", conns.Load())
	}
	if got := callAPI(m, "192.168.1.20:5001", fresh); got != http.StatusOK {
		t.Fatalf("new token = %d, want 200", got)
	}
	if got := callAPI(m, "192.168.1.20:5001", old); got != http.StatusUnauthorized {
		t.Fatalf("replaced token = %d, want 401", got)
	}
}
