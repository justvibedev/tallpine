import { describe, expect, it } from "vitest";
import type { BacklogLink } from "./backlog-api";
import { activeLinkedSessions, resolveBacklogLinks } from "./backlog-link-status";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";

const link = (kind: string, ref: string, createdAt = "2026-10-01T00:00:00Z"): BacklogLink => ({ kind, ref, display: ref, createdAt });
const session = (id: string, extra: Partial<WorkspaceSession> = {}): WorkspaceSession => ({
	id,
	workspaceId: "proj-1",
	workspaceName: "Project",
	title: `title ${id}`,
	provider: "claude-code",
	status: "working",
	updatedAt: "2026-10-01T00:00:00Z",
	prs: [],
	...extra,
});
const pr = { url: "https://github.com/o/r/pull/7", number: 7, state: "open" as const, ci: "passing", review: "none", mergeability: "clean", reviewComments: false, updatedAt: "2026-10-01T00:00:00Z" };
const workspace = (sessions: WorkspaceSession[]): WorkspaceSummary => ({ id: "proj-1", name: "Project", path: "/p", sessions });

describe("resolveBacklogLinks", () => {
	it("joins session and PR links with what the workspaces know", () => {
		const owner = session("s1", { prs: [pr] });
		const resolved = resolveBacklogLinks([link("session", "s1"), link("pr", pr.url)], [workspace([owner])]);
		expect(resolved[0].session?.id).toBe("s1");
		expect(resolved[1].pr?.number).toBe(7);
		expect(resolved[1].prSession?.id).toBe("s1");
	});

	it("falls back to display text for unknown targets and kinds without live status", () => {
		const resolved = resolveBacklogLinks([link("session", "gone"), link("pr", "https://x/pull/1"), link("issue", "#4"), link("blocked_by", "B3")], [workspace([session("s1")])]);
		expect(resolved.map((entry) => [entry.session, entry.pr])).toEqual([[undefined, undefined], [undefined, undefined], [undefined, undefined], [undefined, undefined]]);
	});
});

describe("activeLinkedSessions", () => {
	it("returns only running sessions, newest link first", () => {
		const links = [link("session", "old", "2026-10-01T00:00:00Z"), link("session", "dead", "2026-10-02T00:00:00Z"), link("session", "new", "2026-10-03T00:00:00Z")];
		const workspaces = [workspace([session("old"), session("dead", { isTerminated: true }), session("new")])];
		expect(activeLinkedSessions(links, workspaces).map((s) => s.id)).toEqual(["new", "old"]);
	});
});
