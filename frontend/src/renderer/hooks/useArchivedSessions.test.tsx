import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock, remoteGetMock } = vi.hoisted(() => ({ getMock: vi.fn(), remoteGetMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock },
	apiErrorCode: (error: unknown) => (error as { code?: string } | undefined)?.code,
	hasTrustedApiBaseUrl: () => true,
}));
vi.mock("../lib/host-clients", () => ({ clientForHost: () => ({ GET: remoteGetMock }), connectedHosts: () => [], subscribeConnectedHosts: () => () => undefined }));

import { ARCHIVE_PAGE_SIZE, archivedSessionsQueryKey, invalidateArchivedSessions, useArchivedSessions } from "./useArchivedSessions";

const dead = (id: string, projectId = "p") => ({ id, projectId, kind: "worker", status: "terminated", isTerminated: true, prs: [] });

function setup() {
	const client = new QueryClient({ defaultOptions: { queries: { retryDelay: 0 } } });
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
	return { client, wrapper };
}

beforeEach(() => {
	getMock.mockReset();
	remoteGetMock.mockReset();
});

describe("useArchivedSessions", () => {
	it("fetches nothing until enabled", async () => {
		const { wrapper } = setup();
		renderHook(() => useArchivedSessions({ projectId: "p" }, { enabled: false }), { wrapper });
		await new Promise((resolve) => setTimeout(resolve, 20));
		expect(getMock).not.toHaveBeenCalled();
	});

	it("pages one project's terminated workers with the daemon's cursor", async () => {
		getMock
			.mockResolvedValueOnce({ data: { sessions: [dead("a"), dead("b")], nextCursor: "c1" } })
			.mockResolvedValueOnce({ data: { sessions: [dead("b"), dead("c")] } });
		const { wrapper } = setup();
		const { result } = renderHook(() => useArchivedSessions({ projectId: "p" }), { wrapper });
		await waitFor(() => expect(result.current.sessions.map((session) => session.id)).toEqual(["a", "b"]));
		expect(result.current.hasNextPage).toBe(true);
		expect(getMock).toHaveBeenLastCalledWith("/api/v1/sessions", { params: { query: { active: false, kind: "worker", limit: ARCHIVE_PAGE_SIZE, project: "p" } } });

		await act(async () => { await result.current.fetchNextPage(); });
		// A session that moved between pages is listed once.
		await waitFor(() => expect(result.current.sessions.map((session) => session.id)).toEqual(["a", "b", "c"]));
		expect(result.current.hasNextPage).toBe(false);
		expect(getMock).toHaveBeenLastCalledWith("/api/v1/sessions", { params: { query: { active: false, kind: "worker", limit: ARCHIVE_PAGE_SIZE, cursor: "c1", project: "p" } } });
	});

	it("scopes the standalone archive server-side and names its sessions to the scratchpad", async () => {
		getMock.mockResolvedValue({ data: { sessions: [{ ...dead("solo"), projectId: "" }] } });
		const { wrapper } = setup();
		const { result } = renderHook(() => useArchivedSessions({ standalone: true }), { wrapper });
		await waitFor(() => expect(result.current.sessions).toHaveLength(1));
		expect(getMock).toHaveBeenCalledWith("/api/v1/sessions", { params: { query: { active: false, kind: "worker", limit: ARCHIVE_PAGE_SIZE, standalone: true } } });
		expect(result.current.sessions[0]).toMatchObject({ id: "solo", workspaceId: "__standalone__" });
	});

	it("asks every project at once when no scope is given", async () => {
		getMock.mockResolvedValue({ data: { sessions: [] } });
		const { wrapper } = setup();
		renderHook(() => useArchivedSessions({}), { wrapper });
		await waitFor(() => expect(getMock).toHaveBeenCalled());
		expect(getMock.mock.calls[0][1].params.query).toEqual({ active: false, kind: "worker", limit: ARCHIVE_PAGE_SIZE });
	});

	it("reads a remote host's archive through that host's client, tagging its sessions", async () => {
		remoteGetMock.mockResolvedValue({ data: { sessions: [dead("r")] } });
		const { wrapper } = setup();
		const { result } = renderHook(() => useArchivedSessions({ projectId: "p" }, { hostId: "box-a" }), { wrapper });
		await waitFor(() => expect(result.current.sessions).toHaveLength(1));
		expect(getMock).not.toHaveBeenCalled();
		expect(result.current.sessions[0]?.hostId).toBe("box-a");
	});

	it("refetches loaded pages when invalidated, e.g. after a session is archived", async () => {
		getMock.mockResolvedValueOnce({ data: { sessions: [dead("a")] } }).mockResolvedValueOnce({ data: { sessions: [dead("new"), dead("a")] } });
		const { client, wrapper } = setup();
		const { result } = renderHook(() => useArchivedSessions({ projectId: "p" }), { wrapper });
		await waitFor(() => expect(result.current.sessions).toHaveLength(1));
		await act(async () => { await invalidateArchivedSessions(client); });
		await waitFor(() => expect(result.current.sessions.map((session) => session.id)).toEqual(["new", "a"]));
		expect(client.getQueryState(archivedSessionsQueryKey({ projectId: "p" }))?.isInvalidated).toBe(false);
	});

	it("surfaces a failed page without throwing", async () => {
		getMock.mockResolvedValue({ error: { code: "BOOM" } });
		const { wrapper } = setup();
		const { result } = renderHook(() => useArchivedSessions({ projectId: "p" }), { wrapper });
		await waitFor(() => expect(result.current.isError).toBe(true));
		expect(result.current.sessions).toEqual([]);
	});
});
