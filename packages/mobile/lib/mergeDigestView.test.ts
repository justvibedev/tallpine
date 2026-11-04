import { describe, expect, it } from "vitest";
import type { MergeDigest, MergeEntry } from "./api";
import { mergeDigestEmpty, overlapLine, mergeDigestLanes } from "./mergeDigestView";

const entry = (number: number, lane?: string): MergeEntry => ({ number, url: `u${number}`, title: `t${number}`, state: "open", lane, updatedAt: "2026-10-30T00:00:00Z", paths: [] });

describe("mergeDigestLanes", () => {
	const digest: MergeDigest = {
		recentlyMerged: [entry(1, "b"), entry(2), entry(5, "zeta")],
		inFlight: [entry(3, "b"), entry(4, "a")],
		overlaps: [{ number: 3, with: 1, withMerged: true, crossLane: false, paths: ["x.go"], advice: "rebase" }],
	};
	it("groups per lane, named lanes sorted, unassigned last", () => {
		expect(mergeDigestLanes(digest).map((l) => l.lane)).toEqual(["a", "b", "zeta", "Unassigned"]);
	});
	it("splits merged from in flight inside a lane", () => {
		const b = mergeDigestLanes(digest).find((l) => l.lane === "b")!;
		expect(b.recentlyMerged.map((r) => r.entry.number)).toEqual([1]);
		expect(b.inFlight.map((r) => r.entry.number)).toEqual([3]);
	});
	it("attaches overlap advice to the PR it is about", () => {
		const b = mergeDigestLanes(digest).find((l) => l.lane === "b")!;
		expect(b.inFlight[0].overlaps[0].advice).toBe("rebase");
		expect(b.recentlyMerged[0].overlaps).toEqual([]);
	});
	it("reports an empty digest", () => {
		expect(mergeDigestEmpty({ recentlyMerged: [], inFlight: [], overlaps: [] })).toBe(true);
		expect(mergeDigestEmpty(digest)).toBe(false);
	});
});

describe("overlapLine", () => {
	const overlap = { number: 3, with: 1, withMerged: false, crossLane: false, paths: [], advice: "wait for #1" };
	it("marks cross-lane overlaps", () => {
		expect(overlapLine({ ...overlap, crossLane: true })).toBe("wait for #1 [cross-lane]");
	});
	it("leaves same-lane advice as sent", () => {
		expect(overlapLine(overlap)).toBe("wait for #1");
	});
});
