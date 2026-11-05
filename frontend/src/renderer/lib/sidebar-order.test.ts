import { describe, expect, it } from "vitest";
import { buildSidebarSessionOrder, pinnedFirst } from "./sidebar-order";
import type { WorkspaceSummary } from "../types/workspace";

describe("pinnedFirst", () => {
	it("moves pinned items to the front and keeps each group's order", () => {
		expect(pinnedFirst(["a", "b", "c", "d"], (id) => id, ["d", "b"])).toEqual(["b", "d", "a", "c"]);
	});

	it("ignores pins for items that are not listed", () => {
		expect(pinnedFirst(["a", "b"], (id) => id, ["gone"])).toEqual(["a", "b"]);
	});
});

describe("buildSidebarSessionOrder with pinned projects", () => {
	const project = (id: string, sessionOverrides: Record<string, unknown> = {}) =>
		({ id, sessions: [{ id: `${id}-s`, ...sessionOverrides }] }) as unknown as WorkspaceSummary;
	const workspaces = [project("a"), project("b"), project("c")];
	const ids = (input: Parameters<typeof buildSidebarSessionOrder>[0]) => buildSidebarSessionOrder(input).map((entry) => entry.sessionId);

	it("lists a pinned project's sessions first", () => {
		expect(ids({ workspaces, projectOrder: ["a", "b", "c"], pinnedProjectIds: ["c"] })).toEqual(["c-s", "a-s", "b-s"]);
	});

	it("leaves the order unchanged without pins", () => {
		expect(ids({ workspaces, projectOrder: ["a", "b", "c"] })).toEqual(["a-s", "b-s", "c-s"]);
	});

	it("keeps a pinned session ahead of any project position", () => {
		const withPinnedSession = [project("a"), project("b", { isPinned: true }), project("c")];
		expect(ids({ workspaces: withPinnedSession, projectOrder: ["a", "b", "c"], pinnedProjectIds: ["c"] })).toEqual(["b-s", "c-s", "a-s"]);
	});
});
