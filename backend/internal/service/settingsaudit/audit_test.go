package settingsaudit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

type fakeStore struct {
	rows []domain.SettingsChange
	err  error
}

func (f *fakeStore) AppendSettingsChanges(_ context.Context, c []domain.SettingsChange) error {
	f.rows = append(f.rows, c...)
	return f.err
}

func TestDiffRecordsOnlyChangedKeysAndRedactsSensitive(t *testing.T) {
	before := ProjectConfig(domain.ProjectConfig{AgentRules: "a", NoCI: true, Env: map[string]string{"TOKEN": "s1"}, DefaultBranch: "main"})
	after := ProjectConfig(domain.ProjectConfig{AgentRules: "b", Env: map[string]string{"TOKEN": "s2"}, DefaultBranch: "main"})
	got := Diff(settingsreg.Default(), "app", before, after)
	want := []domain.SettingsChange{
		{ProjectID: "app", Key: "project.agentRules", OldValue: `"a"`, NewValue: `"b"`},
		{ProjectID: "app", Key: "project.env", OldValue: Redacted, NewValue: Redacted},
		{ProjectID: "app", Key: "project.noCI", OldValue: "true", NewValue: "null"},
	}
	if len(got) != len(want) {
		t.Fatalf("diff = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("change %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestDiffIgnoresFormattingOnlyDifferences(t *testing.T) {
	before := Values{"app.harnessDefaults": json.RawMessage(`{"a": 1}`)}
	after := Values{"app.harnessDefaults": json.RawMessage(`{"a":1}`)}
	if got := Diff(settingsreg.Default(), "", before, after); len(got) != 0 {
		t.Fatalf("diff = %+v", got)
	}
}

func TestRecordStoresActorAndSurvivesStoreFailure(t *testing.T) {
	var logs bytes.Buffer
	store := &fakeStore{err: errors.New("disk full")}
	r := New(store, slog.New(slog.NewTextHandler(&logs, nil)))
	r.now = func() time.Time { return time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC) }
	ctx := requestscope.WithAgent(context.Background(), requestscope.Agent{Role: requestscope.RoleOrchestrator, SessionID: "app-1", ProjectID: "app"})
	r.Record(ctx, "app", Values{"project.noCI": json.RawMessage("false")}, Values{"project.noCI": json.RawMessage("true")})
	if len(store.rows) != 1 || store.rows[0].Actor != "orchestrator:app-1" || store.rows[0].ChangedAt.IsZero() {
		t.Fatalf("rows = %+v", store.rows)
	}
	if !strings.Contains(logs.String(), "settings change log write failed") || !strings.Contains(logs.String(), "key=project.noCI") {
		t.Fatalf("log = %s", logs.String())
	}
	var nilRecorder *Recorder
	nilRecorder.Record(ctx, "app", nil, Values{"project.noCI": json.RawMessage("true")})
}

func TestActorFromDefaultsToUser(t *testing.T) {
	if got := ActorFrom(context.Background()); got != "user" {
		t.Fatalf("actor = %q", got)
	}
}

func TestPolicyLayerKeepsOnlyTheLayersValues(t *testing.T) {
	view := agentpolicy.View{Rules: []agentpolicy.RuleView{{Name: "permissionCap", Fields: []agentpolicy.FieldView{
		{Name: "max", Value: json.RawMessage(`"auto"`), Source: agentpolicy.SourceProject},
		{Name: "other", Value: json.RawMessage(`true`), Source: agentpolicy.SourceManaged},
	}}}}
	got := PolicyLayer(view, agentpolicy.SourceProject)
	if string(got["policy.permissionCap.max"]) != `"auto"` || string(got["policy.permissionCap.other"]) != "null" {
		t.Fatalf("layer = %v", got)
	}
}
