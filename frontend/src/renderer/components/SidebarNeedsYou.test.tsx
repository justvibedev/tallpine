import { renderSidebar, rerenderSidebar, mockParams, session, workspace, getMock } from "./Sidebar.test.setup";
import { screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";

const { fetchConversationPageMock } = vi.hoisted(() => ({ fetchConversationPageMock: vi.fn() }));
vi.mock("../hooks/conversation/useConversationQuery", async (importOriginal) => ({
	...(await importOriginal<typeof import("../hooks/conversation/useConversationQuery")>()),
	fetchConversationPage: fetchConversationPageMock,
}));

const orchestrator: WorkspaceSession = { ...session, id: "proj-1-orc", kind: "orchestrator", title: "Orchestrator" };
const msg = (sequence: number, role: "user" | "assistant", text: string) =>
	({ kind: "message", id: `m${sequence}`, sequence, revision: 0, role, origin: role === "user" ? "human" : "agent", text, streaming: false, createdAt: "2026-11-04T10:00:00Z" });

describe("sidebar needs-you icon", () => {
	beforeEach(() => {
		fetchConversationPageMock.mockReset();
		getMock.mockReset();
	});

	it("shows on the project row while the orchestrator's marked reply is unanswered", async () => {
		fetchConversationPageMock.mockResolvedValue({ items: [msg(1, "user", "go"), msg(2, "assistant", "**Needs you:** merge it")] });
		renderSidebar({ workspaces: [{ ...workspace, sessions: [orchestrator] }] });
		expect(await screen.findByText("Orchestrator is waiting for your reply")).toBeInTheDocument();
	});

	it("is absent once the owner has replied", async () => {
		fetchConversationPageMock.mockResolvedValue({ items: [msg(1, "assistant", "**Needs you:** merge it"), msg(2, "user", "merged")] });
		renderSidebar({ workspaces: [{ ...workspace, sessions: [orchestrator] }] });
		await waitFor(() => expect(fetchConversationPageMock).toHaveBeenCalled());
		expect(screen.queryByText("Orchestrator is waiting for your reply")).not.toBeInTheDocument();
	});

	it("hides once that orchestrator's chat is open after the icon was shown", async () => {
		fetchConversationPageMock.mockResolvedValue({ items: [msg(1, "assistant", "**Needs you:** merge it")] });
		const workspaces = [{ ...workspace, sessions: [orchestrator] }];
		renderSidebar({ workspaces });
		expect(await screen.findByText("Orchestrator is waiting for your reply")).toBeInTheDocument();
		mockParams.projectId = "proj-1";
		mockParams.sessionId = orchestrator.id;
		rerenderSidebar();
		await waitFor(() => expect(screen.queryByText("Orchestrator is waiting for your reply")).not.toBeInTheDocument());
	});
});
