package systeminstall

import (
	"context"
	"fmt"
	"strings"
)

// AgentPlans resolves one installation plan for every supported harness
// without executing anything.
func (s *Service) AgentPlans(ctx context.Context) ([]AgentPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	planner, err := s.newRequestPlanner(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AgentPlan, 0, len(agentTargets))
	for _, target := range agentTargets {
		plans := planner.agentMethodPlans(target, AgentOperationInstall)
		reinstallPlans := planner.agentMethodPlans(target, AgentOperationReinstall)
		reinstallByMethod := make(map[string]Plan, len(reinstallPlans))
		for _, reinstallPlan := range reinstallPlans {
			reinstallByMethod[reinstallPlan.Method] = reinstallPlan
		}
		recommended := recommendedPlanIndex(plans)
		plan := plans[recommended]
		updateByMethod := plansByMethod(planner.agentMethodPlans(target, AgentOperationUpdate))
		methods := make([]AgentInstallMethod, 0, len(plans))
		for index, methodPlan := range plans {
			reinstallPlan := reinstallByMethod[methodPlan.Method]
			updatePlan := operationPlanFor(updateByMethod, methodPlan.Method)
			methods = append(methods, AgentInstallMethod{
				ID: methodPlan.Method, Label: installMethodLabel(methodPlan.Method),
				Available: !methodPlan.Unsupported, Recommended: index == recommended,
				Command: displayCommand(methodPlan), Reason: methodPlan.Reason, Notice: methodPlan.Notice,
				ExpectedDestination: methodPlan.ExpectedDestination,
				ReinstallAvailable:  !reinstallPlan.Unsupported,
				ReinstallCommand:    displayCommand(reinstallPlan), ReinstallReason: reinstallPlan.Reason,
				UpdateAvailable: !updatePlan.Unsupported,
				UpdateCommand:   displayCommand(updatePlan), UpdateReason: updatePlan.Reason,
			})
		}
		out = append(out, AgentPlan{
			AgentID: string(target), Available: !plan.Unsupported,
			Automatic: !plan.Unsupported, Method: plan.Method,
			Command: displayCommand(plan), Reason: plan.Reason, Notice: plan.Notice,
			DocumentationURL:    plan.DocsURL,
			ExpectedDestination: plan.ExpectedDestination,
			Methods:             methods,
		})
	}
	return out, nil
}

func plansByMethod(plans []Plan) map[string]Plan {
	out := make(map[string]Plan, len(plans))
	for _, plan := range plans {
		out[plan.Method] = plan
	}
	return out
}

// operationPlanFor treats a method missing from a later listing, such as a
// derived owner cleared in between, as unsupported rather than misaligned.
func operationPlanFor(plans map[string]Plan, method string) Plan {
	if plan, ok := plans[method]; ok {
		return plan
	}
	return Plan{Method: method, Unsupported: true, Reason: "This installation method is no longer detected."}
}

func recommendedPlanIndex(plans []Plan) int {
	for index, plan := range plans {
		if plan.Method == "official-installer" && !plan.Unsupported {
			return index
		}
	}
	for index, plan := range plans {
		if !plan.Unsupported {
			return index
		}
	}
	return len(plans) - 1
}

func installMethodLabel(method string) string {
	switch method {
	case "homebrew":
		return "Homebrew"
	case "npm":
		return "npm"
	case "winget":
		return "winget"
	case "uv":
		return "uv tool"
	case "pipx":
		return "pipx"
	case "bun":
		return "Bun"
	case "official-installer":
		return "Official installer"
	default:
		return "Manual installation"
	}
}

func (s *Service) resolvePlan(target Target) Plan {
	if IsAgentTarget(target) {
		return s.planAgent(target)
	}
	return s.planFor(target)
}

// displayCommand renders the plan for display. Prepare steps are joined with
// "; " rather than "&&": a failed step is noted and Command still runs.
func displayCommand(plan Plan) string {
	if plan.Script != nil {
		display := fmt.Sprintf("%s <downloaded from %s>", strings.Join(plan.Script.Interpreter, " "), plan.Script.URL)
		if len(plan.Script.Args) > 0 {
			display += " " + strings.Join(plan.Script.Args, " ")
		}
		return display
	}
	argv := plan.Command
	if plan.NeedsRoot && len(argv) > 0 {
		argv = append([]string{"sudo"}, argv...)
	}
	steps := make([]string, 0, len(plan.Prepare)+1)
	for _, prepare := range plan.Prepare {
		steps = append(steps, strings.Join(prepare, " "))
	}
	return strings.Join(append(steps, strings.Join(argv, " ")), "; ")
}
