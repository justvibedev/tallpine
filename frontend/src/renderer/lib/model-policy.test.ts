import { describe, expect, it } from "vitest";
import { filterDeniedModels, isModelDenied } from "./model-policy";

const models = [{ id: "gpt-6-astra" }, { id: "Blocked-X" }, { id: "gpt-x" }];

describe("model policy", () => {
	it("hides the shipped and the project's denied models, case-insensitively", () => {
		expect(filterDeniedModels(models, { denylist: ["blocked-x"] }).map((m) => m.id)).toEqual(["gpt-x"]);
		expect(isModelDenied("GPT-6-ASTRA")).toBe(true);
	});

	it("lets an allowlist exempt a denied model", () => {
		expect(filterDeniedModels(models, { allowlist: ["gpt-6-astra"] }).map((m) => m.id)).toEqual(["gpt-6-astra", "Blocked-X", "gpt-x"]);
	});

	it("never falls back to the whole list when everything is denied", () => {
		expect(filterDeniedModels([{ id: "gpt-6-astra" }])).toEqual([]);
	});
});
