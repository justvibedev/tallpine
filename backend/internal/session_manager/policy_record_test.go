package sessionmanager

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (s *fakeStore) SetSessionPolicyDecision(_ context.Context, id domain.SessionID, record []byte, _ time.Time) error {
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	if s.policyRecords == nil {
		s.policyRecords = map[domain.SessionID][]byte{}
	}
	s.policyRecords[id] = record
	return nil
}

func (s *fakeStore) policyRecord(id domain.SessionID) []byte {
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	return s.policyRecords[id]
}

func spawnWithPolicy(t *testing.T, settings map[string]map[string]any) (domain.SessionRecord, *fakeStore) {
	t.Helper()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil }})
	if settings != nil {
		engine, _ := policytest.Engine(t, settings)
		m.SetAgentPolicy(engine)
	}
	rec, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker,
		AgentConfig: ports.AgentConfig{Permissions: domain.PermissionModeBypassPermissions}})
	if err != nil {
		t.Fatal(err)
	}
	return rec, st
}

func TestSpawnRecordsTheDecisionWithWhatWasLowered(t *testing.T) {
	t.Parallel()
	rec, st := spawnWithPolicy(t, capSettings(permissioncap.Auto))
	raw := st.policyRecord(rec.ID)
	if raw == nil {
		t.Fatal("the launch's policy decision was not recorded")
	}
	var got struct {
		V     int `json:"v"`
		Rules map[string]struct {
			Settings  map[string]any    `json:"settings"`
			Sources   map[string]string `json:"sources"`
			Requested string            `json:"requested"`
			Effective string            `json:"effective"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	limit := got.Rules["permissionCap"]
	if got.V != 2 || limit.Settings["max"] != "auto" || limit.Sources["max"] != "user" || limit.Requested != "bypass-permissions" || limit.Effective != "auto" {
		t.Fatalf("record = %s", raw)
	}
}

func TestSpawnWithoutAPolicyRecordsNothing(t *testing.T) {
	t.Parallel()
	rec, st := spawnWithPolicy(t, nil)
	if st.policyRecord(rec.ID) != nil {
		t.Fatal("no engine wired: nothing to record")
	}
}

func TestDecisionRecordHasNoSecretValues(t *testing.T) {
	t.Parallel()
	rec, st := spawnWithPolicy(t, capSettings(permissioncap.AcceptEdits))
	if raw := string(st.policyRecord(rec.ID)); strings.Contains(raw, "token") || strings.Contains(raw, "secret") {
		t.Fatalf("record must carry no secrets: %s", raw)
	}
}
