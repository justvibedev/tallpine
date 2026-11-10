package settings_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
)

type wdStore struct {
	settingssvc.Store
	d domain.WorkflowDefaults
}

func (s *wdStore) GetAppSettings(context.Context) (settingssvc.Snapshot, error) {
	return settingssvc.Snapshot{WorkflowDefaults: s.d}, nil
}
func (s *wdStore) SetWorkflowDefaults(_ context.Context, d domain.WorkflowDefaults, _ time.Time) error {
	s.d = d
	return nil
}

func TestWorkflowDefaultsPartialUpdate(t *testing.T) {
	st := &wdStore{d: domain.DefaultWorkflowDefaults()}
	svc := settingssvc.New(st, nil, settingssvc.Offering{}, nil)
	on := true
	if _, err := svc.SetWorkflowDefaults(context.Background(), settingssvc.WorkflowDefaultsUpdate{AutoReview: &on}); err != nil {
		t.Fatal(err)
	}
	got := svc.WorkflowDefaults(context.Background())
	if !got.AutoReview || got.PlanningMode != domain.PlanningAuto || got.PlanAutoApprove {
		t.Fatalf("got %+v: only autoReview should change", got)
	}
}

// A people-written update never reopens the migrated eval waiver gate.
func TestWorkflowDefaultsKeepEvalWaiverClosed(t *testing.T) {
	d := domain.DefaultWorkflowDefaults()
	d.EvalWaiverClosed = true
	st := &wdStore{d: d}
	svc := settingssvc.New(st, nil, settingssvc.Offering{}, nil)
	on := true
	if _, err := svc.SetWorkflowDefaults(context.Background(), settingssvc.WorkflowDefaultsUpdate{PlanAutoApprove: &on}); err != nil {
		t.Fatal(err)
	}
	if !st.d.EvalWaiverClosed {
		t.Fatalf("stored %+v, want the eval waiver gate kept", st.d)
	}
}

// Changing the reviewer harness drops the old harness's model unless the same
// update supplies one.
func TestWorkflowDefaultsHarnessChangeClearsModel(t *testing.T) {
	st := &wdStore{d: domain.WorkflowDefaults{ReviewerHarness: domain.ReviewerClaudeCode, ReviewerModel: "claude-old"}}
	svc := settingssvc.New(st, nil, settingssvc.Offering{}, nil)
	codex := domain.ReviewerCodex
	if _, err := svc.SetWorkflowDefaults(context.Background(), settingssvc.WorkflowDefaultsUpdate{ReviewerHarness: &codex}); err != nil {
		t.Fatal(err)
	}
	if st.d.ReviewerHarness != domain.ReviewerCodex || st.d.ReviewerModel != "" {
		t.Fatalf("stored %+v, want codex with no model", st.d)
	}
	model := "gpt-new"
	claude := domain.ReviewerClaudeCode
	if _, err := svc.SetWorkflowDefaults(context.Background(), settingssvc.WorkflowDefaultsUpdate{ReviewerHarness: &claude, ReviewerModel: &model}); err != nil {
		t.Fatal(err)
	}
	if st.d.ReviewerModel != "gpt-new" {
		t.Fatalf("stored %+v, want the supplied model kept", st.d)
	}
}
