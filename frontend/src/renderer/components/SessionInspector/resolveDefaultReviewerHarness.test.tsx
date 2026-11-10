import { describe, expect, it } from "vitest";
import { resolveDefaultReviewerHarness } from "./ReviewsSection";

describe("resolveDefaultReviewerHarness", () => {
	it("prefers the project reviewer, then the app default, then the worker's", () => {
		const own = { reviewers: [{ harness: "kiro" }] } as never;
		expect(resolveDefaultReviewerHarness(own, "claude-code", "codex")).toBe("kiro");
		expect(resolveDefaultReviewerHarness(undefined, "claude-code", "codex")).toBe("codex");
		expect(resolveDefaultReviewerHarness(undefined, "codex", "")).toBe("codex");
		expect(resolveDefaultReviewerHarness(undefined, "claude-code", undefined)).toBe("claude-code");
	});
});
