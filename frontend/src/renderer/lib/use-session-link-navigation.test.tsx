import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { useSessionLinkNavigation } from "./use-session-link-navigation";

const mocks = vi.hoisted(() => ({ navigate: vi.fn(), workspace: vi.fn(), cloudProjects: vi.fn(), cloudSessions: vi.fn(), fetchSession: vi.fn() }));
vi.mock("./navigate-to-session", () => ({ useNavigateToSession: () => mocks.navigate }));
vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => mocks.workspace(),
	fetchSessionView: (...args: unknown[]) => mocks.fetchSession(...args),
	useCloudProjectsQuery: () => mocks.cloudProjects(),
	useCloudSessionsQuery: () => mocks.cloudSessions(),
	toCloudWorkspace: (project: { id: string; displayName?: string }, sessions: Array<{ id: string; projectId: string }>) => ({
		id: project.id,
		name: project.displayName ?? project.id,
		path: "",
		sessions: sessions.filter((session) => session.projectId === project.id),
	}),
	remoteWorkspaceQueryKey: (hostId: string) => ["remote-workspaces", hostId],
}));

describe("useSessionLinkNavigation", () => {
	beforeEach(() => {
		mocks.navigate.mockReset();
		mocks.fetchSession.mockReset().mockResolvedValue(undefined);
		mocks.workspace.mockClear();
		mocks.cloudProjects.mockClear();
		mocks.cloudSessions.mockClear();
		mocks.cloudProjects.mockReturnValue({ isSuccess: true, data: [] });
		mocks.cloudSessions.mockReturnValue({ isSuccess: true, data: [] });
		useUiStore.setState({ globalToast: null, globalToasts: [], globalToastSequence: 0 });
		mocks.workspace.mockReturnValue({
			isSuccess: true,
			data: [
				{ id: "other-project", sessions: [{ id: "other-session", isTerminated: false }] },
				{ id: "project", sessions: [{ id: "session", title: "renamed", isTerminated: false }, { id: "terminated", isTerminated: true }] },
			],
		});
	});

	it("selects the exact cross-project session by stable ID", () => {
		const { result } = renderHook(() => useSessionLinkNavigation());
		act(() => expect(result.current("tallpine://sessions/other-project/other-session")).toBe(true));
		expect(mocks.navigate).toHaveBeenCalledWith("other-project", "other-session");
		expect(useUiStore.getState().globalToasts).toEqual([]);
	});

	it("resolves a remote chat link on its source host without querying local or Cloud", () => {
		mocks.workspace.mockReturnValue({ isSuccess: true, data: [{ id: "project", sessions: [{ id: "session", isTerminated: true }] }] });
		const queryClient = new QueryClient();
		queryClient.setQueryData(["remote-workspaces", "box-a"], [{ id: "project", sessions: [{ id: "session", isTerminated: false }] }]);
		queryClient.setQueryData(["remote-workspaces", "box-b"], [{ id: "project", sessions: [{ id: "session", isTerminated: true }] }]);
		const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
		const { result } = renderHook(() => useSessionLinkNavigation("box-a"), { wrapper });
		act(() => expect(result.current("tallpine://sessions/project/session")).toBe(true));
		expect(mocks.navigate).toHaveBeenCalledWith("project", "session", "box-a");
		expect(mocks.workspace).not.toHaveBeenCalled();
		expect(mocks.cloudProjects).not.toHaveBeenCalled();
		expect(mocks.cloudSessions).not.toHaveBeenCalled();
	});


	it("shows feedback instead of navigating to a terminated session", () => {
		const { result } = renderHook(() => useSessionLinkNavigation());
		act(() => expect(result.current("tallpine://sessions/project/terminated")).toBe(false));
		act(() => expect(result.current("tallpine://sessions/project/terminated")).toBe(false));
		expect(mocks.navigate).not.toHaveBeenCalled();
		expect(useUiStore.getState().globalToasts).toEqual([
			expect.objectContaining({
				title: "Session terminated is terminated",
				nonce: 2,
				placement: "top-center",
				dismissible: true,
				durationMs: 5_000,
				dedupeKey: "session-link:project:terminated",
			}),
		]);
	});

	it("navigates to the session and requests the artifact open, even after termination", () => {
		useUiStore.setState({ workspaceFileOpenRequest: null });
		const { result } = renderHook(() => useSessionLinkNavigation());
		act(() => expect(result.current("tallpine://sessions/project/terminated/artifacts/plans/my%20plan.md")).toBe(true));
		expect(mocks.navigate).toHaveBeenCalledWith("project", "terminated");
		expect(useUiStore.getState().workspaceFileOpenRequest).toEqual(expect.objectContaining({
			sessionId: "terminated",
			path: "plans/my plan.md",
			source: "artifact",
		}));
		expect(useUiStore.getState().globalToasts).toEqual([]);
	});

	it("rejects a malformed link with actionable feedback", () => {
		const { result } = renderHook(() => useSessionLinkNavigation());
		act(() => expect(result.current("tallpine://sessions/project/session/kill")).toBe(false));
		expect(mocks.navigate).not.toHaveBeenCalled();
		expect(useUiStore.getState().globalToasts.at(-1)).toEqual(expect.objectContaining({
			title: expect.stringContaining("malformed or unsupported"),
			tone: "error",
			placement: "top-center",
			dismissible: true,
			dedupeKey: "session-link:error",
		}));
	});

	it("reads a session the live list omits by id, and reports one the daemon does not have", async () => {
		const { result } = renderHook(() => useSessionLinkNavigation());
		act(() => expect(result.current("tallpine://sessions/project/missing")).toBe(true));
		await waitFor(() => expect(useUiStore.getState().globalToasts.at(-1)).toEqual(expect.objectContaining({
			title: expect.stringContaining("missing or is not accessible"),
			tone: "error",
			dedupeKey: "session-link:error",
		})));
		expect(mocks.fetchSession).toHaveBeenCalledWith(undefined, "missing");
		expect(mocks.navigate).not.toHaveBeenCalled();
	});

	it("opens a terminated session's artifact link although the live list omits the session", async () => {
		mocks.fetchSession.mockResolvedValue({ id: "gone", projectId: "project", isTerminated: true, status: "terminated" });
		const { result } = renderHook(() => useSessionLinkNavigation());
		act(() => expect(result.current("tallpine://sessions/project/gone")).toBe(true));
		await waitFor(() => expect(useUiStore.getState().globalToasts.at(-1)?.title).toContain("Session gone is terminated"));
		expect(mocks.navigate).not.toHaveBeenCalled();
	});

	it("rejects a session link whose project does not match the session read by id", async () => {
		mocks.fetchSession.mockResolvedValue({ id: "elsewhere", projectId: "other-project", isTerminated: false });
		const { result } = renderHook(() => useSessionLinkNavigation());
		act(() => expect(result.current("tallpine://sessions/project/elsewhere")).toBe(true));
		await waitFor(() => expect(useUiStore.getState().globalToasts.at(-1)?.title).toContain("missing or is not accessible"));
		expect(mocks.navigate).not.toHaveBeenCalled();
	});

	it("does not navigate when the workspace cannot be verified", () => {
		mocks.workspace.mockReturnValue({ isSuccess: false, data: undefined });
		const { result } = renderHook(() => useSessionLinkNavigation());
		act(() => expect(result.current("tallpine://sessions/project/session")).toBe(false));
		expect(useUiStore.getState().globalToasts.at(-1)?.title).toContain("daemon connection");
	});
});
