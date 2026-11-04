import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider
} from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { appI18n } from "../i18n";
import type { WorkspaceSummary } from "../types/workspace";

const { getMock, hasTrustedApiBaseUrlMock, listProjectsMock, listSessionsMock, remoteGetMock } = vi.hoisted(
	() => ({
		getMock: vi.fn(),
		hasTrustedApiBaseUrlMock: vi.fn(() => true),
		listProjectsMock: vi.fn(),
		listSessionsMock: vi.fn(),
		remoteGetMock: vi.fn(),
	}),
);

vi.mock("../lib/api-client", () => ({
	apiErrorCode: (error: unknown) => (error as { code?: string } | undefined)?.code,
	apiClient: { GET: getMock },
	hasTrustedApiBaseUrl: hasTrustedApiBaseUrlMock,
}));

vi.mock("../lib/host-clients", () => ({ clientForHost: () => ({ GET: remoteGetMock }), connectedHosts: () => [], subscribeConnectedHosts: () => () => undefined }));



import { forgetSessionArtifactFiles, refreshWorkspaceSession, sessionArtifactsQueryKey, useWorkspacePolling, useWorkspaceQuery, useWorkspaceScope, useWorkspaceSession, selectTraySessions, useWorkspaceTraySessions, workspaceQueryKey, workspaceQueryKeyForHost } from "./useWorkspaceQuery";

it("preserves the existing local and remote workspace cache keys", () => {
	expect(workspaceQueryKeyForHost()).toBe(workspaceQueryKey);
	expect(workspaceQueryKeyForHost("box-a")).toEqual(["remote-workspaces", "box-a"]);
});

function wrapper({ children }: { children: ReactNode }) {
	// The hook pins its own retry policy; retryDelay 0 keeps the error tests fast.
	const queryClient = new QueryClient({ defaultOptions: { queries: { retryDelay: 0 } } });
	return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

function respondWith(payload: {
	projects?: { data?: unknown; error?: unknown };
	sessions?: { data?: unknown; error?: unknown };
}) {
	getMock.mockImplementation(async (url: string) => {
		if (url === "/api/v1/projects") return payload.projects ?? { data: { projects: [] }, error: undefined };
		if (url === "/api/v1/sessions") return payload.sessions ?? { data: { sessions: [] }, error: undefined };
		throw new Error(`unexpected GET ${url}`);
	});
}

beforeEach(() => {
	getMock.mockReset();
	hasTrustedApiBaseUrlMock.mockReset().mockReturnValue(true);
	listProjectsMock.mockReset();
	listSessionsMock.mockReset().mockResolvedValue({ items: [] });
	remoteGetMock.mockReset();
});

describe("useWorkspaceQuery", () => {
	it.each(["pending", "failed", "removed"])("keeps local workspace cleanup state %s", async (workspaceCleanup) => {
		respondWith({
			projects: { data: { projects: [{ id: "p1", name: "Project", path: "/tmp/project" }] } },
			sessions: { data: { sessions: [{ id: "s1", projectId: "p1", harness: "codex", status: "working", statusReadiness: "ready",
				activity: { state: "idle", lastActivityAt: "2026-01-01T00:00:00Z" }, isTerminated: true, workspaceCleanup, prs: [] }] } },
		});
		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(result.current.data?.[0].sessions[0].workspaceCleanup).toBe(workspaceCleanup);
	});

	it.each(["checking", "unavailable"] as const)("does not expose unverified activity while %s", async (statusReadiness) => {
		respondWith({
			projects: { data: { projects: [{ id: "p1", name: "Project", path: "/tmp/project" }] } },
			sessions: { data: { sessions: [{ id: "s1", projectId: "p1", harness: "codex", status: "working", statusReadiness,
				activity: { state: "active", lastActivityAt: "2026-01-01T00:00:00Z" }, updatedAt: "2026-01-01T00:00:00Z", prs: [] }] } },
		});
		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(result.current.data?.[0].sessions[0]).toMatchObject({ status: "unknown", statusReadiness });
		expect(result.current.data?.[0].sessions[0].activity).toBeUndefined();
	});

	it("keeps each artifact's inline URL, which the chat frames an HTML artifact from", async () => {
		const inlineUrl = "http://tallpine-inline-artifact.x.localhost:3101/q3/report.html";
		respondWith({
			projects: { data: { projects: [{ id: "p1", name: "Project", path: "/tmp/project" }] } },
			sessions: { data: { sessions: [{ id: "s1", projectId: "p1", harness: "codex", status: "working", statusReadiness: "ready",
				activity: { state: "active", lastActivityAt: "2026-01-01T00:00:00Z" }, updatedAt: "2026-01-01T00:00:00Z", prs: [],
				artifactFiles: [{ path: "q3/report.html", name: "report.html", kind: "html", size: 12, updatedAt: "2026-01-01T00:00:00Z", inlineUrl }] }] } },
		});
		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(result.current.data?.[0].sessions[0].artifactFiles?.[0]).toMatchObject({ path: "q3/report.html", inlineUrl });
	});

	it("rejects workspace reads while the daemon base URL is untrusted", async () => {
		hasTrustedApiBaseUrlMock.mockReturnValue(false);

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });

		await waitFor(() => expect(result.current.isError).toBe(true));
		expect(result.current.error).toEqual(new Error("Tallpine daemon API is not ready"));
		expect(getMock).not.toHaveBeenCalled();
	});

	it("fetches as soon as the daemon is ready instead of waiting out a retry backoff", async () => {
		hasTrustedApiBaseUrlMock.mockReturnValue(false);
		respondWith({ projects: { data: { projects: [{ id: "p1", name: "Project", path: "/p" }] } } });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retryDelay: 60_000 } } });
		const slowRetryWrapper = ({ children }: { children: ReactNode }) => (
			<QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
		);

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper: slowRetryWrapper });
		await waitFor(() => expect(result.current.failureCount).toBe(1));

		hasTrustedApiBaseUrlMock.mockReturnValue(true);
		void queryClient.invalidateQueries({ queryKey: workspaceQueryKey }, { cancelRefetch: false });

		await waitFor(() => expect(result.current.isSuccess).toBe(true), { timeout: 1_000 });
		expect(result.current.data?.[0]).toMatchObject({ id: "p1" });
	});

	it("maps projects and their sessions, applying provider/status/title fallbacks", async () => {
		respondWith({
			projects: {
				data: {
					projects: [
						{
							id: "proj-1",
							name: "my-app",
							path: "/home/me/my-app",
							orchestratorAgent: "codex",
						},
					],
				},
				error: undefined,
			},
			sessions: {
				data: {
					sessions: [
						{
							id: "sess-1",
							projectId: "proj-1",
							terminalHandleId: "term-1",
							terminalGeneration: "launch-2",
							displayName: "fix-bug",
							issueId: "github:acme/project-one#42",
							harness: "claude-code",
							reviewerHarness: "kiro",
							branch: "qa/modal-worker",
							status: "mergeable",
							scmStatus: "review_pending",
							kanbanColumn: "ready",
							displayStatus: "Mergeable",
							isTerminated: false,
							autoInjectReview: false,
							autoInjectCI: false,
							activity: { state: "idle", lastActivityAt: "2026-06-10T15:30:00Z" },
							lastUserMessageAt: "2026-06-10T16:10:00Z",
							updatedAt: "2026-06-10T16:15:04Z",
						},
						{
							// Unknown harness/status and no displayName/issueId: falls back
							// to codex / unknown / the session id.
							id: "sess-2",
							projectId: "proj-1",
							harness: "mystery-agent",
							reviewerHarness: "mystery-reviewer",
							status: "bogus",
							isTerminated: false,
							updatedAt: "2026-06-10T16:15:04Z",
						},
						// Belongs to another project; must not leak into proj-1.
						{ id: "sess-3", projectId: "proj-2", isTerminated: false, updatedAt: "2026-06-10T16:15:04Z" },
					],
				},
				error: undefined,
			},
		});

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));

		const [workspace] = result.current.data ?? [];
		expect(workspace).toMatchObject({
			id: "proj-1",
			name: "my-app",
			path: "/home/me/my-app",
			orchestratorAgent: "codex",
		});
		expect(workspace.sessions).toHaveLength(2);
		expect(workspace.sessions[0]).toMatchObject({
			id: "sess-1",
			terminalHandleId: "term-1",
			terminalGeneration: "launch-2",
			title: "fix-bug",
			issueId: "github:acme/project-one#42",
			provider: "claude-code",
			reviewerHarness: "kiro",
			branch: "qa/modal-worker",
			status: "mergeable",
			scmStatus: "review_pending",
			kanbanColumn: "ready",
			displayStatus: "Mergeable",
			activity: { state: "idle", lastActivityAt: "2026-06-10T15:30:00Z" },
			lastUserMessageAt: "2026-06-10T16:10:00Z",
			autoInjectReview: false,
			autoInjectCI: false,
		});
		expect(workspace.sessions[1]).toMatchObject({
			id: "sess-2",
			title: "sess-2",
			provider: "codex",
			reviewerHarness: undefined,
			status: "unknown",
			branch: undefined,
			autoInjectReview: true,
			autoInjectCI: true,
		});
	});

	it("preserves scratch projects and leaves branchless scratch sessions branchless", async () => {
		respondWith({
			projects: {
				data: {
					projects: [
						{
							id: "scratch",
							name: "Scratch",
							kind: "scratch",
							path: "/home/me/.tallpine/scratch/default",
						},
					],
				},
				error: undefined,
			},
			sessions: {
				data: {
					sessions: [
						{
							id: "scratch-worker-1",
							projectId: "scratch",
							harness: "codex",
							status: "working",
							isTerminated: false,
							updatedAt: "2026-06-10T16:15:04Z",
						},
					],
				},
				error: undefined,
			},
		});

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));

		expect(result.current.data?.[0]).toMatchObject({
			id: "scratch",
			kind: "scratch",
		});
		expect(result.current.data?.[0].sessions[0]).toMatchObject({
			id: "scratch-worker-1",
			branch: undefined,
		});
	});

	it("falls back to the direct session read while the workspace list has not caught up", async () => {
		getMock.mockImplementation(async (url: string, options?: { params?: { path?: { sessionId?: string } } }) => {
			if (url === "/api/v1/projects") {
				return {
					data: {
						projects: [{ id: "proj-1", name: "workspace3", path: "/tmp/workspace3", orchestratorAgent: "codex" }],
					},
					error: undefined,
				};
			}
			if (url === "/api/v1/sessions") {
				return { data: { sessions: [] }, error: undefined };
			}
			if (url === "/api/v1/sessions/{sessionId}") {
				expect(options?.params?.path?.sessionId).toBe("sess-orch");
				return {
					data: {
						session: {
							id: "sess-orch",
							projectId: "proj-1",
							displayName: "orchestrate",
							harness: "codex",
							kind: "orchestrator",
							mode: "tui",
							status: "working",
							kanbanColumn: "building",
							displayStatus: "Working",
							autoInjectReview: true,
							autoInjectCI: true,
							autoReviewEnabled: false,
							isPinned: false,
							isTerminated: false,
							terminateOnPrMerge: false,
							prs: [],
							activity: { state: "idle", lastActivityAt: "2026-09-04T10:00:00Z" },
							createdAt: "2026-09-04T10:00:00Z",
							updatedAt: "2026-09-04T10:00:01Z",
						},
					},
					error: undefined,
				};
			}
			throw new Error(`unexpected GET ${url}`);
		});

		const queryClient = new QueryClient({ defaultOptions: { queries: { retryDelay: 0 } } });
		const localWrapper = ({ children }: { children: ReactNode }) => (
			<QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
		);

		const { result } = renderHook(() => useWorkspaceSession("sess-orch"), { wrapper: localWrapper });

		await waitFor(() => expect(result.current.data?.id).toBe("sess-orch"));
		expect(result.current.data).toMatchObject({
			id: "sess-orch",
			workspaceId: "proj-1",
			workspaceName: "workspace3",
			title: "orchestrate",
			provider: "codex",
			kind: "orchestrator",
		});
		await waitFor(() => {
			const cached = queryClient.getQueryData<WorkspaceSummary[]>(workspaceQueryKey);
			expect(Array.isArray(cached)).toBe(true);
			expect(cached?.[0]?.sessions.some((session: { id: string }) => session.id === "sess-orch")).toBe(true);
		});
	});

	it("groups projectless sessions in Scratchpad after projects", async () => {
		respondWith({
			projects: { data: { projects: [{ id: "proj-1", name: "my-app", path: "/p" }] }, error: undefined },
			sessions: {
				data: {
					sessions: [
						{
							id: "standalone-1",
							displayName: "Research",
							provisionState: "failed",
							provisionError: "Agent failed to start",
							provisionSteps: [{ id: "agent", status: "running", startedAt: "2026-06-10T16:15:00Z" }],
							harness: "codex",
							status: "working",
							isTerminated: false,
							updatedAt: "2026-06-10T16:15:04Z",
						},
					],
				},
				error: undefined,
			},
		});

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));

		expect(result.current.data?.map((workspace) => workspace.id)).toEqual(["proj-1", "__standalone__"]);
		expect(result.current.data?.[1]).toMatchObject({
			id: "__standalone__",
			name: "Scratchpad",
			kind: "standalone",
		});
		expect(result.current.data?.[1].sessions[0]).toMatchObject({
			id: "standalone-1",
			workspaceId: "__standalone__",
			workspaceName: "Scratchpad",
			title: "Research",
			branch: undefined,
			provisionState: "failed",
			provisionError: "Agent failed to start",
			provisionSteps: [{ id: "agent", status: "running", startedAt: "2026-06-10T16:15:00Z" }],
		});
	});

	it("localizes the standalone workspace name", async () => {
		await appI18n.changeLanguage("zh-CN");
		respondWith({
			sessions: {
				data: {
					sessions: [
						{
							id: "standalone-1",
							harness: "codex",
							status: "working",
							isTerminated: false,
							updatedAt: "2026-06-10T16:15:04Z",
						},
					],
				},
				error: undefined,
			},
		});

		try {
			const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
			await waitFor(() => expect(result.current.isSuccess).toBe(true));

			expect(result.current.data?.[0]).toMatchObject({ name: "草稿区" });
			expect(result.current.data?.[0].sessions[0]).toMatchObject({ workspaceName: "草稿区" });
		} finally {
			await appI18n.changeLanguage("en");
		}
	});

	it("maps each session's prs straight from the session list", async () => {
		respondWith({
			projects: { data: { projects: [{ id: "proj-1", name: "my-app", path: "/p" }] }, error: undefined },
			sessions: {
				data: {
					sessions: [
						{
							id: "sess-1",
							projectId: "proj-1",
							status: "pr_open",
							isTerminated: false,
							updatedAt: "2026-06-10T16:15:04Z",
							prs: [
								{
									number: 278,
									state: "open",
									url: "u",
									ci: "passing",
									review: "approved",
									mergeability: "clean",
									reviewComments: false,
									updatedAt: "2026-06-10T16:15:04Z",
								},
							],
						},
						{
							id: "sess-2",
							projectId: "proj-1",
							status: "working",
							isTerminated: false,
							updatedAt: "2026-06-10T16:15:04Z",
						},
					],
				},
				error: undefined,
			},
		});

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));

		const sessions = result.current.data?.[0].sessions ?? [];
		expect(sessions[0].prs).toEqual([
			{
				number: 278,
				state: "open",
				url: "u",
				ci: "passing",
				review: "approved",
				mergeability: "clean",
				reviewComments: false,
				updatedAt: "2026-06-10T16:15:04Z",
			},
		]);
		// A session with no PRs maps to an empty stack, so the empty states render.
		expect(sessions[1].prs).toEqual([]);
	});

	it("preserves backend merged status for terminated merged sessions", async () => {
		respondWith({
			projects: { data: { projects: [{ id: "proj-1", name: "my-app", path: "/p" }] }, error: undefined },
			sessions: {
				data: {
					sessions: [
						{
							id: "sess-1",
							projectId: "proj-1",
							status: "merged",
							isTerminated: true,
							updatedAt: "2026-06-10T16:15:04Z",
						},
					],
				},
				error: undefined,
			},
		});

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));

		expect(result.current.data?.[0].sessions[0].status).toBe("merged");
		expect(result.current.data?.[0].sessions[0].isTerminated).toBe(true);
	});

	it("falls back to terminated for terminated sessions without a known backend status", async () => {
		respondWith({
			projects: { data: { projects: [{ id: "proj-1", name: "my-app", path: "/p" }] }, error: undefined },
			sessions: {
				data: {
					sessions: [
						{
							id: "sess-1",
							projectId: "proj-1",
							status: "bogus",
							isTerminated: true,
							updatedAt: "2026-06-10T16:15:04Z",
						},
					],
				},
				error: undefined,
			},
		});

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));

		expect(result.current.data?.[0].sessions[0].status).toBe("terminated");
		expect(result.current.data?.[0].sessions[0].isTerminated).toBe(true);
	});

	it("surfaces a projects fetch error", async () => {
		const failure = new TypeError("Failed to fetch");
		respondWith({ projects: { data: undefined, error: failure } });

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });

		await waitFor(() => expect(result.current.isError).toBe(true), { timeout: 3_000 });
		expect(result.current.error).toBe(failure);
	});

	it("surfaces a sessions fetch error even when projects load", async () => {
		const failure = new Error("sessions backend down");
		respondWith({
			projects: { data: { projects: [{ id: "proj-1", name: "my-app", path: "/p" }] }, error: undefined },
			sessions: { data: undefined, error: failure },
		});

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });

		await waitFor(() => expect(result.current.isError).toBe(true), { timeout: 3_000 });
		expect(result.current.error).toBe(failure);
	});





	it("selects only attention-worthy worker sessions for the always-mounted tray", async () => {
		respondWith({
			projects: { data: { projects: [{ id: "proj-1", name: "my-app", path: "/p" }] }, error: undefined },
			sessions: {
				data: {
					sessions: [
						{ id: "needs-input", projectId: "proj-1", displayName: "Needs input", harness: "codex", status: "needs_input", updatedAt: "2026-08-01T00:00:00Z" },
						{ id: "mergeable", projectId: "proj-1", displayName: "Mergeable", harness: "codex", status: "mergeable", updatedAt: "2026-08-01T00:00:00Z" },
						{ id: "working", projectId: "proj-1", displayName: "Working", harness: "codex", status: "working", updatedAt: "2026-08-01T00:00:00Z" },
						{ id: "merged", projectId: "proj-1", displayName: "Merged", harness: "codex", status: "merged", updatedAt: "2026-08-01T00:00:00Z" },
						{ id: "orchestrator", projectId: "proj-1", displayName: "Orchestrator", harness: "codex", kind: "orchestrator", status: "needs_input", updatedAt: "2026-08-01T00:00:00Z" },
					],
				},
				error: undefined,
			},
		});

		const { result } = renderHook(() => useWorkspaceTraySessions(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(result.current.data).toEqual([
			{ projectId: "proj-1", projectName: "my-app", sessionId: "needs-input", title: "Needs input", zone: "action" },
			{ projectId: "proj-1", projectName: "my-app", sessionId: "mergeable", title: "Mergeable", zone: "merge" },
		]);
	});
});

describe("selectTraySessions", () => {
	it("keeps a worker waiting on the live orchestrator out of the tray, but not a human-addressed wait", () => {
		const base = { workspaceId: "proj-1", status: "needs_input" } as const;
		const entries = selectTraySessions([
			{
				id: "proj-1",
				name: "my-app",
				path: "/repos/my-app",
				type: "main",
				sessions: [
					{ ...base, id: "to-orch", title: "to orch", needsInputAudience: "orchestrator" },
					{ ...base, id: "to-human", title: "to human", needsInputAudience: "human" },
				],
			} as unknown as WorkspaceSummary,
		]);
		expect(entries.map((entry) => entry.sessionId)).toEqual(["to-human"]);
	});
});

describe("useWorkspaceScope board presentation", () => {


	it.each([
		{ kind: "orchestrator", isTerminated: false, expected: false },
		{ kind: "orchestrator", isTerminated: true, expected: false },
		{ kind: "worker", isTerminated: false, expected: true },
		{ kind: "worker", isTerminated: true, expected: true },
		{ kind: undefined, isTerminated: false, expected: true },
	])("projects worker presence for $kind, terminated=$isTerminated", async ({ kind, isTerminated, expected }) => {
		respondWith({
			projects: { data: { projects: [{ id: "p", name: "project", path: "/repo" }] } },
			sessions: { data: { sessions: [{ id: "s", projectId: "p", kind, isTerminated, status: "working" }] } },
		});
		const { result } = renderHook(() => useWorkspaceScope("p"), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(result.current.data?.hasWorkerSessions).toBe(expected);
		expect(result.current.data?.project).not.toHaveProperty("sessions");
	});

});

describe("useWorkspacePolling", () => {
	it("polls once per interval however many components subscribe", async () => {
		vi.useFakeTimers();
		try {
			respondWith({});
			const queryClient = new QueryClient({ defaultOptions: { queries: { retryDelay: 0 } } });
			const shared = ({ children }: { children: ReactNode }) => (
				<QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
			);
			renderHook(
				() => {
					useWorkspacePolling();
					useWorkspaceQuery();
					useWorkspaceQuery();
					useWorkspaceTraySessions();
				},
				{ wrapper: shared },
			);
			await vi.advanceTimersByTimeAsync(0);
			// One refresh is a workers request and an orchestrators request.
			const sessionFetches = () => getMock.mock.calls.filter(([url, init]) => url === "/api/v1/sessions" && init?.params?.query?.kind === "worker").length;
			const afterMount = sessionFetches();
			await vi.advanceTimersByTimeAsync(15_000);
			expect(sessionFetches()).toBe(afterMount + 1);
			await vi.advanceTimersByTimeAsync(15_000);
			expect(sessionFetches()).toBe(afterMount + 2);
		} finally {
			vi.useRealTimers();
		}
	});
});



describe("live-only workspace list", () => {
	const project = { id: "p", name: "project", path: "/repo" };
	const live = (id: string, extra: Record<string, unknown> = {}) => ({ id, projectId: "p", kind: "worker", status: "working", isTerminated: false, prs: [], ...extra });
	const artifact = { path: "out/report.md", name: "report.md", kind: "markdown", size: 3, updatedAt: "2026-10-01T00:00:00Z", rawUrl: "http://raw/report.md" };

	function mockDaemon(options: {
		workers?: unknown[];
		orchestrators?: unknown[];
		archived?: { total: number; byProject: Record<string, number> };
		details?: Record<string, unknown>;
		projects?: unknown[];
	}) {
		getMock.mockImplementation(async (url: string, init?: { params?: { path?: { sessionId?: string }; query?: Record<string, unknown> } }) => {
			if (url === "/api/v1/projects") return { data: { projects: options.projects ?? [project] } };
			if (url === "/api/v1/sessions") {
				const kind = init?.params?.query?.kind;
				return kind === "orchestrator"
					? { data: { sessions: options.orchestrators ?? [] } }
					: { data: { sessions: options.workers ?? [], archived: options.archived } };
			}
			if (url === "/api/v1/sessions/{sessionId}") {
				const detail = options.details?.[init?.params?.path?.sessionId ?? ""];
				return detail ? { data: { session: detail } } : { error: { code: "SESSION_NOT_FOUND" }, response: { status: 404 } };
			}
			throw new Error(`unexpected GET ${url}`);
		});
	}

	function newClient() {
		return new QueryClient({ defaultOptions: { queries: { retryDelay: 0 } } });
	}

	function wrapperFor(client: QueryClient) {
		return ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
	}

	it("asks the daemon for live workers and live orchestrators only", async () => {
		mockDaemon({ workers: [live("w")], orchestrators: [live("o", { kind: "orchestrator" })] });
		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		const queries = getMock.mock.calls.filter(([url]) => url === "/api/v1/sessions").map(([, init]) => init.params.query);
		expect(queries).toEqual([{ active: true, kind: "worker" }, { active: true, kind: "orchestrator" }]);
		expect(result.current.data?.[0]?.sessions.map((session) => session.id)).toEqual(["w", "o"]);
	});

	it("sizes each project's archive without loading it, and keeps a standalone archive reachable", async () => {
		mockDaemon({ workers: [live("w")], archived: { total: 5, byProject: { p: 3, "": 2 } } });
		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		const [projectWorkspace, standalone] = result.current.data ?? [];
		expect(projectWorkspace.archivedCount).toBe(3);
		expect(projectWorkspace.sessions).toHaveLength(1);
		// No live standalone session, yet the archive of two still needs its workspace.
		expect(standalone).toMatchObject({ id: "__standalone__", archivedCount: 2, sessions: [] });
	});

	it("lists a live session's artifact files from its own read, once per interval", async () => {
		mockDaemon({
			workers: [live("w", { hasArtifacts: true, outputType: "artifact" }), live("done", { hasArtifacts: true, isTerminated: true }), live("plain")],
			details: { w: live("w", { hasArtifacts: true, artifactFiles: [artifact] }) },
		});
		const client = newClient();
		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper: wrapperFor(client) });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		const sessions = result.current.data?.[0]?.sessions ?? [];
		expect(sessions.find((session) => session.id === "w")).toMatchObject({ hasArtifacts: true, artifactFiles: [expect.objectContaining({ path: "out/report.md" })] });
		expect(sessions.find((session) => session.id === "plain")?.artifactFiles).toBeUndefined();
		const detailReads = () => getMock.mock.calls.filter(([url]) => url === "/api/v1/sessions/{sessionId}").map(([, init]) => init.params.path.sessionId);
		// Only the live session with artifacts is read; the terminated one is not.
		expect(detailReads()).toEqual(["w"]);
		await act(async () => { await client.invalidateQueries({ queryKey: workspaceQueryKey }); });
		expect(detailReads()).toEqual(["w"]);
		// A listing dropped on purpose (a file written moments ago) is read again.
		forgetSessionArtifactFiles(client, "w");
		await act(async () => { await client.invalidateQueries({ queryKey: workspaceQueryKey }); });
		expect(detailReads()).toEqual(["w", "w"]);
	});

	it("keeps a session's cached listing when its read fails", async () => {
		mockDaemon({ workers: [live("w", { hasArtifacts: true })], details: {} });
		const client = newClient();
		client.setQueryData(sessionArtifactsQueryKey("w"), [artifact]);
		client.getQueryCache().find({ queryKey: sessionArtifactsQueryKey("w") })?.setState({ dataUpdatedAt: 1 });
		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper: wrapperFor(client) });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(result.current.data?.[0]?.sessions[0]?.artifactFiles).toEqual([expect.objectContaining({ path: "out/report.md" })]);
	});

	describe("refreshWorkspaceSession", () => {
		async function seeded(workers: unknown[], details: Record<string, unknown>, archived = { total: 4, byProject: { p: 4 } }) {
			mockDaemon({ workers, details, archived });
			const client = newClient();
			// Reading data in the hook subscribes to it; a patch lands after the first render.
			const { result } = renderHook(() => {
				const query = useWorkspaceQuery();
				void query.data;
				return query;
			}, { wrapper: wrapperFor(client) });
			await waitFor(() => expect(result.current.isSuccess).toBe(true));
			getMock.mockClear();
			return { client, result };
		}

		it("replaces one session without reading the list", async () => {
			const { client, result } = await seeded([live("a"), live("b")], { a: live("a", { status: "idle", displayName: "renamed" }) });
			expect(await refreshWorkspaceSession(client, "a")).toBe("patched");
			expect(getMock.mock.calls.map(([url]) => url)).toEqual(["/api/v1/sessions/{sessionId}"]);
			await waitFor(() => expect(result.current.data?.[0]?.sessions.find((session) => session.id === "a")?.title).toBe("renamed"));
			expect(result.current.data?.[0]?.sessions.map((session) => session.id)).toEqual(["a", "b"]);
		});

		it("writes patches read close together to the list once", async () => {
			const { client, result } = await seeded([live("a"), live("b")], {
				a: live("a", { displayName: "a renamed" }),
				b: live("b", { displayName: "b renamed" }),
			});
			const writes: unknown[] = [];
			const unsubscribe = client.getQueryCache().subscribe((event) => {
				if (event.type === "updated" && event.action.type === "success" && event.query.queryKey[0] === workspaceQueryKey[0]) writes.push(event.query.state.data);
			});
			const outcomes = await Promise.all([refreshWorkspaceSession(client, "a"), refreshWorkspaceSession(client, "b")]);
			unsubscribe();
			expect(outcomes).toEqual(["patched", "patched"]);
			expect(writes).toHaveLength(1);
			await waitFor(() => expect(result.current.data?.[0]?.sessions.map((session) => session.title)).toEqual(["a renamed", "b renamed"]));
		});

		it("moves a session that terminated into the archive count and asks for a list refetch", async () => {
			const { client, result } = await seeded([live("a"), live("b")], { a: live("a", { isTerminated: true, status: "terminated" }) });
			expect(await refreshWorkspaceSession(client, "a")).toBe("refetch");
			await waitFor(() => expect(result.current.data?.[0]?.sessions.map((session) => session.id)).toEqual(["b"]));
			expect(result.current.data?.[0]?.archivedCount).toBe(5);
		});

		it("adds a newly created session and asks for a list refetch", async () => {
			const { client, result } = await seeded([live("a")], { n: live("n") });
			expect(await refreshWorkspaceSession(client, "n")).toBe("refetch");
			await waitFor(() => expect(result.current.data?.[0]?.sessions.map((session) => session.id)).toEqual(["a", "n"]));
		});

		it("asks for a list refetch when the session's project is not cached", async () => {
			const { client, result } = await seeded([live("a")], { x: live("x", { projectId: "unknown" }) });
			expect(await refreshWorkspaceSession(client, "x")).toBe("refetch");
			expect(result.current.data?.[0]?.sessions.map((session) => session.id)).toEqual(["a"]);
		});

		it("drops a session the daemon no longer has", async () => {
			const { client, result } = await seeded([live("a"), live("b")], {});
			expect(await refreshWorkspaceSession(client, "a")).toBe("refetch");
			await waitFor(() => expect(result.current.data?.[0]?.sessions.map((session) => session.id)).toEqual(["b"]));
		});

		it("keeps the artifact listing the read returned for the next list refresh", async () => {
			const { client } = await seeded([live("a", { hasArtifacts: true })], { a: live("a", { hasArtifacts: true, artifactFiles: [artifact] }) });
			await refreshWorkspaceSession(client, "a");
			expect(client.getQueryData(sessionArtifactsQueryKey("a"))).toEqual([expect.objectContaining({ path: "out/report.md" })]);
		});
	});

	it("finds a terminated session by id and keeps it across list refetches", async () => {
		mockDaemon({ workers: [live("a")], details: { gone: live("gone", { isTerminated: true, status: "terminated" }) } });
		const client = newClient();
		const { result } = renderHook(() => ({ session: useWorkspaceSession("gone"), list: useWorkspaceQuery() }), { wrapper: wrapperFor(client) });
		await waitFor(() => expect(result.current.session.data?.id).toBe("gone"));
		await waitFor(() => expect(result.current.list.data?.[0]?.sessions.some((session) => session.id === "gone")).toBe(true));
		await act(async () => { await client.invalidateQueries({ queryKey: workspaceQueryKey }); });
		expect(result.current.list.data?.[0]?.sessions.map((session) => session.id)).toEqual(["a", "gone"]);
	});
});
