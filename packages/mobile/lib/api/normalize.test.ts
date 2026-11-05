import { describe, expect, it } from "vitest";
import { mapPR, mapSession } from "./normalize";

const base = { url: "https://github.com/o/r/pull/1", number: 1 };

describe("mapPR", () => {
	it("keeps a draft as an open PR flagged isDraft", () => {
		expect(mapPR({ ...base, state: "draft" })).toMatchObject({ state: "open", isDraft: true });
	});

	it.each([
		["merged", "merged"],
		["closed", "closed"],
		["open", "open"],
		[undefined, "open"],
		["bogus", "open"],
	] as const)("maps state %s to %s", (state, expected) => {
		expect(mapPR({ ...base, state: state as never }).state).toBe(expected);
	});

	it.each([
		["passing", "passing"],
		["failing", "failing"],
		["pending", "pending"],
		["unknown", "none"],
		[undefined, "none"],
	] as const)("maps ci %s to %s", (ci, expected) => {
		expect(mapPR({ ...base, ci: ci as never }).ciStatus).toBe(expected);
	});

	it.each([
		["approved", "approved"],
		["changes_requested", "changes_requested"],
		["review_required", "pending"],
		["none", "none"],
		[undefined, "none"],
	] as const)("maps review %s to %s", (review, expected) => {
		expect(mapPR({ ...base, review: review as never }).reviewDecision).toBe(expected);
	});

	it("derives mergeable and unresolved threads", () => {
		expect(mapPR({ ...base, mergeability: "mergeable", reviewComments: true })).toMatchObject({
			mergeability: { state: "mergeable", mergeable: true },
			unresolvedThreads: 1,
		});
		expect(mapPR(base)).toMatchObject({ mergeability: { state: "unknown", mergeable: false }, unresolvedThreads: 0 });
	});
});

describe("mapSession artifacts", () => {
	it("leaves artifacts unknown for a board row that omits the file list, and flags hasArtifacts", () => {
		const row = mapSession({ id: "w-1", hasArtifacts: true } as never);
		expect(row.artifacts).toBeUndefined();
		expect(row.hasArtifacts).toBe(true);
	});

	it("lists the files a single-session read carries", () => {
		const file = { path: "a.md", name: "a.md", kind: "markdown", size: 1, updatedAt: "2026-11-01T00:00:00Z" };
		const read = mapSession({ id: "w-1", artifactFiles: [file] } as never);
		expect(read.artifacts).toEqual([file]);
		expect(read.hasArtifacts).toBe(true);
		expect(mapSession({ id: "w-1", artifactFiles: [] } as never)).toMatchObject({ artifacts: [], hasArtifacts: false });
	});
});
