import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

vi.mock("../../lib/backlog-api", async (orig) => ({
	...(await orig<typeof import("../../lib/backlog-api")>()),
	listBacklog: async () => [{ ref: "B7", title: "Mobile app" }],
}));
import { useProjectSessions } from "./useProjectSessions";
import type { WorkspaceSummary } from "../../types/workspace";

const base = { status: "working", isTerminated: false } as const;

describe("useProjectSessions lane orchestrators", () => {
	it("lists live lane orchestrators first, titled by the lane item, and hides the default orchestrator", async () => {
		const workspace = {
			id: "proj",
			sessions: [
				{ ...base, id: "w1", title: "Worker", kind: "worker" },
				{ ...base, id: "o1", title: "Orchestrator", kind: "orchestrator" },
				{ ...base, id: "o2", title: "Orchestrator", kind: "orchestrator", lane: "B7" },
			],
		} as unknown as WorkspaceSummary;
		const ref = { current: null };
		const { result } = renderHook(() =>
			useProjectSessions({ workspace, selection: {} as never, projectKey: "proj", expanded: false, activeProjectMatches: false, projectHeaderRef: ref, projectSessionsRef: ref }),
			{ wrapper: ({ children }) => <QueryClientProvider client={new QueryClient()}>{children}</QueryClientProvider> },
		);
		await waitFor(() => expect(result.current.sessions.map((s) => [s.id, s.title])).toEqual([["o2", "Mobile app"], ["w1", "Worker"]]));
	});
});
