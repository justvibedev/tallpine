import type { BacklogItem } from "../lib/backlog-api";

const NOW = Date.parse("2026-10-28T12:00:00Z");
export const backlogNow = NOW;
export const daysAgo = (days: number) => new Date(NOW - days * 24 * 60 * 60 * 1000).toISOString();

let counter = 0;

/** A backlog item as the API returns it; override only what the test cares about. */
export function backlogItem(overrides: Partial<BacklogItem> = {}): BacklogItem {
	counter += 1;
	return {
		id: `item-${counter}`,
		ref: `B${counter}`,
		projectId: "proj-1",
		title: `Item ${counter}`,
		note: "",
		doneWhen: "",
		state: "open",
		hold: "",
		childCount: 0,
		childClosedCount: 0,
		position: counter,
		origin: { kind: "human" },
		agentNotes: "",
		fields: {},
		createdAt: daysAgo(3),
		updatedAt: daysAgo(1),
		links: [],
		linkCounts: {},
		openBlockers: [],
		badges: { active: false, inReview: false, landed: false },
		...overrides,
	};
}
