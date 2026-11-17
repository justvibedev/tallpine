package settings_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
)

// The service refuses app rules over the cap and switches for an unknown
// role itself, whatever path reaches it, and stores nothing.
func TestSetWorkflowDefaultsRefusesInvalidAppInstructions(t *testing.T) {
	big := strings.Repeat("r", domain.MaxAgentRulesBytes+1)
	roles := domain.SkillRoles{"planner": {"a": true}}
	for name, update := range map[string]settingssvc.WorkflowDefaultsUpdate{
		"rules": {ReviewerRules: &big},
		"role":  {SkillRoles: &roles},
	} {
		st := &wdStore{d: domain.DefaultWorkflowDefaults()}
		svc := settingssvc.New(st, nil, settingssvc.Offering{}, nil)
		if _, err := svc.SetWorkflowDefaults(context.Background(), update); !errors.Is(err, settingssvc.ErrInvalidAppInstructions) {
			t.Errorf("%s: err = %v, want ErrInvalidAppInstructions", name, err)
		}
		if st.d.ReviewerRules != "" || st.d.SkillRoles != nil {
			t.Errorf("%s: stored %+v", name, st.d.AppInstructions)
		}
	}
}
