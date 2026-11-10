package settingswrite

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

type reviewerDefaultStore struct {
	settingssvc.Store
	d domain.WorkflowDefaults
}

func (s *reviewerDefaultStore) GetAppSettings(context.Context) (settingssvc.Snapshot, error) {
	return settingssvc.Snapshot{WorkflowDefaults: s.d}, nil
}
func (s *reviewerDefaultStore) SetWorkflowDefaults(_ context.Context, d domain.WorkflowDefaults, _ time.Time) error {
	s.d = d
	return nil
}

// The default reviewer's harness and model are written as one pair whichever
// registry key the batch lists first, and a harness change alone drops the old
// harness's model.
func TestReviewerDefaultHarnessAndModelWrittenTogether(t *testing.T) {
	const hk, mk = settingsreg.Key("app.workflowDefaults.reviewerHarness"), settingsreg.Key("app.workflowDefaults.reviewerModel")
	pair := []planned{
		{def: settingsreg.Setting{Key: hk}, value: json.RawMessage(`"codex"`)},
		{def: settingsreg.Setting{Key: mk}, value: json.RawMessage(`"gpt-new"`)},
	}
	for name, plan := range map[string][]planned{"harness first": pair, "model first": {pair[1], pair[0]}} {
		st := &reviewerDefaultStore{d: domain.WorkflowDefaults{ReviewerHarness: domain.ReviewerClaudeCode, ReviewerModel: "claude-old"}}
		w := &Writer{AppSettings: settingssvc.New(st, nil, settingssvc.Offering{}, nil)}
		for _, p := range plan {
			if err := w.setWorkflowDefault(context.Background(), p.def.Key, p.value, plan); err != nil {
				t.Fatal(err)
			}
		}
		if st.d.ReviewerHarness != domain.ReviewerCodex || st.d.ReviewerModel != "gpt-new" {
			t.Fatalf("%s: stored %s/%q, want codex/gpt-new", name, st.d.ReviewerHarness, st.d.ReviewerModel)
		}
	}
	st := &reviewerDefaultStore{d: domain.WorkflowDefaults{ReviewerHarness: domain.ReviewerClaudeCode, ReviewerModel: "claude-old"}}
	w := &Writer{AppSettings: settingssvc.New(st, nil, settingssvc.Offering{}, nil)}
	if err := w.setWorkflowDefault(context.Background(), hk, json.RawMessage(`"codex"`), pair[:1]); err != nil {
		t.Fatal(err)
	}
	if st.d.ReviewerHarness != domain.ReviewerCodex || st.d.ReviewerModel != "" {
		t.Fatalf("harness alone: stored %s/%q, want codex with the model cleared", st.d.ReviewerHarness, st.d.ReviewerModel)
	}
}
