package controllers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
)

type harnessDefaultSettingsService struct {
	hibernationSettingsService
	defaults map[string]domain.HarnessDefault
}

func (s *harnessDefaultSettingsService) Get(context.Context) (settingssvc.Snapshot, error) {
	return settingssvc.Snapshot{HarnessDefaults: s.defaults}, nil
}

func (s *harnessDefaultSettingsService) SetHarnessDefault(_ context.Context, agent domain.AgentHarness, value domain.HarnessDefault) (settingssvc.Snapshot, error) {
	if agent != domain.HarnessCodex {
		return settingssvc.Snapshot{}, settingssvc.ErrUnknownHarness
	}
	s.defaults[string(agent)] = value
	return settingssvc.Snapshot{HarnessDefaults: s.defaults}, nil
}

func TestSettingsHarnessDefaultPatchRequiresDisabledModels(t *testing.T) {
	svc := &harnessDefaultSettingsService{defaults: map[string]domain.HarnessDefault{}}
	c := &SettingsController{Svc: svc}
	patch := func(agent, body string) int {
		router := chi.NewRouter()
		router.Patch("/api/v1/settings/harness-defaults/{agent}", c.setHarnessDefault)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, loopbackRequest(http.MethodPatch, "/api/v1/settings/harness-defaults/"+agent, strings.NewReader(body)))
		return w.Code
	}
	for _, body := range []string{`{}`, `{"model":"gpt-5","effort":"low"}`} {
		if code := patch("codex", body); code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d", body, code)
		}
	}
	if code := patch("not-an-agent", `{"disabledModels":[]}`); code != http.StatusNotFound {
		t.Fatalf("unknown harness status=%d", code)
	}
	if code := patch("codex", `{"disabledModels":["gpt-5","gpt-5-mini"]}`); code != http.StatusOK {
		t.Fatalf("disabled models status=%d", code)
	}
	want := domain.HarnessDefault{DisabledModels: []string{"gpt-5", "gpt-5-mini"}}
	if got := svc.defaults["codex"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("after disabled models = %+v", got)
	}
}

// The models panel and the compaction default save independently: a patch
// that leaves a field out must keep its saved value.
func TestSettingsHarnessDefaultPatchKeepsFieldsLeftOut(t *testing.T) {
	svc := &harnessDefaultSettingsService{defaults: map[string]domain.HarnessDefault{}}
	c := &SettingsController{Svc: svc}
	router := chi.NewRouter()
	router.Patch("/api/v1/settings/harness-defaults/{agent}", c.setHarnessDefault)
	patch := func(body string) {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, loopbackRequest(http.MethodPatch, "/api/v1/settings/harness-defaults/codex", strings.NewReader(body)))
		if w.Code != http.StatusOK {
			t.Fatalf("body=%s status=%d", body, w.Code)
		}
	}
	patch(`{"autoCompactWindow":"70%"}`)
	patch(`{"disabledModels":["gpt-5"]}`)
	want := domain.HarnessDefault{DisabledModels: []string{"gpt-5"}, AutoCompactWindow: "70%"}
	if got := svc.defaults["codex"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("after both patches = %+v, want %+v", got, want)
	}
	patch(`{"autoCompactWindow":""}`)
	if got := svc.defaults["codex"].AutoCompactWindow; got != "" {
		t.Fatalf("window not cleared: %q", got)
	}
}

type recordingCompaction struct{ harnesses []domain.AgentHarness }

func (r *recordingCompaction) ApplyCompactionDefault(_ context.Context, harness domain.AgentHarness) {
	r.harnesses = append(r.harnesses, harness)
}

// Saving a compaction default reaches the running chats of that harness; saving
// only the model list does not.
func TestSettingsHarnessDefaultPatchAppliesTheCompactionDefaultToRunningChats(t *testing.T) {
	svc := &harnessDefaultSettingsService{defaults: map[string]domain.HarnessDefault{}}
	applier := &recordingCompaction{}
	c := &SettingsController{Svc: svc, Compaction: applier}
	router := chi.NewRouter()
	router.Patch("/api/v1/settings/harness-defaults/{agent}", c.setHarnessDefault)
	patch := func(body string) int {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, loopbackRequest(http.MethodPatch, "/api/v1/settings/harness-defaults/codex", strings.NewReader(body)))
		return w.Code
	}
	if code := patch(`{"disabledModels":["gpt-5"]}`); code != http.StatusOK || len(applier.harnesses) != 0 {
		t.Fatalf("models-only save: status %d applied %v", code, applier.harnesses)
	}
	if code := patch(`{"autoCompactWindow":"40%"}`); code != http.StatusOK ||
		len(applier.harnesses) != 1 || applier.harnesses[0] != domain.HarnessCodex {
		t.Fatalf("compaction save: status %d applied %v", code, applier.harnesses)
	}
}
