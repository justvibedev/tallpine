package systeminstall

import (
	"context"
	"errors"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

type installCapabilitiesStub struct {
	prefix            string
	prefixErr         error
	nodeVersion       string
	npmVersion        string
	homebrewPrefix    string
	homebrewErr       error
	homebrewInstalled bool
	writable          bool
	calls             *int
	probe             func(context.Context) error
}

func (s installCapabilitiesStub) Probe(ctx context.Context) (ports.InstallCapabilities, error) {
	if s.calls != nil {
		(*s.calls)++
	}
	if s.probe != nil {
		if err := s.probe(ctx); err != nil {
			return ports.InstallCapabilities{}, err
		}
	}
	nodeVersion := s.nodeVersion
	if nodeVersion == "" {
		nodeVersion = "v22.19.0"
	}
	npmVersion := s.npmVersion
	if npmVersion == "" {
		npmVersion = "10.0.0"
	}
	homebrewPrefix := s.homebrewPrefix
	if s.homebrewPrefix == "" && s.homebrewErr == nil {
		homebrewPrefix = "/opt/homebrew"
	}
	formulae := map[string]bool{}
	casks := map[string]bool{}
	if s.homebrewInstalled {
		formulae["codex"] = true
		casks["codex"] = true
	}
	return ports.InstallCapabilities{
		NPM: ports.NPMInstallCapabilities{
			NodeVersion: nodeVersion, NPMVersion: npmVersion,
			GlobalPrefix: s.prefix, PrefixWritable: s.writable, Err: s.prefixErr,
		},
		Homebrew: ports.HomebrewInstallCapabilities{
			Prefix: homebrewPrefix, PrefixWritable: s.writable,
			Formulae: formulae, Casks: casks, Err: s.homebrewErr,
		},
	}, nil
}

func TestAgentPlansSnapshotsCapabilitiesOnce(t *testing.T) {
	calls := 0
	s := newTestService("darwin", "npm", "brew")
	s.installCapabilities = installCapabilitiesStub{prefix: "/Users/test/.npm", writable: true, calls: &calls}
	if _, err := s.AgentPlans(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("capability probes = %d, want one snapshot for the request", calls)
	}
}

func TestAgentPlansCancelsCapabilitySnapshotWithRequest(t *testing.T) {
	started := make(chan struct{})
	s := newTestService("darwin", "npm", "brew")
	s.installCapabilities = installCapabilitiesStub{probe: func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := s.AgentPlans(ctx)
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("AgentPlans error = %v, want context canceled", err)
	}
}

func TestAgentInstallPlansNeverUseShellEvaluationOrSudo(t *testing.T) {
	found := []string{"brew", "npm", "pnpm", "bun", "uv", "pipx", "winget", "bash", "sh", "pwsh.exe", "powershell.exe"}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		s := newTestService(goos, found...)
		planner, err := s.newRequestPlanner(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range agentTargets {
			for _, plan := range planner.agentMethodPlans(target, AgentOperationInstall) {
				argv := append([]string(nil), plan.Command...)
				if plan.Script != nil {
					argv = append(argv, plan.Script.Interpreter...)
				}
				for _, arg := range argv {
					if arg == "sudo" || arg == "-c" || arg == "-Command" || strings.Contains(arg, "|") {
						t.Fatalf("%s/%s/%s contains shell-evaluated argument %q: %+v", goos, target, plan.Method, arg, plan)
					}
				}
			}
		}
	}
}

func TestOfficialInstallersRejectUnsupportedOperatingSystems(t *testing.T) {
	for _, target := range []Target{TargetCodex, Target("cursor"), TargetKiro, Target("kimchi"), Target("goose"), Target("devin"), Target("muse"), Target("prime-agent")} {
		plan := newTestService("freebsd", "sh", "bash", "pwsh").planAgent(target)
		if !plan.Unsupported || plan.Script != nil {
			t.Fatalf("%s plan = %+v, want manual unsupported plan", target, plan)
		}
	}
}

func TestOfficialInstallerIsPreferredOverPackageManagers(t *testing.T) {
	s := newTestService("darwin", "brew", "npm", "sh")
	s.installCapabilities = installCapabilitiesStub{
		prefix: "/Users/test/.npm", homebrewPrefix: "/opt/homebrew", writable: true,
	}
	plans, err := s.AgentPlans(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range plans {
		if plan.AgentID != string(TargetCodex) {
			continue
		}
		if plan.Method != "official-installer" {
			t.Fatalf("recommended method = %q, want official-installer", plan.Method)
		}
		if len(plan.Methods) != 3 {
			t.Fatalf("methods = %+v", plan.Methods)
		}
		want := []string{"homebrew", "npm", "official-installer"}
		for i, method := range plan.Methods {
			if method.ID != want[i] || method.Recommended != (i == 2) {
				t.Fatalf("method[%d] = %+v", i, method)
			}
		}
		return
	}
	t.Fatal("codex plan not found")
}

func TestVibeReinstallUsesPackageManagerReinstallCommands(t *testing.T) {
	s := newTestService("darwin", "uv", "pipx")
	planner, err := s.newRequestPlanner(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	plans := planner.agentMethodPlans(Target("vibe"), AgentOperationReinstall)
	want := map[string]string{
		"uv":   "uv tool install mistral-vibe --force --reinstall",
		"pipx": "pipx install --force mistral-vibe",
	}
	for _, plan := range plans {
		if got := strings.Join(plan.Command, " "); got != want[plan.Method] {
			t.Errorf("%s reinstall command = %q, want %q", plan.Method, got, want[plan.Method])
		}
	}
}

func TestHomebrewReinstallRepairsThroughInstallWhenPackageIsNotOwned(t *testing.T) {
	for _, tt := range []struct {
		name      string
		installed bool
		want      string
	}{
		{name: "package absent", want: "brew install --cask codex"},
		{name: "package present", installed: true, want: "brew reinstall --cask codex"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService("darwin", "brew")
			s.installCapabilities = installCapabilitiesStub{homebrewInstalled: tt.installed, writable: true}
			planner, err := s.newRequestPlanner(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			plan, err := planner.resolveAgentMethod(TargetCodex, "homebrew", AgentOperationReinstall)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(plan.Command, " "); got != tt.want {
				t.Fatalf("Homebrew repair command = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestKiroReinstallIsUnavailableWithoutVerifiedHeadlessRecipe(t *testing.T) {
	s := newTestService("windows", "powershell.exe", "kiro-cli")
	planner, err := s.newRequestPlanner(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	plans := planner.agentMethodPlans(TargetKiro, AgentOperationReinstall)
	if len(plans) != 1 {
		t.Fatalf("Kiro reinstall plans = %d, want 1", len(plans))
	}
	plan := plans[0]
	if !plan.Unsupported || len(plan.Command) != 0 || plan.Script != nil {
		t.Fatalf("Kiro reinstall plan = %+v, want instructions-only", plan)
	}
	if !strings.Contains(plan.Reason, "verified headless reinstall") {
		t.Fatalf("Kiro reinstall reason = %q", plan.Reason)
	}
	_, err = planner.resolveAgentMethod(TargetKiro, "official-installer", AgentOperationReinstall)
	if !errors.Is(err, ErrInstallMethod) {
		t.Fatalf("Kiro reinstall error = %v, want ErrInstallMethod", err)
	}
}

func TestKiroMacOSInstallRequiresInteractiveVendorFlow(t *testing.T) {
	plan := newTestService("darwin", "bash").planAgent(TargetKiro)
	if !plan.Unsupported || plan.Method != "manual" || plan.Script != nil || len(plan.Command) != 0 {
		t.Fatalf("Kiro macOS plan = %+v, want manual interactive installation", plan)
	}
	if !strings.Contains(plan.Reason, "must be run interactively") || plan.DocsURL == "" {
		t.Fatalf("Kiro macOS guidance = %+v, want interactive reason and documentation", plan)
	}
}

func TestNPMPlanRequiresWritableGlobalPrefix(t *testing.T) {
	tests := []struct {
		name       string
		caps       installCapabilitiesStub
		wantReason string
	}{
		{name: "prefix lookup fails", caps: installCapabilitiesStub{prefixErr: errors.New("npm failed")}, wantReason: "could not be inspected"},
		{name: "prefix not writable", caps: installCapabilitiesStub{prefix: "/usr/local", writable: false}, wantReason: "not writable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService("darwin", "npm")
			s.installCapabilities = tt.caps
			plan := s.planNPM(TargetCodex, "@openai/codex")
			if !plan.Unsupported || !strings.Contains(plan.Reason, tt.wantReason) {
				t.Fatalf("plan = %+v, want unavailable reason containing %q", plan, tt.wantReason)
			}
		})
	}

	s := newTestService("darwin", "npm")
	s.installCapabilities = installCapabilitiesStub{prefix: "/Users/test/.npm", writable: true}
	plan := s.planNPM(TargetCodex, "@openai/codex")
	if plan.Unsupported || plan.ExpectedDestination != "/Users/test/.npm/bin" {
		t.Fatalf("plan = %+v, want writable npm destination", plan)
	}
}

func TestNPMPlanRequiresParseableNodeAndNPMVersions(t *testing.T) {
	tests := []struct {
		name        string
		nodeVersion string
		npmVersion  string
		wantReason  string
	}{
		{name: "unparseable node", nodeVersion: "unknown", npmVersion: "10.8.0", wantReason: "could not be validated"},
		{name: "unparseable npm", nodeVersion: "v22.19.0", npmVersion: "unknown", wantReason: "could not be validated"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService("darwin", "npm")
			s.installCapabilities = installCapabilitiesStub{
				prefix: "/Users/test/.npm", writable: true,
				nodeVersion: tt.nodeVersion, npmVersion: tt.npmVersion,
			}
			plan := s.planNPM(TargetCodex, "@openai/codex")
			if !plan.Unsupported || !strings.Contains(plan.Reason, tt.wantReason) {
				t.Fatalf("plan = %+v, want unavailable reason containing %q", plan, tt.wantReason)
			}
		})
	}
}

func TestHomebrewPlanRequiresWritablePrefix(t *testing.T) {
	s := newTestService("darwin", "brew")
	s.installCapabilities = installCapabilitiesStub{homebrewPrefix: "/opt/homebrew", writable: false}
	plan := s.planBrew(TargetCodex, "codex")
	if !plan.Unsupported || !strings.Contains(plan.Reason, "not writable") {
		t.Fatalf("plan = %+v, want unavailable Homebrew writability reason", plan)
	}
}

func TestHomebrewPlanReinstallsAnExistingPackage(t *testing.T) {
	s := newTestService("darwin", "brew")
	s.installCapabilities = installCapabilitiesStub{homebrewPrefix: "/opt/homebrew", homebrewInstalled: true, writable: true}
	plan := s.planBrewCask(TargetCodex, "codex")
	if got := strings.Join(plan.Command, " "); got != "brew reinstall --cask codex" {
		t.Fatalf("command = %q, want an actual cask reinstall", got)
	}
}

func TestHomebrewPlanFailsClosedWhenInstalledPackageProbeFails(t *testing.T) {
	s := newTestService("darwin", "brew")
	s.installCapabilities = installCapabilitiesStub{
		homebrewPrefix: "/opt/homebrew", homebrewErr: errors.New("brew list timed out"), writable: true,
	}
	plan := s.planBrewCask(TargetCodex, "codex")
	if !plan.Unsupported || !strings.Contains(plan.Reason, "could not be inspected") {
		t.Fatalf("plan = %+v, want failed-closed Homebrew inspection error", plan)
	}
}

func TestAgentPlansExposeEveryViableServerOwnedMethod(t *testing.T) {
	s := newTestService("darwin", "brew", "npm")
	s.installCapabilities = installCapabilitiesStub{prefix: "/Users/test/.npm", writable: true}
	plans, err := s.AgentPlans(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var codex AgentPlan
	for _, plan := range plans {
		if plan.AgentID == string(TargetCodex) {
			codex = plan
			break
		}
	}
	if len(codex.Methods) != 3 {
		t.Fatalf("codex methods = %+v, want homebrew, npm, and official installer", codex.Methods)
	}
	if codex.Methods[0].ID != "homebrew" || !codex.Methods[0].Recommended || !codex.Methods[0].Available {
		t.Fatalf("first method = %+v, want recommended viable homebrew", codex.Methods[0])
	}
	if codex.Methods[1].ID != "npm" || codex.Methods[1].Recommended || !codex.Methods[1].Available {
		t.Fatalf("second method = %+v, want alternate viable npm", codex.Methods[1])
	}
	if codex.Methods[2].ID != "official-installer" || codex.Methods[2].Recommended || codex.Methods[2].Available {
		t.Fatalf("third method = %+v, want unavailable official installer without sh", codex.Methods[2])
	}
	if strings.Contains(codex.Methods[0].Command, "curl") || strings.Contains(codex.Methods[1].Command, "curl") {
		t.Fatalf("codex methods include remote script execution: %+v", codex.Methods)
	}
}

func TestResolveAgentMethodRejectsUnknownOrUnavailableMethod(t *testing.T) {
	s := newTestService("darwin", "brew")
	if _, err := s.resolveAgentMethod(TargetCodex, "npm"); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("resolve npm error = %v, want unavailable", err)
	}
	if _, err := s.resolveAgentMethod(TargetCodex, "made-up"); err == nil || !strings.Contains(err.Error(), "unknown install method") {
		t.Fatalf("resolve made-up error = %v, want unknown method", err)
	}
	plan, err := s.resolveAgentMethod(TargetCodex, "homebrew")
	if err != nil || plan.Method != "homebrew" {
		t.Fatalf("resolve homebrew = %+v, %v", plan, err)
	}
}

func TestAgentTargetsAreValidButPrerequisitesAreNotHarnessRows(t *testing.T) {
	for _, target := range agentTargets {
		if !Valid(target) || !IsAgentTarget(target) {
			t.Fatalf("agent target %q is not accepted by both allowlists", target)
		}
	}
	for _, target := range []Target{TargetTmux, TargetGH, TargetClaude} {
		if !Valid(target) || IsAgentTarget(target) {
			t.Fatalf("prerequisite target %q was classified incorrectly", target)
		}
	}
}
