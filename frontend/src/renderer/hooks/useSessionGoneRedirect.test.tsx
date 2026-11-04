import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useSessionGoneRedirect } from "./useSessionGoneRedirect";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";

const navigateMock = vi.hoisted(() => vi.fn());
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => navigateMock }));

const orch = (id: string, lane?: string) =>
	({ id, kind: "orchestrator", workspaceId: "ao", lane, status: "working", isTerminated: false, createdAt: "2026-10-30T09:00:00Z" }) as unknown as WorkspaceSession;
const project = (sessions: WorkspaceSession[]) => [{ id: "ao", name: "ao", sessions }] as unknown as WorkspaceSummary[];

describe("useSessionGoneRedirect", () => {
	beforeEach(() => navigateMock.mockReset());

	it("leaves an ended lane orchestrator for the default orchestrator, never another lane", () => {
		const lane = orch("tallpine-291", "B1");
		const main = orch("tallpine-233");
		const otherLane = orch("tallpine-300", "B2");
		const { result, rerender } = renderHook((p: { session?: WorkspaceSession }) =>
			useSessionGoneRedirect({ sessionId: "tallpine-291", projectId: "ao", session: p.session, settled: true, workspaces: project([main, otherLane]), enabled: true }),
			{ initialProps: { session: lane as WorkspaceSession | undefined } });
		expect(result.current).toBe(false);
		expect(navigateMock).not.toHaveBeenCalled();
		rerender({ session: undefined });
		expect(result.current).toBe(true);
		expect(navigateMock).toHaveBeenCalledTimes(1);
		expect(navigateMock).toHaveBeenCalledWith({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "ao", sessionId: "tallpine-233" } });
	});

	it("falls back to the project board when no default orchestrator runs", () => {
		const { rerender } = renderHook((p: { session?: WorkspaceSession }) =>
			useSessionGoneRedirect({ sessionId: "tallpine-291", projectId: "ao", session: p.session, settled: true, workspaces: project([]), enabled: true }),
			{ initialProps: { session: orch("tallpine-291", "B1") as WorkspaceSession | undefined } });
		rerender({ session: undefined });
		expect(navigateMock).toHaveBeenCalledWith(expect.objectContaining({ to: "/projects/$projectId", params: { projectId: "ao" } }));
	});

	it("does not redirect while lookups are loading, or for an id that was never seen", () => {
		const { result } = renderHook(() =>
			useSessionGoneRedirect({ sessionId: "tallpine-404", projectId: "ao", session: undefined, settled: true, workspaces: project([orch("tallpine-233")]), enabled: true }));
		expect(result.current).toBe(false);
		const loading = renderHook((p: { settled: boolean }) =>
			useSessionGoneRedirect({ sessionId: "tallpine-291", projectId: "ao", session: undefined, settled: p.settled, workspaces: project([orch("tallpine-233")]), enabled: true }),
			{ initialProps: { settled: false } });
		expect(loading.result.current).toBe(false);
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("does not react to another session's removal", () => {
		const { result } = renderHook(() =>
			useSessionGoneRedirect({ sessionId: "tallpine-233", projectId: "ao", session: orch("tallpine-233"), settled: true, workspaces: project([orch("tallpine-233")]), enabled: true }));
		expect(result.current).toBe(false);
		expect(navigateMock).not.toHaveBeenCalled();
	});
});
