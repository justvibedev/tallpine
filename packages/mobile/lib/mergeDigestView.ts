import type { MergeDigest, MergeEntry, MergeOverlap } from "./api";

export type MergeDigestRow = { entry: MergeEntry; overlaps: MergeOverlap[] };
export type MergeDigestLane = { lane: string; recentlyMerged: MergeDigestRow[]; inFlight: MergeDigestRow[] };

const NO_LANE = "Unassigned";

/**
 * The digest grouped per lane, the order the daemon sent kept inside each group.
 * Overlap advice rides on the PR it is about, so "wait for #N" shows up on the
 * card of the PR that has to wait.
 */
export function mergeDigestLanes(digest: MergeDigest): MergeDigestLane[] {
	const lanes = new Map<string, MergeDigestLane>();
	const laneFor = (entry: MergeEntry) => {
		const name = entry.lane?.trim() || NO_LANE;
		let lane = lanes.get(name);
		if (!lane) lanes.set(name, (lane = { lane: name, recentlyMerged: [], inFlight: [] }));
		return lane;
	};
	const row = (entry: MergeEntry): MergeDigestRow => ({
		entry,
		overlaps: digest.overlaps.filter((overlap) => overlap.number === entry.number),
	});
	for (const entry of digest.inFlight) laneFor(entry).inFlight.push(row(entry));
	for (const entry of digest.recentlyMerged) laneFor(entry).recentlyMerged.push(row(entry));
	return [...lanes.values()].sort((a, b) => (a.lane === NO_LANE ? 1 : 0) - (b.lane === NO_LANE ? 1 : 0) || a.lane.localeCompare(b.lane));
}

export function mergeDigestEmpty(digest: MergeDigest): boolean {
	return digest.recentlyMerged.length === 0 && digest.inFlight.length === 0;
}

/** The advice line as `tallpine merges` prints it: cross-lane overlaps carry a marker. */
export function overlapLine(overlap: MergeOverlap): string {
	return overlap.crossLane ? `${overlap.advice} [cross-lane]` : overlap.advice;
}
