import type { BacklogRunItem } from "./backlog-run-api";

/** Statuses in the order the card counts and explains them. */
export const RUN_ITEM_STATUSES = ["active", "in_review", "needs_you", "queued", "landed", "held", "failed"] as const satisfies readonly BacklogRunItem["displayStatus"][];

/** Cost as dollars ("$1.25"); a lower bound gets a "+" because some usage is unpriced. */
export function formatRunCost(nanos: number | null | undefined, complete: boolean): string | undefined {
	if (nanos === null || nanos === undefined) return undefined;
	const dollars = nanos / 1e9;
	const text = dollars > 0 && dollars < 0.01 ? "<$0.01" : `$${dollars.toFixed(2)}`;
	return complete ? text : `${text}+`;
}

/** Elapsed wall-clock as "2h 5m", "7m" or "<1m". */
export function formatRunElapsed(seconds: number | null | undefined): string | undefined {
	if (seconds === null || seconds === undefined) return undefined;
	const minutes = Math.floor(seconds / 60);
	if (minutes < 1) return "<1m";
	const hours = Math.floor(minutes / 60);
	return hours > 0 ? `${hours}h ${minutes % 60}m` : `${minutes}m`;
}

/** The plan's items grouped by lane, in lane order. */
export function runLanes(items: readonly BacklogRunItem[]): BacklogRunItem[][] {
	const lanes = new Map<number, BacklogRunItem[]>();
	for (const item of items) lanes.set(item.lane, [...(lanes.get(item.lane) ?? []), item]);
	return [...lanes.entries()].sort(([a], [b]) => a - b).map(([, group]) => [...group].sort((a, b) => a.position - b.position));
}

/** Workers the daemon lets a project run at once. */
export const MAX_RUN_WORKERS = 3;
