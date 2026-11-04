package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
)

type workflowDefaultsService struct {
	hibernationSettingsService
	autoReview bool
	last       settingssvc.WorkflowDefaultsUpdate
}

func (s *workflowDefaultsService) SetWorkflowDefaults(_ context.Context, u settingssvc.WorkflowDefaultsUpdate) (settingssvc.Snapshot, error) {
	if u.AutoReview != nil {
		s.autoReview = *u.AutoReview
	}
	s.last = u
	s.calls++
	snap := settingssvc.Snapshot{}
	snap.WorkflowDefaults.AutoReview = s.autoReview
	snap.WorkflowDefaults.PlanningMode = "always"
	if u.PlanningMode != nil {
		snap.WorkflowDefaults.PlanningMode = *u.PlanningMode
	}
	snap.WorkflowDefaults.PlanAutoApprove = u.PlanAutoApprove != nil && *u.PlanAutoApprove
	return snap, nil
}

func TestSettingsWorkflowDefaultsUserWrites(t *testing.T) {
	svc := &workflowDefaultsService{}
	c := &SettingsController{Svc: svc}
	for _, body := range []string{`{}`, `{"autoReview":null}`, `{"autoReview":"on"}`, `{"planningMode":""}`, `{"planningMode":"sometimes"}`} {
		w := httptest.NewRecorder()
		c.setWorkflowDefaults(w, loopbackRequest(http.MethodPatch, "/api/v1/settings/workflow-defaults", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", body, w.Code)
		}
	}
	w := httptest.NewRecorder()
	c.setWorkflowDefaults(w, loopbackRequest(http.MethodPatch, "/api/v1/settings/workflow-defaults", strings.NewReader(`{"autoReview":true}`)))
	if w.Code != http.StatusOK || !svc.autoReview {
		t.Fatalf("status %d autoReview %v", w.Code, svc.autoReview)
	}
	var out struct {
		WorkflowDefaults struct {
			AutoReview bool `json:"autoReview"`
		} `json:"workflowDefaults"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if !out.WorkflowDefaults.AutoReview {
		t.Fatalf("response %s lacks workflowDefaults.autoReview", w.Body.String())
	}
}

func TestSettingsWorkflowDefaultsPlanning(t *testing.T) {
	svc := &workflowDefaultsService{}
	c := &SettingsController{Svc: svc}
	w := httptest.NewRecorder()
	c.setWorkflowDefaults(w, loopbackRequest(http.MethodPatch, "/api/v1/settings/workflow-defaults", strings.NewReader(`{"planningMode":"off","planAutoApprove":true}`)))
	if w.Code != http.StatusOK || svc.last.PlanningMode == nil || *svc.last.PlanningMode != "off" || svc.last.PlanAutoApprove == nil || !*svc.last.PlanAutoApprove {
		t.Fatalf("status %d update %+v", w.Code, svc.last)
	}
	var out struct {
		WorkflowDefaults struct {
			PlanningMode    string `json:"planningMode"`
			PlanAutoApprove bool   `json:"planAutoApprove"`
		} `json:"workflowDefaults"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.WorkflowDefaults.PlanningMode != "off" || !out.WorkflowDefaults.PlanAutoApprove {
		t.Fatalf("response %s lacks the planning defaults", w.Body.String())
	}
}
