import { describe, expect, it } from "vitest";
import { KNOWN_REVIEWER_HARNESS_IDS, toReviewerHarnessId } from "./reviewer-harnesses";

describe("reviewer harness vocabulary", () => {
	it("lists only the supported reviewers", () => {
		expect([...KNOWN_REVIEWER_HARNESS_IDS].sort()).toEqual(["claude-code", "codex", "deepseek-harness", "kiro"]);
	});

	it("treats a reviewer saved before its removal as unset", () => {
		expect(toReviewerHarnessId("opencode")).toBeUndefined();
		expect(toReviewerHarnessId("pi")).toBeUndefined();
		expect(toReviewerHarnessId("kiro")).toBe("kiro");
	});
});
