import { h, Wrap } from "./TaskComposer.test.setup";
import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TaskComposer } from "./TaskComposer";
import { agentReadiness } from "../test/agent-readiness-fixtures";
import { clearCachedAgentModels } from "../lib/agent-model-cache";
import { useUiStore } from "../stores/ui-store";

describe("TaskComposer expired login on a remote host", () => {
	beforeEach(() => {
		useUiStore.setState({ remoteHosts: true });
	});

	afterEach(() => {
		useUiStore.setState({ remoteHosts: false });
		h.get.mockReset();
		h.remoteGet.mockReset();
		h.remotePost.mockReset();
		h.remotePost.mockResolvedValue({ data: {} });
		h.agentCatalog = undefined;
		clearCachedAgentModels("claude-code");
	});

	// A remote host may run an older daemon that still refuses the spawn for a
	// stale catalog, so it keeps the notice and its Log in shortcut.
	it("keeps the expired-login notice for a remote host", async () => {
		const claude = agentReadiness("claude-code", "Claude Code", { authentication: "configured" });
		h.agentCatalog = { agents: [claude] };
		h.remoteGet.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return { data: { agent: "claude-code", selectionMode: "catalog", models: [{ id: "sonnet", label: "Sonnet 5.5" }], allowCustom: true, stale: true, warning: "the saved Claude Code login token expired", warningCode: "auth_expired" } };
			}
			if (path === "/api/v1/settings") return { data: { defaultSessionMode: "chat", chatHarnesses: [] } };
			return { data: { status: "ok", project: { id: "project-a", config: { worker: { agent: "claude-code" } } } } };
		});
		h.remotePost.mockImplementation(async (path: string) => path === "/api/v1/agents/readiness/ensure"
			? { data: { agents: [claude] } }
			: { data: {} });
		render(<Wrap><TaskComposer hostId="box-a" projectId="project-a" onCreated={vi.fn()} /></Wrap>);
		const notice = await screen.findByTestId("model-catalog-notice");
		expect(notice).toHaveTextContent("Claude Code's saved login has expired.");
		expect(screen.getByRole("button", { name: "Log in again" })).toBeInTheDocument();
	});
});
