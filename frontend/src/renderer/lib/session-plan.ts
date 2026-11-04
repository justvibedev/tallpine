/**
 * The session-wide plan: a list the agent keeps for the whole session (Claude's
 * task list) rather than a plan for one turn. The backend marks such a plan with
 * `spansTurns`, and the agent re-sends the whole list into every later turn while
 * any task is unfinished, so most copies say nothing new.
 */

import type { ConversationPlan, PlanStep } from "../types/conversation";

/** What a timeline row says about a turn whose session-wide plan changed. */
export interface PlanUpdate {
	done: number;
	total: number;
}

/** Whether two copies of a plan list the same steps in the same states. */
export function samePlanContent(a: ConversationPlan | undefined, b: ConversationPlan | undefined): boolean {
	const left = a?.steps ?? [];
	const right = b?.steps ?? [];
	return (
		(a?.explanation ?? "") === (b?.explanation ?? "") &&
		left.length === right.length &&
		left.every((step, index) => step.text === right[index]!.text && step.status === right[index]!.status)
	);
}

/** The summary row for `next`, or undefined when it changes nothing from `previous`. */
export function planUpdateSince(
	previous: ConversationPlan | undefined,
	next: ConversationPlan,
): PlanUpdate | undefined {
	if (next.steps.length === 0 || samePlanContent(previous, next)) return undefined;
	return { done: countDone(next.steps), total: next.steps.length };
}

/** The most recent session-wide plan among the turns, oldest first. A rolled-back turn's copy is history the agent no longer has. */
export function latestSpanningPlan(
	turns: readonly { plan?: ConversationPlan; rolledBack?: boolean }[],
): ConversationPlan | undefined {
	return turns.findLast((turn) => !turn.rolledBack && turn.plan?.spansTurns)?.plan;
}

/** Open steps first (in progress, then not started), completed ones apart. */
export function splitPlanSteps(steps: readonly PlanStep[]): { open: PlanStep[]; done: PlanStep[] } {
	return {
		open: [
			...steps.filter((step) => step.status === "in_progress"),
			...steps.filter((step) => step.status === "pending"),
		],
		done: steps.filter((step) => step.status === "completed"),
	};
}

function countDone(steps: readonly PlanStep[]): number {
	return steps.filter((step) => step.status === "completed").length;
}
