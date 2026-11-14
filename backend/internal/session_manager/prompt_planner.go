package sessionmanager

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/plannerpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// plannerPrompt is the standing instruction of a planner session (B205): a
// read-only worker that explores the repo, asks at most five questions, and
// submits a plan through `tallpine plan`. The lens checklist and the question
// rules are the C2 workflow of the planning experiment, unchanged; the plan
// sections are generic so they fit any project. Caps match domain.MaxPlan*.
const plannerPrompt = "## Planner\n\n" +
	"You are a PLANNER, not an implementer. Do not edit files in the repo, commit, push, or open a pull request. " +
	"Use read-only commands only. Your plan will be built by an engineer who sees only the backlog item and your plan. " +
	"Everything you deliver goes through `tallpine plan`; a file you write is not delivered.\n\n" +
	domain.PlanFormatPrompt +
	"Submit the plan once. Your work is done when the plan is submitted; the owner approves it. " +
	"Reporting done does not close the backlog item, because the plan is not the work."

// plannerOrchestratorPrompt is added to an orchestrator's standing
// instructions while the planner switch is on (B205). %s is the project's
// planning-mode rule.
const plannerOrchestratorPrompt = "## Plan Before Build\n\n" +
	"Backlog work that the planning mode below says to plan gets a read-only planner session before anyone builds. Planning mode: %s\n\n" +
	"- Start a planner with `tallpine plan start <ref>`; never write the plan yourself. It asks the owner at most 5 questions (each with a recommended answer) and lists its assumptions. `tallpine plan show <ref>` prints the state; you get one message per change.\n" +
	"- The owner answers on the plan card, which is their surface: do not paste plans or questions into chat. If the owner answers in chat instead, relay exactly what they said: `tallpine plan answer <ref> --file answers.json --relayed`, or `tallpine plan defaults <ref> --relayed` when they say to go with the defaults. Never answer for the owner.\n" +
	"- A plan the owner has not approved is not a plan. Approve only when the owner said so in chat after the plan was submitted: `tallpine plan approve <ref> --owner-message last` (or the message id). The daemon refuses an approval that is not backed by an owner message newer than the plan. Never approve on your own, and never from a message that predates the submitted plan.\n" +
	"- If the owner does not reply, wait. Do not pick for them.\n" +
	"- Once approved, spawn the implementer with `tallpine spawn --backlog <ref>`: it receives the decisions table and the PR split. A planner finishing does not close the item.\n" +
	"- If the plan is stale or the owner wants another go: `tallpine plan replan <ref>`. When a stale plan meets an owner intent to continue that item, run `tallpine plan replan <ref>` or file one `tallpine ask`; never stall silently on it.\n" +
	"- Even when planning mode is off, owner decisions for multi-PR, design or research work go only through a planner's card: start or replan the planner on the relevant item and hand it the design artifacts as context. Design and research workers feed findings to the planner. Never ask the owner a list of decision questions in chat or in files."

// plannerModeRule is the planning-mode sentence of the orchestrator section.
func plannerModeRule(mode domain.PlanningMode) string {
	switch mode.WithDefault() {
	case domain.PlanningAlways:
		return "always. Plan every item before building it, unless the owner says \"just do it\" for an item."
	case domain.PlanningOff:
		return "off. Do not plan unless the owner asks (\"plan this\")."
	default:
		return "auto. Plan only work that splits into several PRs. Single-PR work (including schema or migration, cross-area or ambiguous items) goes straight to a worker, who asks its questions in its report. Never plan trivial fixes. When the owner says \"plan this\" or \"just do it\", do exactly that."
	}
}

// plannerWorkerPrompt is added to implementer instructions while the planner
// switch is on (B205): a task that carries an approved plan is built from it.
const plannerWorkerPrompt = "## Approved Plans\n\n" +
	"If your task includes an approved plan (decisions table and PR split), build from it. When you depart from a decision, report it at once: `tallpine report --checkpoint --note \"Deviation: <decision> -> <what you did and why>\"`. The reviewer checks your code against the decisions and flags a departure you did not report.\n\n" +
	"Never hand the owner a question list, in chat or in a file. Put findings and open decisions in your report; the orchestrator feeds them to a planner, whose card is the only place the owner decides."

// planningModeFor is the project's planning mode (see
// plannerpolicy.ResolvePlanningMode).
func (m *Manager) planningModeFor(ctx context.Context, cfg domain.ProjectConfig) domain.PlanningMode {
	var lock plannerpolicy.Outcome
	if m.policy != nil {
		lock = plannerpolicy.Of(m.policy.Decide(ctx, agentpolicy.SessionRef{}))
	}
	return plannerpolicy.ResolvePlanningMode(lock, cfg, m.workflowDefaults(ctx))
}
