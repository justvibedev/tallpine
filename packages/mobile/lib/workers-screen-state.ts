/** How long the Workers board may sit on its loader before it owns up to a failure. */
export const WORKERS_LOADING_DEADLINE_MS = 12_000;

export type WorkersScreenState = "unpaired" | "loading" | "stalled" | "board";

/**
 * What the Workers screen shows before it has any board to draw.
 *
 * The loader used to be a bare spinner on the near-black base colour with no
 * words and no end: a paired machine that never answered looked like a black
 * screen forever. `stalled` is that same wait once the deadline has passed, and
 * it renders an error with Retry and Scan instead of spinning.
 */
export function workersScreenState(s: {
	configured: boolean;
	hostCount: number;
	loading: boolean;
	hasSessions: boolean;
	hostSelected: boolean;
	loadingExpired: boolean;
}): WorkersScreenState {
	if (!s.configured && s.hostCount === 0) return "unpaired";
	if (!s.hostSelected && s.loading && !s.hasSessions) return s.loadingExpired ? "stalled" : "loading";
	return "board";
}
