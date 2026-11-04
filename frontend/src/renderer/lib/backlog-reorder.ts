import type { BacklogItem } from "./backlog-api";

export type BacklogReorderTarget = { before: string } | { after: string };

/**
 * The API places an item directly before or after another one. Moving down the
 * list lands after the item now at the destination; moving up lands before it.
 * Siblings only: top-level Open items among themselves, children within their
 * parent. Returns undefined for a no-op.
 */
export function reorderTarget(siblings: readonly BacklogItem[], fromIndex: number, toIndex: number): BacklogReorderTarget | undefined {
	if (fromIndex === toIndex || fromIndex < 0 || toIndex < 0 || fromIndex >= siblings.length || toIndex >= siblings.length) return undefined;
	const destination = siblings[toIndex].ref;
	return toIndex > fromIndex ? { after: destination } : { before: destination };
}

/** Keyboard equivalent of a drag: Alt+Up / Alt+Down move one place. */
export function stepTarget(siblings: readonly BacklogItem[], ref: string, direction: "up" | "down"): BacklogReorderTarget | undefined {
	const from = siblings.findIndex((item) => item.ref === ref);
	return reorderTarget(siblings, from, direction === "up" ? from - 1 : from + 1);
}

/** The list as the user will see it right after the move, for optimistic display. */
export function applyMove<T>(list: readonly T[], fromIndex: number, toIndex: number): T[] {
	const next = [...list];
	const [moved] = next.splice(fromIndex, 1);
	next.splice(toIndex, 0, moved);
	return next;
}
