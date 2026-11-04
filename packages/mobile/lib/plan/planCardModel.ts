/**
 * The plan card's state, shaped for a phone (B205). Mirrors the desktop
 * planDockModel: every question starts on the planner's recommended answer,
 * an assumption is accepted unless corrected, and "go with defaults" is a
 * separate explicit action. Pure, so the rules are unit-tested.
 */
export type PlanOption = { id: string; label: string };
export type PlanQuestion = { id: string; text: string; reason?: string; options: PlanOption[]; recommended: string };
export type PlanAssumption = { id: string; text: string; scopeCut?: boolean };
export type PlanView = {
	id: string;
	status: "drafting" | "awaiting_answers" | "writing" | "awaiting_approval" | "approved" | "superseded" | "failed";
	questions: PlanQuestion[];
	assumptions: PlanAssumption[];
	prSplit: { title: string; summary?: string }[];
	answers?: { questions?: Record<string, PlanAnswer>; corrections?: Record<string, string> };
	decisionsMarkdown?: string;
	planMarkdown?: string;
	failure?: string;
	stale?: boolean;
	itemChanged?: boolean;
	buildAnyway?: boolean;
	/** The planner's provider account is out of quota until this time (ISO); it resumes by itself. */
	pausedUntil?: string;
};
export type ProjectPlan = { itemRef: string; itemTitle: string; plan: PlanView };
export type { PlanAnswer, PlanAnswersBody as PlanAnswersRequest } from "@tallpine/shared/plan/plan-draft";

export {
	OTHER_OPTION, acceptedAssumptionCount, chooseOption, correct, initialDraft, isComplete, isDefaults, optionLabel, planPhase, toAnswersRequest, writeOther,
} from "@tallpine/shared/plan/plan-draft";
export type { PlanChoice, PlanDraft, PlanCardPhase } from "@tallpine/shared/plan/plan-draft";

import type { PlanAnswer } from "@tallpine/shared/plan/plan-draft";
import type { PlanCardPhase } from "@tallpine/shared/plan/plan-draft";
export const PLAN_STATUS_LABEL: Record<PlanCardPhase, string> = {
	drafting: "Planning",
	answering: "Needs your answers",
	writing: "Writing the plan",
	review: "Ready to approve",
	approved: "Approved",
	failed: "Failed",
	closed: "Closed",
};
