import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { projectBoardNavigateTarget } from "../lib/navigate-to-session";
import { projectLandingOrchestratorId } from "../lib/project-landing";
import { Route } from "../routes/_shell.projects.$projectId";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";

function session(id: string, over: Partial<WorkspaceSession> = {}): WorkspaceSession {
	return { id, kind: "worker", status: "working", createdAt: "2026-10-01T00:00:00Z", ...over } as WorkspaceSession;
}
function project(sessions: WorkspaceSession[]): WorkspaceSummary {
	return { id: "p1", name: "p1", sessions } as unknown as WorkspaceSummary;
}
const orch = (id: string, createdAt = "2026-10-01T00:00:00Z") => session(id, { kind: "orchestrator", createdAt });

function run(workspaces: WorkspaceSummary[], search: { view?: "board" } = {}) {
	const queryClient = new QueryClient();
	queryClient.setQueryData(workspaceQueryKey, workspaces);
	const before = Route.options.beforeLoad as (a: unknown) => unknown;
	try {
		before({ params: { projectId: "p1" }, search, context: { queryClient } });
	} catch (thrown) {
		return thrown as { options: { to: string; params: Record<string, string>; replace?: boolean } };
	}
	return undefined;
}

describe("project landing", () => {
	it("no workers -> redirects (replace) to the newest live orchestrator chat", () => {
		const r = run([project([orch("o-old"), orch("o-new", "2026-10-02T00:00:00Z"), session("w1", { status: "terminated", isTerminated: true })])]);
		expect(r?.options.to).toBe("/projects/$projectId/sessions/$sessionId");
		expect(r?.options.params).toEqual({ projectId: "p1", sessionId: "o-new" });
		expect(r?.options.replace).toBe(true);
	});
	it("one live worker -> board unchanged", () => {
		expect(run([project([orch("o1"), session("w1")])])).toBeUndefined();
	});
	it("no orchestrator -> board unchanged", () => {
		expect(run([project([])])).toBeUndefined();
		expect(run([project([session("w2", { status: "terminated", isTerminated: true })])])).toBeUndefined();
	});
	it("terminated orchestrator only -> board unchanged", () => {
		expect(run([project([session("o1", { kind: "orchestrator", isTerminated: true, status: "terminated" })])])).toBeUndefined();
	});
	it("explicit board link -> board even with no workers", () => {
		expect(run([project([orch("o1")])], { view: "board" })).toBeUndefined();
		expect(projectBoardNavigateTarget("p1").search).toEqual({ view: "board" });
	});
	it("unknown project or cold cache -> board", () => {
		expect(projectLandingOrchestratorId(undefined, "p1")).toBeUndefined();
		expect(projectLandingOrchestratorId([project([orch("o1")])], "other")).toBeUndefined();
	});
});
