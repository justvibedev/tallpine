// The setup module registers vi.mock calls, so it must be imported before ChatWorkspace.
import { cleanupChatWorkspaceTest, resetChatWorkspaceMocks } from "./ChatWorkspace.test.setup";
import { screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ChatWorkspace } from "./ChatWorkspace";
import { renderChat as render } from "../../test/render-chat";
import { chatFixtureEmpty } from "../../lib/chat-fixture";
import { chatSession } from "./ChatWorkspaceTestHelpers";
import type { OwnerAsk } from "../../lib/ask-api";

const api = vi.hoisted(() => ({ listProjectAsks: vi.fn() }));
vi.mock("../../lib/ask-api", async (importActual) => ({ ...(await importActual<typeof import("../../lib/ask-api")>()), ...api }));

const ask: OwnerAsk = {
	id: "a1", projectId: "p1", ref: "Q1", text: "Ship it?", state: "open", anchored: false, recommended: "y",
	options: [{ id: "y", label: "Yes" }], createdAt: "2026-11-13T10:00:00Z", updatedAt: "2026-11-13T10:00:00Z",
};

function renderEmptyOrchestrator() {
	return render(<ChatWorkspace snapshot={{ ...chatFixtureEmpty, controller: { state: "stopped" } }} sessionRole="orchestrator" session={{ ...chatSession, kind: "orchestrator", workspaceId: "p1", provisionState: "failed" }} />);
}

beforeEach(() => resetChatWorkspaceMocks());
afterEach(() => { cleanupChatWorkspaceTest(); api.listProjectAsks.mockReset(); });

describe("empty orchestrator chat with an open ask", () => {
	it("shows the welcome hero without asks", async () => {
		api.listProjectAsks.mockResolvedValue([]);
		renderEmptyOrchestrator();
		expect(await screen.findByRole("heading", { name: "What do you want to work on?" })).toBeInTheDocument();
	});

	it("hides the hero and suggestions while the strip has open asks", async () => {
		api.listProjectAsks.mockResolvedValue([ask]);
		renderEmptyOrchestrator();
		expect(await screen.findByTestId("ask-strip")).toBeInTheDocument();
		expect(screen.queryByRole("heading", { name: "What do you want to work on?" })).not.toBeInTheDocument();
	});
});
