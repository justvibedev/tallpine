package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
)

type storageSettingsService struct {
	hibernationSettingsService
	enabled *bool
	days    *int
}

func (s *storageSettingsService) SetStorageCompaction(_ context.Context, enabled *bool, days *int) (settingssvc.Snapshot, error) {
	s.calls++
	if enabled == nil && days == nil {
		return settingssvc.Snapshot{}, fmt.Errorf("%w: empty", settingssvc.ErrInvalidStorageCompaction)
	}
	if days != nil && *days < 1 {
		return settingssvc.Snapshot{}, fmt.Errorf("%w: days", settingssvc.ErrInvalidStorageCompaction)
	}
	s.enabled, s.days = enabled, days
	snapshot := settingssvc.Snapshot{StorageCompaction: true, ProviderEventRetentionDays: 14}
	if enabled != nil {
		snapshot.StorageCompaction = *enabled
	}
	if days != nil {
		snapshot.ProviderEventRetentionDays = *days
	}
	return snapshot, nil
}

func TestSettingsStorageCompactionPartialUpdatesAndValidation(t *testing.T) {
	svc := &storageSettingsService{}
	c := &SettingsController{Svc: svc}
	patch := func(body string) (*httptest.ResponseRecorder, SettingsResponse) {
		w := httptest.NewRecorder()
		c.setStorageCompaction(w, loopbackRequest(http.MethodPatch, "/api/v1/settings/storage-compaction", strings.NewReader(body)))
		var got SettingsResponse
		_ = json.Unmarshal(w.Body.Bytes(), &got)
		return w, got
	}

	for _, body := range []string{`{}`, `{"providerEventRetentionDays":0}`} {
		if w, _ := patch(body); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "STORAGE_COMPACTION_INVALID") {
			t.Fatalf("body=%s status=%d response=%s", body, w.Code, w.Body.String())
		}
	}
	if w, _ := patch(`{"enabled":"no"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("non-boolean enabled accepted: status=%d", w.Code)
	}

	w, got := patch(`{"enabled":false}`)
	if w.Code != http.StatusOK || got.StorageCompaction || got.ProviderEventRetentionDays != 14 || svc.days != nil {
		t.Fatalf("disable: status=%d response=%+v days=%v", w.Code, got, svc.days)
	}
	w, got = patch(`{"providerEventRetentionDays":30}`)
	if w.Code != http.StatusOK || got.ProviderEventRetentionDays != 30 || svc.enabled != nil {
		t.Fatalf("retention: status=%d response=%+v enabled=%v", w.Code, got, svc.enabled)
	}
}
