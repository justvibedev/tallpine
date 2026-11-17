package settingswrite

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
	"tallpine.eu/tallpine/backend/internal/service/settingsview"
	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

var appInstructionKeys = []settingsreg.Key{"app.workerRules", "app.orchestratorRules", "app.reviewerRules", "app.skillRoles"}

// No agent writes the app rules or switches: an orchestrator is refused any
// app-wide change, and even in Dev mode each key is the user's only.
func TestAgentsCannotWriteAppInstructions(t *testing.T) {
	reg := settingsreg.Default()
	orch := requestscope.Agent{Role: requestscope.RoleOrchestrator, ProjectID: "mer"}
	if agentMayChange(orch, "") == nil {
		t.Fatal("an orchestrator may change app-wide settings")
	}
	orch.DevMode = true
	for _, k := range appInstructionKeys {
		def, ok := reg.Lookup(k)
		if !ok {
			t.Fatalf("%s is not registered", k)
		}
		next := json.RawMessage(`"x"`)
		if k == "app.skillRoles" {
			next = json.RawMessage(`{"reviewer":{"a":true}}`)
		}
		if agentMayWrite(orch, reg, def, def.Default, next) == "" {
			t.Errorf("a Dev mode orchestrator may write %s", k)
		}
	}
}

func appWriter() (*Writer, *reviewerDefaultStore) {
	st := &reviewerDefaultStore{}
	svc := settingssvc.New(st, nil, settingssvc.Offering{}, nil)
	return &Writer{View: &settingsview.Service{AppSettings: svc}, AppSettings: svc}, st
}

// The owner writes app rules and switches through the registry; an unset
// key returns to empty, and invalid values are refused before any write.
func TestOwnerWritesAppInstructions(t *testing.T) {
	ctx := context.Background()
	w, st := appWriter()
	st.d.AutoReview = true
	err := w.Apply(ctx, Change{Set: map[settingsreg.Key]json.RawMessage{
		"app.workerRules": json.RawMessage(`"Worker rule."`), "app.reviewerRules": json.RawMessage(`"Review rule."`),
		"app.skillRoles": json.RawMessage(`{"worker":{"design":false},"reviewer":{"sql":true}}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if st.d.WorkerRules != "Worker rule." || st.d.ReviewerRules != "Review rule." || st.d.SkillRoles["worker"]["design"] || !st.d.SkillRoles["reviewer"]["sql"] {
		t.Fatalf("stored %+v", st.d.AppInstructions)
	}
	if !st.d.AutoReview {
		t.Fatal("writing app rules changed app-wide auto-review")
	}
	if err := w.Apply(ctx, Change{Unset: []settingsreg.Key{"app.workerRules", "app.skillRoles"}}); err != nil {
		t.Fatal(err)
	}
	if st.d.WorkerRules != "" || st.d.SkillRoles != nil || st.d.ReviewerRules != "Review rule." {
		t.Fatalf("unset left %+v", st.d.AppInstructions)
	}
	for name, raw := range map[settingsreg.Key]json.RawMessage{
		"app.skillRoles":  json.RawMessage(`{"planner":{"a":true}}`),
		"app.workerRules": json.RawMessage(`"` + strings.Repeat("r", domain.MaxAgentRulesBytes+1) + `"`),
	} {
		var refusal *Error
		if err := w.Apply(ctx, Change{Set: map[settingsreg.Key]json.RawMessage{name: raw}}); !errors.As(err, &refusal) || refusal.Code != CodeInvalid {
			t.Errorf("%s: err = %v, want %s", name, err, CodeInvalid)
		}
	}
}
