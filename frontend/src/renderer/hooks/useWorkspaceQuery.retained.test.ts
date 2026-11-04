import { describe, expect, it } from "vitest";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";
import { retainingStructuralSharing, retainWorkspaceSession } from "./useWorkspaceQuery";

const workspace = (sessionIds: string[]): WorkspaceSummary[] => [
	{
		id: "p1",
		name: "Project",
		path: "/p1",
		archivedCount: 0,
		sessions: sessionIds.map((id) => ({ id }) as WorkspaceSession),
	} as WorkspaceSummary,
];
const carried = (old: WorkspaceSummary[]) =>
	(retainingStructuralSharing(old, workspace([])) as WorkspaceSummary[])[0].sessions.map((session) => session.id);

describe("retained sessions", () => {
	it("carries only the 20 most recently opened dead sessions across refetches", () => {
		const ids = Array.from({ length: 25 }, (_, index) => `s-${index}`);
		for (const id of ids) retainWorkspaceSession(id);
		expect(carried(workspace(ids))).toEqual(ids.slice(5));

		// Opening one again makes it the most recent, so the next eviction skips it.
		retainWorkspaceSession("s-5");
		retainWorkspaceSession("s-new");
		const kept = carried(workspace([...ids, "s-new"]));
		expect(kept).toContain("s-5");
		expect(kept).not.toContain("s-6");
		expect(kept).toHaveLength(20);
	});
});
