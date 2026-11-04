package httpd

import "testing"

// A creating orchestrator may set orchestrator.autoCompactWindow (B193), and a
// config read back with it set must stay writable; a worker override of it is
// still refused.
func TestAgentConfigViolationAllowsOrchestratorCompactWindow(t *testing.T) {
	if v := agentConfigViolation([]byte(`{"orchestrator":{"agent":"codex","autoCompactWindow":"200k"}}`)); v != "" {
		t.Fatalf("orchestrator.autoCompactWindow refused: %s", v)
	}
	if v := agentConfigViolation([]byte(`{"worker":{"autoCompactWindow":"200k"}}`)); v == "" {
		t.Fatal("worker.autoCompactWindow accepted")
	}
}

func TestAgentConfigViolationAllowsOrchestratorIdleCompactAt(t *testing.T) {
	if v := agentConfigViolation([]byte(`{"orchestrator":{"idleCompactAt":"40%"}}`)); v != "" {
		t.Fatalf("orchestrator.idleCompactAt refused: %s", v)
	}
}

// planner is a role override (B205) a creating orchestrator may set: the
// planner's agent and agentConfig, nothing else. A managed policy.planner lock
// still wins at resolution time (SetPlannerLock), so this only opens the write.
func TestAgentConfigViolationAllowsPlannerRole(t *testing.T) {
	if v := agentConfigViolation([]byte(`{"planner":{"agent":"codex","agentConfig":{"model":"m"}}}`)); v != "" {
		t.Fatalf("planner role refused: %s", v)
	}
	if v := agentConfigViolation([]byte(`{"planner":{"autoCompactWindow":"200k"}}`)); v == "" {
		t.Fatal("planner.autoCompactWindow accepted")
	}
}
