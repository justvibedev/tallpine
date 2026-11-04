import { laneLabel } from "./laneSelection";

export const ALL_WORKER_LANES = "all";
/** The chip for workers outside any lane; named like the lane switcher's default lane. */
export const DEFAULT_WORKER_LANE = "default";

type Laned = { workerLane?: string; isTerminated?: boolean };

export type WorkerLaneChip = { id: string; label: string };

/**
 * Lane chips for the board: one per lane that has a worker on the board, with
 * the lane switcher's "Default" for un-laned workers once a lane exists.
 * Empty when no worker is laned, so projects without lanes show no filter.
 */
export function workerLaneChips(all: readonly Laned[]): WorkerLaneChip[] {
	// A lane exists only while a live worker is in it; the daemon omits workerLane
	// for ended workers, and this also covers an older daemon that does not.
	const sessions = all.filter((s) => !s.isTerminated);
	const lanes = [...new Set(sessions.flatMap((s) => (s.workerLane ? [s.workerLane] : [])))].sort((a, b) => a.localeCompare(b, undefined, { numeric: true }));
	if (lanes.length === 0) return [];
	const chips: WorkerLaneChip[] = [];
	if (sessions.some((s) => !s.workerLane)) chips.push({ id: DEFAULT_WORKER_LANE, label: laneLabel({}) });
	return [...chips, ...lanes.map((lane) => ({ id: lane, label: laneLabel({ lane }) }))];
}

export function filterWorkersByLane<T extends Laned>(sessions: readonly T[], laneId: string): T[] {
	if (laneId === ALL_WORKER_LANES) return [...sessions];
	if (laneId === DEFAULT_WORKER_LANE) return sessions.filter((s) => !s.workerLane);
	return sessions.filter((s) => s.workerLane === laneId);
}

/** A remembered lane that no longer has a chip falls back to All rather than an empty board. */
export function effectiveWorkerLane(chips: readonly WorkerLaneChip[], laneId: string): string {
	return chips.some((chip) => chip.id === laneId) ? laneId : ALL_WORKER_LANES;
}
