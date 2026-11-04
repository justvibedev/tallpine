package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
)

type hibernationSettingsService struct {
	SettingsService
	policy domain.ChatHibernationPolicy
	calls  int
}

func (s *hibernationSettingsService) SetChatHibernation(_ context.Context, update settingssvc.ChatHibernationUpdate) (settingssvc.Snapshot, error) {
	if s.calls == 0 && s.policy == (domain.ChatHibernationPolicy{}) {
		s.policy = domain.DefaultChatHibernationPolicy()
	}
	s.calls++
	if update.Enabled != nil {
		s.policy.Enabled = *update.Enabled
	}
	if update.WorkerIdle != nil {
		if !domain.ValidChatHibernationIdle(*update.WorkerIdle) {
			return settingssvc.Snapshot{}, settingssvc.ErrInvalidChatHibernation
		}
		s.policy.WorkerIdle = *update.WorkerIdle
	}
	if update.OrchestratorIdle != nil {
		s.policy.OrchestratorIdle = *update.OrchestratorIdle
	}
	return settingssvc.Snapshot{ChatHibernation: s.policy}, nil
}
func (s *hibernationSettingsService) ChatHarnesses([]domain.AgentHarness) []domain.AgentHarness {
	return nil
}
func (s *hibernationSettingsService) Offering() settingssvc.Offering { return settingssvc.Offering{} }

func TestSettingsChatHibernationValidatesPatch(t *testing.T) {
	svc := &hibernationSettingsService{}
	c := &SettingsController{Svc: svc}
	for _, body := range []string{`{}`, `{"enabled":null}`, `{"enabled":"true"}`, `{"workerIdleSeconds":"60"}`} {
		w := httptest.NewRecorder()
		c.setChatHibernation(w, loopbackRequest(http.MethodPatch, "/api/v1/settings/chat-hibernation", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest || svc.calls != 0 {
			t.Fatalf("body=%s status=%d calls=%d", body, w.Code, svc.calls)
		}
	}
	for _, enabled := range []bool{true, false} {
		body := `{"enabled":false}`
		if enabled {
			body = `{"enabled":true}`
		}
		w := httptest.NewRecorder()
		c.setChatHibernation(w, loopbackRequest(http.MethodPatch, "/api/v1/settings/chat-hibernation", strings.NewReader(body)))
		var got SettingsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK || got.ChatHibernationEnabled != enabled {
			t.Fatalf("body=%s status=%d response=%+v err=%v", body, w.Code, got, err)
		}
	}
	w := httptest.NewRecorder()
	c.setChatHibernation(w, loopbackRequest(http.MethodPatch, "/api/v1/settings/chat-hibernation", strings.NewReader(`{"workerIdleSeconds":600}`)))
	var got SettingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK ||
		got.ChatHibernationWorkerIdleSeconds != 600 || got.ChatHibernationOrchestratorIdleSeconds != 3600 {
		t.Fatalf("threshold patch status=%d response=%+v err=%v", w.Code, got, err)
	}
	// A value that would overflow a Duration is refused before conversion.
	calls := svc.calls
	for _, body := range []string{`{"workerIdleSeconds":9223372036854775807}`, `{"orchestratorIdleSeconds":-1}`} {
		w = httptest.NewRecorder()
		c.setChatHibernation(w, loopbackRequest(http.MethodPatch, "/api/v1/settings/chat-hibernation", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest || svc.calls != calls {
			t.Fatalf("body=%s status=%d calls=%d", body, w.Code, svc.calls)
		}
	}
	w = httptest.NewRecorder()
	c.setChatHibernation(w, loopbackRequest(http.MethodPatch, "/api/v1/settings/chat-hibernation", strings.NewReader(`{"workerIdleSeconds":5}`)))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "CHAT_HIBERNATION_INVALID") {
		t.Fatalf("out-of-range threshold status=%d body=%s", w.Code, w.Body.String())
	}
}

type versionRefreshSettingsService struct {
	hibernationSettingsService
	refresh bool
}

func (s *versionRefreshSettingsService) SetVersionRefresh(_ context.Context, enabled bool) (settingssvc.Snapshot, error) {
	s.refresh = enabled
	s.calls++
	return settingssvc.Snapshot{VersionRefresh: enabled}, nil
}

func TestSettingsVersionRefreshRequiresBoolean(t *testing.T) {
	svc := &versionRefreshSettingsService{}
	c := &SettingsController{Svc: svc}
	for _, body := range []string{`{}`, `{"enabled":null}`, `{"enabled":"false"}`} {
		w := httptest.NewRecorder()
		c.setVersionRefresh(w, loopbackRequest(http.MethodPatch, "/api/v1/settings/version-refresh", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest || svc.calls != 0 {
			t.Fatalf("body=%s status=%d calls=%d", body, w.Code, svc.calls)
		}
	}
	w := httptest.NewRecorder()
	c.setVersionRefresh(w, loopbackRequest(http.MethodPatch, "/api/v1/settings/version-refresh", strings.NewReader(`{"enabled":false}`)))
	var got SettingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK || got.VersionRefresh || svc.refresh {
		t.Fatalf("status=%d response=%+v err=%v", w.Code, got, err)
	}
}
