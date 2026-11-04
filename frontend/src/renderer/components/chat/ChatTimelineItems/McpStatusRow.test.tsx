import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ActivityRow, ChatPromptSender } from "../ChatTimelineItems";
import type { ConversationActivity } from "../../../types/conversation";

function notice(detail: NonNullable<ConversationActivity["detail"]>): ConversationActivity {
	return {
		kind: "activity", id: "mcp-1", sequence: 1, revision: 1, createdAt: "2026-10-30T00:00:00Z",
		activityKind: "system", status: "failed", summary: "MCP server github failed to start",
		detail: { event: "mcp.status", server: "github", status: "failed", ...detail },
	};
}

describe("McpStatusRow", () => {
	it("names the server and error, and says what to do", () => {
		render(<ActivityRow activity={notice({ text: "spawn npx ENOENT" })} />);
		expect(screen.getByText("MCP server github failed to start")).toBeInTheDocument();
		expect(screen.getByText("spawn npx ENOENT")).toBeInTheDocument();
		expect(screen.getByText(/reconnect it/)).toBeInTheDocument();
		// No sender, no actions.
		expect(screen.queryByRole("button")).toBeNull();
	});

	it("reconnects through the agent's /mcp command", async () => {
		const send = vi.fn().mockResolvedValue(undefined);
		render(<ChatPromptSender.Provider value={send}><ActivityRow activity={notice({})} /></ChatPromptSender.Provider>);
		fireEvent.click(screen.getByRole("button", { name: "Reconnect" }));
		expect(send).toHaveBeenCalledWith("/mcp reconnect github");
		expect(screen.getByRole("button", { name: "Reconnect" })).toBeDisabled();
		// Enabled again once the send resolves, so a failed reconnect can be retried.
		await vi.waitFor(() => expect(screen.getByRole("button", { name: "Reconnect" })).toBeEnabled());
	});

	it("shows status with plain /mcp, and re-enables the buttons if the send fails", async () => {
		const send = vi.fn().mockRejectedValue(new Error("offline"));
		render(<ChatPromptSender.Provider value={send}><ActivityRow activity={notice({})} /></ChatPromptSender.Provider>);
		fireEvent.click(screen.getByRole("button", { name: "Show status" }));
		expect(send).toHaveBeenCalledWith("/mcp");
		await screen.findByRole("button", { name: "Show status" });
		await vi.waitFor(() => expect(screen.getByRole("button", { name: "Show status" })).toBeEnabled());
	});

	it("offers no reconnect for a server that needs sign-in", () => {
		render(<ChatPromptSender.Provider value={vi.fn()}><ActivityRow activity={notice({ status: "needs-auth" })} /></ChatPromptSender.Provider>);
		expect(screen.queryByRole("button", { name: "Reconnect" })).toBeNull();
		expect(screen.getByRole("button", { name: "Show status" })).toBeInTheDocument();
	});
});
