import { describe, expect, it } from "vitest";
import { effectiveStrongerReviewer, reviewDefaultsConfig, reviewDefaultsDraft } from "./project-review-defaults";

describe("project review defaults", () => {
	it("auto review is tri-state: unset inherits, an explicit false stays false", () => {
		expect(reviewDefaultsDraft({}).autoReview).toBeNull();
		expect(reviewDefaultsDraft({ autoReview: false }).autoReview).toBe(false);
		expect(reviewDefaultsConfig({}, { autoReview: null }).autoReview).toBeUndefined();
		expect(reviewDefaultsConfig({ autoReview: true }, { autoReview: null }).autoReview).toBeUndefined();
		expect(reviewDefaultsConfig({}, { autoReview: false }).autoReview).toBe(false);
		expect(reviewDefaultsConfig({}, { autoReview: true }).autoReview).toBe(true);
	});

	it("treats end-on-merge as on unless stored off", () => {
		expect(reviewDefaultsDraft({}).terminateOnPrMerge).toBe(true);
		expect(reviewDefaultsConfig({}, { terminateOnPrMerge: true }).terminateOnPrMerge).toBeUndefined();
		expect(reviewDefaultsConfig({}, { terminateOnPrMerge: false }).terminateOnPrMerge).toBe(false);
	});

	it("round-trips the stronger reviewer, paths and first-review-only", () => {
		const stored = { reviewRisk: { harness: "codex", model: "big", effort: "high", paths: ["db/**", "auth/**"], firstReviewOnly: true } };
		const draft = reviewDefaultsDraft(stored);
		expect(draft).toMatchObject({ reviewRiskHarness: "codex", reviewRiskModel: "big", reviewRiskEffort: "high", reviewRiskPaths: "db/**, auth/**", reviewRiskFirstOnly: true });
		expect(reviewDefaultsConfig(stored, draft).reviewRisk).toEqual(stored.reviewRisk);
	});

	it("leaves reviewRisk out when emptied so the built-in defaults apply", () => {
		expect(reviewDefaultsConfig({}, { reviewRiskModel: " ", reviewRiskPaths: "" }).reviewRisk).toBeUndefined();
	});

	it("resolves the built-in stronger reviewer like the daemon", () => {
		expect(effectiveStrongerReviewer({})).toEqual({ harness: "claude-code", model: "claude-opus-5-5", effort: "high", builtIn: true });
		expect(effectiveStrongerReviewer({ reviewRiskHarness: "codex" })).toEqual({ harness: "codex", model: "", effort: "", builtIn: false });
		expect(effectiveStrongerReviewer({ reviewRiskModel: "m" })).toMatchObject({ harness: "claude-code", model: "m", effort: "" });
	});
});
