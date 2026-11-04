package settingsreg

import (
	"strings"
	"testing"
)

func valid() Setting {
	return Setting{Key: "app.example", Scope: ScopeApp, Kind: KindBool, Default: jsonOf(false), Description: "x", Writers: []Actor{ActorUser}}
}

func TestNewRejectsInvalidDeclarations(t *testing.T) {
	cases := map[string]func(*Setting){
		"bad key":            func(s *Setting) { s.Key = "app.Bad-key" },
		"scope mismatch":     func(s *Setting) { s.Scope = ScopeProject },
		"no description":     func(s *Setting) { s.Description = "" },
		"no user writer":     func(s *Setting) { s.Writers = []Actor{ActorOrchestrator} },
		"bad default json":   func(s *Setting) { s.Default = []byte("{") },
		"bool default type":  func(s *Setting) { s.Default = jsonOf(1) },
		"unknown kind":       func(s *Setting) { s.Kind = "float" },
		"enum default":       func(s *Setting) { s.Kind, s.Enum, s.Default = KindEnum, []string{"a"}, jsonOf("b") },
		"int above max":      func(s *Setting) { s.Kind, s.Default, s.Max = KindInt, jsonOf(5), bound(4) },
		"int below min":      func(s *Setting) { s.Kind, s.Default, s.Min = KindInt, jsonOf(1), bound(2) },
		"string default int": func(s *Setting) { s.Kind = KindString },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := valid()
			mutate(&s)
			if _, err := New(s); err == nil {
				t.Fatalf("New accepted %+v", s)
			}
		})
	}
	if _, err := New(valid()); err != nil {
		t.Fatalf("New rejected a valid setting: %v", err)
	}
}

func TestNewRejectsDuplicateKeys(t *testing.T) {
	if _, err := New(valid(), valid()); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("err = %v, want duplicate", err)
	}
}

func TestDefaultRegistryLookupAndScopes(t *testing.T) {
	reg := Default()
	for _, scope := range []Scope{ScopeApp, ScopeProject, ScopePolicy} {
		if len(reg.Scope(scope)) == 0 {
			t.Errorf("no %s settings", scope)
		}
	}
	if len(reg.All()) != len(reg.Scope(ScopeApp))+len(reg.Scope(ScopeProject))+len(reg.Scope(ScopePolicy)) {
		t.Error("a setting has no known scope")
	}
	s, ok := reg.Lookup("policy.permissionCap.max")
	if !ok || !s.MDMLockable || !s.CanWrite(ActorOrchestrator) {
		t.Fatalf("policy.permissionCap.max = %+v, %v", s, ok)
	}
	for key, want := range map[Key]bool{"policy.accountPool.placement": false, "policy.crossProjectOrchestration.enabled": false, "policy.protectState.mode": true} {
		if s, _ := reg.Lookup(key); s.CanWrite(ActorOrchestrator) != want {
			t.Errorf("%s: orchestrator writer = %v, want %v", key, !want, want)
		}
	}
	if _, ok := reg.Lookup("app.nope"); ok {
		t.Fatal("lookup of an unknown key succeeded")
	}
}

// TestWritersDescribeAgentAccess pins the B202 permission model: the project's
// own orchestrator writes every project setting and the ordered-enum policy
// fields (tighten-only, enforced by settingswrite), never an app setting or
// another policy field; a creating orchestrator only the agentConfigKeys
// subset; workers nothing. Two exceptions: project.planAutoApprove, which only
// the user writes, and project.planInPlace, which the own orchestrator may not
// write but the creating orchestrator may (eval projects it registered).
func TestWritersDescribeAgentAccess(t *testing.T) {
	for _, s := range Default().All() {
		if s.CanWrite(ActorWorker) {
			t.Errorf("%s: workers may not write settings", s.Key)
		}
		orch := s.Scope == ScopeProject
		if s.Key == "project.planAutoApprove" {
			orch = false // approves plans without the owner: a person's alone
		}
		if s.Key == "project.planInPlace" || s.Key == "project.eval" {
			orch = false // denied to the own orchestrator; the creator may (checked below)
			if !s.CanWrite(ActorCreatorOrchestrator) {
				t.Errorf("%s: the creating orchestrator may write it", s.Key)
			}
		}
		if s.Scope == ScopePolicy {
			// Only ordered enums a project may override; the rest stay the user's.
			orch = s.CanWrite(ActorOrchestrator)
			if orch && s.Kind != KindEnum && s.Key != "policy.securityMode.enabled" { // on-only, see settingswrite
				t.Errorf("%s: an orchestrator may only write enum policy fields", s.Key)
			}
		}
		if s.CanWrite(ActorOrchestrator) != orch {
			t.Errorf("%s: orchestrator writer = %v, want %v", s.Key, !orch, orch)
		}
		if s.CanWrite(ActorCreatorOrchestrator) && s.Scope != ScopeProject {
			t.Errorf("%s: a creating orchestrator writes only project settings", s.Key)
		}
	}
}

func TestRegistry_PlannerSettingsDeclared(t *testing.T) {
	reg := Default()
	for _, key := range []Key{"project.planningMode", "project.planner", "policy.planner.mode", "policy.planner.harness", "policy.planner.model"} {
		s, ok := reg.Lookup(key)
		if !ok {
			t.Fatalf("%s is not declared", key)
		}
		if strings.HasPrefix(string(key), "policy.") && !s.MDMLockable {
			t.Errorf("%s must be MDM-lockable", key)
		}
	}
	if s, _ := reg.Lookup("policy.planner.model"); s.Kind != KindString {
		t.Errorf("policy.planner.model kind = %q, want string", s.Kind)
	}
}

// project.planAutoApprove approves plans without the owner, so only the user
// writes it: not the project's orchestrator, not a creating orchestrator.
func TestRegistry_PlanAutoApproveIsHumanOnly(t *testing.T) {
	s, ok := Default().Lookup("project.planAutoApprove")
	if !ok {
		t.Fatal("project.planAutoApprove is not declared")
	}
	if s.Kind != KindBool || string(s.Default) != "false" {
		t.Errorf("kind %q default %s, want bool false", s.Kind, s.Default)
	}
	if !s.CanWrite(ActorUser) || s.CanWrite(ActorOrchestrator) || s.CanWrite(ActorCreatorOrchestrator) {
		t.Errorf("writers = %v, want the user only", s.Writers)
	}
	if s, ok := Default().Lookup("policy.planner.autoApprove"); !ok || !s.MDMLockable {
		t.Errorf("policy.planner.autoApprove = %+v, %v; want a lockable policy key", s, ok)
	}
}
