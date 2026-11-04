import { describe, expect, it } from "vitest";
import type { ConversationPlan } from "../types/conversation";
import { latestSpanningPlan, planUpdateSince, samePlanContent, splitPlanSteps } from "./session-plan";

const plan = (...statuses: Array<"pending" | "in_progress" | "completed">): ConversationPlan => ({
	spansTurns: true,
	steps: statuses.map((status, index) => ({ text: `step ${index}`, status })),
});

describe("planUpdateSince", () => {
	it("reports nothing for an identical re-sent plan", () => {
		expect(planUpdateSince(plan("completed", "pending"), plan("completed", "pending"))).toBeUndefined();
	});

	it("reports the progress of a changed plan", () => {
		expect(planUpdateSince(plan("pending", "pending"), plan("completed", "pending"))).toEqual({ done: 1, total: 2 });
	});

	it("reports a first plan, but not an empty one", () => {
		expect(planUpdateSince(undefined, plan("pending"))).toEqual({ done: 0, total: 1 });
		expect(planUpdateSince(undefined, plan())).toBeUndefined();
	});

	it("treats a new step text as a change", () => {
		const next = plan("pending");
		next.steps[0]!.text = "renamed";
		expect(samePlanContent(plan("pending"), next)).toBe(false);
	});
});

describe("splitPlanSteps", () => {
	it("puts in-progress steps before not-started ones and sets completed apart", () => {
		const { open, done } = splitPlanSteps(plan("completed", "pending", "in_progress", "completed").steps);
		expect(open.map((step) => step.text)).toEqual(["step 2", "step 1"]);
		expect(done.map((step) => step.text)).toEqual(["step 0", "step 3"]);
	});
});

describe("latestSpanningPlan", () => {
	it("ignores per-turn plans", () => {
		expect(latestSpanningPlan([{ plan: { steps: [{ text: "a", status: "pending" }] } }])).toBeUndefined();
		expect(latestSpanningPlan([{ plan: plan("pending") }, { plan: plan("completed") }])).toEqual(plan("completed"));
	});

	it("ignores rolled-back turns", () => {
		expect(latestSpanningPlan([{ plan: plan("pending") }, { plan: plan("completed"), rolledBack: true }])).toEqual(
			plan("pending"),
		);
	});
});
