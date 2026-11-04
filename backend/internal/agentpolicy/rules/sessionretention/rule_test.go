package sessionretention

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/ruletest"
	"tallpine.eu/tallpine/backend/internal/domain"
)

func newEngine(t *testing.T, managed string) *agentpolicy.Engine {
	t.Helper()
	return ruletest.Engine(t, Rule{}, managed)
}

func decide(e *agentpolicy.Engine, project string) Outcome {
	return Of(e.Decide(context.Background(), agentpolicy.SessionRef{Project: domain.ProjectID(project)}))
}

func update(e *agentpolicy.Engine, project string, fields map[string]string) error {
	u := agentpolicy.Update{Name: {}}
	for k, v := range fields {
		u[Name][k] = json.RawMessage(v)
	}
	return e.Update(context.Background(), project, u, time.Now())
}

func TestOnByDefault(t *testing.T) {
	got := decide(newEngine(t, ""), "")
	want := Outcome{Enabled: true, Days: 7, DropProviderEvents: true, DropWorktrees: true, DropTranscripts: false, IncludeOrchestrators: false}
	if got != want {
		t.Fatalf("default = %+v, want %+v", got, want)
	}
}

func TestUserOptInAndProjectOverride(t *testing.T) {
	e := newEngine(t, "")
	if err := update(e, "", map[string]string{FieldEnabled: "true", FieldDays: "10"}); err != nil {
		t.Fatal(err)
	}
	if err := update(e, "p", map[string]string{FieldDays: "90"}); err != nil {
		t.Fatal(err)
	}
	if got := decide(e, ""); !got.Enabled || got.Days != 10 {
		t.Fatalf("user = %+v", got)
	}
	if got := decide(e, "p"); !got.Enabled || got.Days != 90 {
		t.Fatalf("project override = %+v", got)
	}
}

// The MDM value overrides and locks the user's: an update is refused and the
// managed value stays in force.
func TestManagedValueLocksUserSetting(t *testing.T) {
	e := newEngine(t, `{"schemaVersion":2,"rules":{"sessionRetention":{"enabled":true,"days":14}}}`)
	err := update(e, "", map[string]string{FieldEnabled: "false", FieldDays: "365"})
	var locked *agentpolicy.LockedError
	if !errors.As(err, &locked) {
		t.Fatalf("update of a managed field = %v, want LockedError", err)
	}
	_ = update(e, "p", map[string]string{FieldEnabled: "false"})
	for _, project := range []string{"", "p"} {
		if got := decide(e, project); !got.Enabled || got.Days != 14 {
			t.Fatalf("project %q = %+v, want the managed enabled/14", project, got)
		}
	}
	view := e.Describe(context.Background(), "")
	for _, r := range view.Rules {
		if r.Name != Name {
			continue
		}
		for _, f := range r.Fields {
			if (f.Name == FieldEnabled || f.Name == FieldDays) && !f.Locked {
				t.Fatalf("%s is not reported locked", f.Name)
			}
			if f.Name == FieldDropTranscripts && f.Locked {
				t.Fatalf("%s is locked but the managed file does not set it", f.Name)
			}
		}
	}
}

func TestDaysOutOfRangeRefused(t *testing.T) {
	e := newEngine(t, "")
	for _, v := range []string{"0", "3651"} {
		if err := update(e, "", map[string]string{FieldDays: v}); err == nil {
			t.Fatalf("days=%s accepted", v)
		}
	}
}
