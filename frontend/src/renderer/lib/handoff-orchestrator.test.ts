import { beforeEach, describe, expect, it, vi } from "vitest";

const { post } = vi.hoisted(() => ({ post: vi.fn() }));

vi.mock("./host-clients", () => ({ clientForSessionHost: () => ({ POST: post }) }));
vi.mock("./api-client", () => ({
	apiErrorCode: (error: unknown) => (error as { code?: string } | undefined)?.code,
	apiErrorMessage: (error: unknown, fallback = "Request failed") => (error as { message?: string } | undefined)?.message ?? fallback,
}));

import { askOrchestratorToHandOff, handoffOrchestratorWithLatestNote, HANDOFF_REQUEST_MESSAGE } from "./handoff-orchestrator";

describe("orchestrator handoff client", () => {
	beforeEach(() => post.mockReset());

	it("hands off via the daemon route and returns the new orchestrator", async () => {
		post.mockResolvedValue({ data: { orchestrator: { id: "demo-2", projectId: "demo" } }, response: { status: 201 } });
		await expect(handoffOrchestratorWithLatestNote("demo-1")).resolves.toEqual({ kind: "handedOff", sessionId: "demo-2", projectId: "demo" });
		expect(post).toHaveBeenCalledWith("/api/v1/orchestrators/{id}/handoff", { params: { path: { id: "demo-1" } }, body: {} });
	});

	it("reports a missing state note instead of failing", async () => {
		post.mockResolvedValue({ error: { code: "HANDOFF_NOTE_REQUIRED", message: "no note" }, response: { status: 409 } });
		await expect(handoffOrchestratorWithLatestNote("demo-1")).resolves.toEqual({ kind: "noNote" });
	});

	it("throws other daemon errors", async () => {
		post.mockResolvedValue({ error: { code: "ORCHESTRATOR_AGENT_NOT_CONFIGURED", message: "no agent" }, response: { status: 409 } });
		await expect(handoffOrchestratorWithLatestNote("demo-1")).rejects.toThrow("no agent");
	});

	it("asks the orchestrator to write its note and hand off, as the user", async () => {
		post.mockResolvedValue({ response: { status: 200 } });
		await askOrchestratorToHandOff("demo-1");
		expect(post).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/send", {
			params: { path: { sessionId: "demo-1" } },
			body: { message: HANDOFF_REQUEST_MESSAGE, userAuthored: true },
		});
		expect(HANDOFF_REQUEST_MESSAGE).toContain("tallpine orchestrator handoff --note");
	});
});
