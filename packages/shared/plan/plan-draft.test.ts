import { describe, expect, it } from "vitest";
import {
	acceptedAssumptionCount,
	OTHER_OPTION, chooseOption, correct, initialDraft, isComplete, isDefaults, optionLabel, planPhase, toAnswersRequest, writeOther,
} from "./plan-draft";

const plan = {
	id: "p1",
	status: "awaiting_answers",
	questions: [
		{ id: "q1", text: "DB?", options: [{ id: "a", label: "SQLite" }, { id: "b", label: "Postgres" }], recommended: "b" },
		{ id: "q2", text: "Auth?", options: [{ id: "x", label: "Token" }], recommended: "gone" },
	],
	assumptions: [{ id: "as1", text: "Go 1.22" }],
};

describe("plan-draft", () => {
	it("starts on the recommended answers, falling back to the first option", () => {
		const d = initialDraft(plan);
		expect(d.choices.q1?.selected).toBe("b");
		expect(d.choices.q2?.selected).toBe("x");
		expect(isDefaults(plan, d)).toBe(true);
		expect(isComplete(plan, d)).toBe(true);
	});

	it("has an empty selection for a question without options", () => {
		const empty = { questions: [{ id: "q", recommended: "", options: [] }] };
		expect(initialDraft(empty).choices.q?.selected).toBe("");
		expect(isComplete(empty, initialDraft(empty))).toBe(false);
	});

	it("is no longer defaults after a choice or correction, and Other needs text", () => {
		let d = chooseOption(initialDraft(plan), "q1", "a");
		expect(isDefaults(plan, d)).toBe(false);
		d = writeOther(initialDraft(plan), "q1", "  ");
		expect(isComplete(plan, d)).toBe(false);
		d = writeOther(d, "q1", " MySQL ");
		expect(isComplete(plan, d)).toBe(true);
		expect(isDefaults(plan, correct(initialDraft(plan), "as1", "Go 1.23"))).toBe(false);
	});

	it("an unanswered question is incomplete", () => {
		expect(isComplete(plan, { choices: {}, corrections: {} })).toBe(false);
	});

	it("builds the request with trimmed Other text and only real corrections", () => {
		let d = writeOther(initialDraft(plan), "q1", " MySQL ");
		d = correct(d, "as1", "Go 1.23");
		expect(toAnswersRequest(plan, d)).toEqual({
			questions: { q1: { other: "MySQL" }, q2: { option: "x" } },
			corrections: { as1: "Go 1.23" },
		});
		expect(toAnswersRequest(plan, correct(d, "as1", "  "))).not.toHaveProperty("corrections");
		expect(OTHER_OPTION).toBe("__other__");
	});

	it("clearing a correction goes back to accepting the assumption", () => {
		const d = correct(initialDraft(plan), "as1", "Go 1.23");
		expect(correct(d, "as1", "  ").corrections).toEqual({});
		expect(isDefaults(plan, correct(d, "as1", "  "))).toBe(true);
	});

	it("maps statuses to phases", () => {
		const phases = ["drafting", "awaiting_answers", "writing", "awaiting_approval", "approved", "failed", "superseded"].map((status) => planPhase({ status }));
		expect(phases).toEqual(["drafting", "answering", "writing", "review", "approved", "failed", "closed"]);
	});

	it("labels an option, falling back to its id", () => {
		expect(optionLabel(plan, "q1", "a")).toBe("SQLite");
		expect(optionLabel(plan, "q1", "zz")).toBe("zz");
		expect(optionLabel(plan, "nope", "a")).toBe("a");
	});
});

describe("assumption helpers", () => {
	it("counts uncorrected assumptions", () => {
		const p = { assumptions: [{ id: "a" }, { id: "b" }, { id: "c" }] };
		const base = { choices: {}, corrections: {} };
		expect(acceptedAssumptionCount(p, base)).toBe(3);
		expect(acceptedAssumptionCount(p, { ...base, corrections: { a: "x", b: "  " } })).toBe(2);
	});
});
