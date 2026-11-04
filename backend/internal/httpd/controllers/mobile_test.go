package controllers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeBridge struct{ enabled bool }

func (f *fakeBridge) Status() MobileStatusResponse {
	return MobileStatusResponse{Enabled: f.enabled, Host: "192.168.1.42", Port: 3011}
}
func (f *fakeBridge) StartRemoteAccess() (MobileStatusResponse, error) {
	return MobileStatusResponse{}, nil
}

func (f *fakeBridge) Enable() (MobileStatusResponse, error) {
	f.enabled = true
	r := f.Status()
	r.Password = "abcd1234"
	return r, nil
}
func (f *fakeBridge) EnableLANOnly() (MobileStatusResponse, error)    { return f.Enable() }
func (f *fakeBridge) EnableTunnelOnly() (MobileStatusResponse, error) { return f.Enable() }
func (f *fakeBridge) Disable() error                                  { f.enabled = false; return nil }
func (f *fakeBridge) Regenerate() (MobileStatusResponse, error) {
	r := f.Status()
	r.Password = "wxyz5678"
	return r, nil
}
func (f *fakeBridge) SetKeepAwake(on bool) (MobileStatusResponse, error) {
	r := f.Status()
	r.KeepAwake.Enabled = on
	return r, nil
}
func (f *fakeBridge) SetKeepAwakeLid(bool, bool) (MobileStatusResponse, error) {
	return MobileStatusResponse{}, errors.New("closed-lid wakefulness unavailable")
}
func (f *fakeBridge) SetSecurePairing(on bool) (MobileStatusResponse, error) {
	r := f.Status()
	r.SecurePairing.Enabled = on
	return r, nil
}
func (f *fakeBridge) SetRequireDevicePairing(on bool) (MobileStatusResponse, error) {
	r := f.Status()
	r.RequireDevicePairing = on
	return r, nil
}
func (f *fakeBridge) SetBrowserRemoteAccess(on bool) (MobileStatusResponse, error) {
	r := f.Status()
	r.BrowserRemoteAccess = on
	return r, nil
}

func (f *fakeBridge) SetFeatureEnabled(on bool) (MobileStatusResponse, error) {
	r := f.Status()
	r.FeatureEnabled = on
	return r, nil
}
func (f *fakeBridge) SetPublicBaseURL(raw string) (MobileStatusResponse, error) {
	r := f.Status()
	r.PublicBaseURL = raw
	return r, nil
}
func (f *fakeBridge) SetQuickTunnel(on bool) (MobileStatusResponse, error) {
	r := f.Status()
	r.QuickTunnel = on
	return r, nil
}

func TestMobileBrowserRemoteAccess(t *testing.T) {
	c := &MobileController{Bridge: &fakeBridge{}}
	req := loopbackRequest(http.MethodPost, "/api/v1/mobile/browser-control", strings.NewReader(`{"enabled":true}`))
	rr := httptest.NewRecorder()
	c.BrowserRemoteAccess(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var got MobileStatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.BrowserRemoteAccess {
		t.Fatal("browser remote access was not enabled")
	}
}

func TestMobileEnableReturnsPassword(t *testing.T) {
	c := &MobileController{Bridge: &fakeBridge{}}
	w := httptest.NewRecorder()
	c.Enable(w, loopbackRequest(http.MethodPost, "/api/v1/mobile/enable", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
	var got MobileStatusResponse
	json.NewDecoder(w.Body).Decode(&got)
	if !got.Enabled || got.Password != "abcd1234" || got.Warning == "" {
		t.Fatalf("bad response: %+v", got)
	}
}

func TestMobileKeepAwakeRoute(t *testing.T) {
	c := &MobileController{Bridge: &fakeBridge{}}

	w := httptest.NewRecorder()
	c.KeepAwake(w, loopbackRequest(http.MethodPost, "/api/v1/mobile/keep-awake", bytes.NewBufferString(`{"enabled":true}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
	var got MobileStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.KeepAwake.Enabled {
		t.Fatalf("bad response: %+v", got.KeepAwake)
	}

	w = httptest.NewRecorder()
	c.KeepAwake(w, loopbackRequest(http.MethodPost, "/api/v1/mobile/keep-awake", bytes.NewBufferString(`not json`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid body: got %d", w.Code)
	}
}

func TestLidRequestRejectsUnavailableBridge(t *testing.T) {
	c := &MobileController{Bridge: &fakeBridge{}}
	w := httptest.NewRecorder()
	c.KeepAwake(w, loopbackRequest(http.MethodPost, "/api/v1/mobile/keep-awake", bytes.NewBufferString(`{"enabled":true,"lidClosed":true}`)))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", w.Code)
	}
}
