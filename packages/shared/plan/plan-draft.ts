/**
 * The plan card's draft model, shared by the desktop dock and the phone card.
 * Every question starts on the planner's recommended answer, an assumption is
 * accepted unless corrected, and "go with defaults" is a separate explicit
 * action. Pure and structural: each app passes its own PlanView type.
 */

/** The "Other" choice's option value: free text instead of one of the planner's options. */
export const OTHER_OPTION = "__other__";

export interface PlanChoice {
	/** An option id, or OTHER_OPTION. */
	selected: string;
	/** Free text, used when selected is OTHER_OPTION. */
	other: string;
}

export interface PlanDraft {
	/** Question id -> choice. */
	choices: Record<string, PlanChoice>;
	/** Assumption id -> the owner's correction. An absent id is accepted as written. */
	corrections: Record<string, string>;
}

export type PlanCardPhase = "drafting" | "answering" | "writing" | "review" | "approved" | "failed" | "closed";

export interface PlanAnswer {
	option?: string;
	other?: string;
}

export interface PlanAnswersBody {
	questions: Record<string, PlanAnswer>;
	corrections?: Record<string, string>;
}

type QuestionsOf = { questions: ReadonlyArray<{ id: string; recommended: string; options: ReadonlyArray<{ id: string; label: string }> }> };
type AssumptionsOf = { assumptions: ReadonlyArray<{ id: string }> };

export function planPhase(plan: { status: string }): PlanCardPhase {
	switch (plan.status) {
		case "drafting": return "drafting";
		case "awaiting_answers": return "answering";
		case "writing": return "writing";
		case "awaiting_approval": return "review";
		case "approved": return "approved";
		case "failed": return "failed";
		default: return "closed";
	}
}

/** The starting draft: every question on its recommended answer, no corrections. */
export function initialDraft(plan: QuestionsOf): PlanDraft {
	const choices: Record<string, PlanChoice> = {};
	for (const q of plan.questions) {
		const recommended = q.options.some((o) => o.id === q.recommended) ? q.recommended : (q.options[0]?.id ?? "");
		choices[q.id] = { selected: recommended, other: "" };
	}
	return { choices, corrections: {} };
}

export function chooseOption(draft: PlanDraft, questionId: string, selected: string): PlanDraft {
	const current = draft.choices[questionId] ?? { selected, other: "" };
	return { ...draft, choices: { ...draft.choices, [questionId]: { ...current, selected } } };
}

export function writeOther(draft: PlanDraft, questionId: string, other: string): PlanDraft {
	return { ...draft, choices: { ...draft.choices, [questionId]: { selected: OTHER_OPTION, other } } };
}

/** Sets a correction; empty text goes back to accepting the assumption. */
export function correct(draft: PlanDraft, assumptionId: string, text: string): PlanDraft {
	const corrections = { ...draft.corrections };
	if (text.trim() === "") delete corrections[assumptionId];
	else corrections[assumptionId] = text;
	return { ...draft, corrections };
}

/** True when no question is left without an answer (Other needs text). */
export function isComplete(plan: QuestionsOf, draft: PlanDraft): boolean {
	return plan.questions.every((q) => {
		const c = draft.choices[q.id];
		if (!c) return false;
		return c.selected === OTHER_OPTION ? c.other.trim() !== "" : q.options.some((o) => o.id === c.selected);
	});
}

/** True when the draft is exactly "go with defaults". */
export function isDefaults(plan: QuestionsOf, draft: PlanDraft): boolean {
	const start = initialDraft(plan);
	return Object.keys(draft.corrections).length === 0 && plan.questions.every((q) => draft.choices[q.id]?.selected === start.choices[q.id]?.selected);
}

/** The request the draft sends: every question answered, only real corrections. */
export function toAnswersRequest(plan: QuestionsOf & AssumptionsOf, draft: PlanDraft): PlanAnswersBody {
	const questions: Record<string, PlanAnswer> = {};
	for (const q of plan.questions) {
		const c = draft.choices[q.id];
		if (!c) continue;
		questions[q.id] = c.selected === OTHER_OPTION ? { other: c.other.trim() } : { option: c.selected };
	}
	const corrections: Record<string, string> = {};
	for (const a of plan.assumptions) {
		const text = draft.corrections[a.id]?.trim();
		if (text) corrections[a.id] = text;
	}
	return { questions, ...(Object.keys(corrections).length > 0 ? { corrections } : {}) };
}

/** A question's label for a given option id, falling back to the id. */
export function optionLabel(plan: QuestionsOf, questionId: string, optionId: string): string {
	return plan.questions.find((q) => q.id === questionId)?.options.find((o) => o.id === optionId)?.label ?? optionId;
}

/** How many assumptions the draft leaves uncorrected, i.e. accepted as written. */
export function acceptedAssumptionCount(plan: AssumptionsOf, draft: PlanDraft): number {
	return plan.assumptions.filter((a) => (draft.corrections[a.id] ?? "").trim() === "").length;
}
