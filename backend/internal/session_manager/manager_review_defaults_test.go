package sessionmanager

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestSpawn_TerminateOnPRMergeFromProjectDefault(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		on   bool
		want bool
	}{{"default off", false, false}, {"project default on", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			m, st, _, _ := newManager()
			cfg := testRoleAgents()
			cfg.TerminateOnPRMerge = domain.BoolPtr(tc.on)
			st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Kind: domain.ProjectKindWorkspace, Config: cfg}
			s, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
			if err != nil {
				t.Fatal(err)
			}
			if got := st.sessions[s.ID].TerminateOnPRMerge; got != tc.want {
				t.Fatalf("TerminateOnPRMerge = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSpawn_AutoReviewAndTerminateOnMergeAreProjectOnly(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		project   *bool
		want      bool
		wantClose bool
	}{{"project unset: review off, end-on-merge on (B407)", nil, false, true}, {"project true is on", domain.BoolPtr(true), true, true}, {"project false is off", domain.BoolPtr(false), false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			m, st, _, _ := newManager()
			cfg := testRoleAgents()
			cfg.AutoReview, cfg.TerminateOnPRMerge = tc.project, tc.project
			st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Kind: domain.ProjectKindWorkspace, Config: cfg}
			s, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
			if err != nil {
				t.Fatal(err)
			}
			if got := st.sessions[s.ID]; got.AutoReviewEnabled != tc.want || got.TerminateOnPRMerge != tc.wantClose {
				t.Fatalf("autoReview=%v terminateOnPRMerge=%v, want %v/%v", got.AutoReviewEnabled, got.TerminateOnPRMerge, tc.want, tc.wantClose)
			}
		})
	}
}

type appWorkflowDefaults struct{ d domain.WorkflowDefaults }

func (appWorkflowDefaults) DefaultSessionMode(context.Context) domain.SessionMode { return "" }
func (a appWorkflowDefaults) WorkflowDefaults(context.Context) domain.WorkflowDefaults {
	return a.d
}

func TestSpawn_AutoReviewInheritsAppDefault(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		project *bool
		app     bool
		want    bool
	}{
		{"unset inherits on", nil, true, true},
		{"unset inherits off", nil, false, false},
		{"explicit false stays off", domain.BoolPtr(false), true, false},
		{"explicit true stays on", domain.BoolPtr(true), false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, st, _, _ := newManager()
			m.defaults = appWorkflowDefaults{domain.WorkflowDefaults{AutoReview: tc.app}}
			cfg := testRoleAgents()
			cfg.AutoReview = tc.project
			st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Kind: domain.ProjectKindWorkspace, Config: cfg}
			s, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
			if err != nil {
				t.Fatal(err)
			}
			if got := st.sessions[s.ID].AutoReviewEnabled; got != tc.want {
				t.Fatalf("AutoReviewEnabled = %v, want %v", got, tc.want)
			}
		})
	}
}
