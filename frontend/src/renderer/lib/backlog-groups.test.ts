import { describe, expect, it } from "vitest";
import { backlogItem as item, backlogNow, daysAgo } from "../test/backlog-fixtures";
import { BACKLOG_STALE_DAYS, groupBacklog } from "./backlog-groups";

const refs = (items: readonly { ref: string }[]) => items.map((entry) => entry.ref);

describe("groupBacklog", () => {
	it("splits items into inbox, open, held, stale and archive in drag order", () => {
		const second = item({ ref: "B2", position: 2 });
		const first = item({ ref: "B1", position: 1 });
		const waiting = item({ ref: "B3", state: "inbox", position: 3 });
		const held = item({ ref: "B4", hold: "after the rename", position: 4 });
		const stale = item({ ref: "B5", updatedAt: daysAgo(BACKLOG_STALE_DAYS + 1), position: 5 });
		const closed = item({ ref: "B6", state: "done", closedAt: daysAgo(2), position: 6 });
		const g = groupBacklog([second, first, waiting, held, stale, closed], backlogNow);
		expect(refs(g.open)).toEqual(["B1", "B2"]);
		expect(refs(g.inbox)).toEqual(["B3"]);
		expect(refs(g.held)).toEqual(["B4"]);
		expect(refs(g.stale)).toEqual(["B5"]);
		expect(refs(g.archive)).toEqual(["B6"]);
	});

	it("keeps an item that is still active or in review out of the stale row", () => {
		const old = daysAgo(BACKLOG_STALE_DAYS + 30);
		const active = item({ ref: "B1", updatedAt: old, badges: { active: true, inReview: false, landed: false } });
		const review = item({ ref: "B2", updatedAt: old, badges: { active: false, inReview: true, landed: false } });
		const g = groupBacklog([active, review], backlogNow);
		expect(refs(g.open)).toEqual(["B1", "B2"]);
		expect(g.stale).toEqual([]);
	});

	it("uses the sixty day threshold", () => {
		const justInside = item({ ref: "B1", updatedAt: daysAgo(BACKLOG_STALE_DAYS - 1) });
		const justOutside = item({ ref: "B2", updatedAt: daysAgo(BACKLOG_STALE_DAYS + 1) });
		const g = groupBacklog([justInside, justOutside], backlogNow);
		expect(BACKLOG_STALE_DAYS).toBe(60);
		expect(refs(g.open)).toEqual(["B1"]);
		expect(refs(g.stale)).toEqual(["B2"]);
	});

	it("nests children one level under a live parent and counts done and dropped as finished", () => {
		const parent = item({ id: "p", ref: "B1", position: 1, childCount: 5, childClosedCount: 3 });
		const kids = [
			item({ ref: "B2", parentId: "p", position: 2 }),
			item({ ref: "B3", parentId: "p", position: 1 }),
			item({ ref: "B4", parentId: "p", state: "done", closedAt: daysAgo(1) }),
			item({ ref: "B5", parentId: "p", state: "dropped", closedAt: daysAgo(1) }),
			item({ ref: "B6", parentId: "p", state: "done", closedAt: daysAgo(1) }),
		];
		const g = groupBacklog([parent, ...kids], backlogNow);
		expect(refs(g.open)).toEqual(["B1"]);
		expect(refs(g.childrenByParent.get("p") ?? [])).toEqual(["B3", "B2", "B4", "B5", "B6"]);
		expect(g.progress.get("p")).toEqual({ done: 3, total: 5 });
	});

	it("takes progress from the server's counts, not from the closed children it fetched", () => {
		const parent = item({ id: "p", ref: "B1", childCount: 600, childClosedCount: 590 });
		const onlyOpenChildren = [item({ ref: "B2", parentId: "p" }), item({ ref: "B3", parentId: "p" })];
		const g = groupBacklog([parent, ...onlyOpenChildren], backlogNow);
		expect(g.progress.get("p")).toEqual({ done: 590, total: 600 });
	});

	it("lists a child on its own when its parent is closed so it never disappears", () => {
		const parent = item({ id: "p", ref: "B1", state: "done", closedAt: daysAgo(1) });
		const child = item({ ref: "B2", parentId: "p" });
		const g = groupBacklog([parent, child], backlogNow);
		expect(refs(g.open)).toEqual(["B2"]);
		expect(g.childrenByParent.get("p")).toBeUndefined();
	});

	it("orders the archive by most recently closed", () => {
		const older = item({ ref: "B1", state: "dropped", closedAt: daysAgo(9) });
		const newer = item({ ref: "B2", state: "done", closedAt: daysAgo(1) });
		expect(refs(groupBacklog([older, newer], backlogNow).archive)).toEqual(["B2", "B1"]);
	});
});
