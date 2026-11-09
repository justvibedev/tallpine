import { describe, expect, it } from "vitest";
import { reviewVerdict } from "./reviewState";

type ReviewState = Parameters<typeof reviewVerdict>[0];

function state(status: string, postFailed?: boolean): ReviewState {
	return { status, prNumber: 7, latestRun: { status: "complete", postFailed } } as unknown as ReviewState;
}

describe("reviewVerdict", () => {
	it("shows a verdict that never reached the PR as a failure to post", () => {
		for (const status of ["changes_requested", "up_to_date"]) {
			const verdict = reviewVerdict(state(status, true));
			expect(verdict.tone).toBe("danger");
			expect(verdict.label).toContain("not posted to PR");
		}
	});

	it("leaves a posted verdict alone", () => {
		expect(reviewVerdict(state("up_to_date", false))).toMatchObject({ tone: "success", label: "Approved" });
	});

	it("does not mark a run still in progress", () => {
		expect(reviewVerdict(state("running", true)).label).not.toContain("not posted");
	});
});
