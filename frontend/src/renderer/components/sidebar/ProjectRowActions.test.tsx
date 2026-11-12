import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { navigate, ask, handOff } = vi.hoisted(() => ({ navigate: vi.fn(), ask: vi.fn(), handOff: vi.fn() }));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => navigate }));
vi.mock("../../lib/handoff-orchestrator", () => ({ askOrchestratorToHandOff: ask, handoffOrchestratorWithLatestNote: handOff }));

import { useUiStore } from "../../stores/ui-store";
import { TooltipProvider } from "../ui/tooltip";
import { ProjectRowActions } from "./ProjectRowActions";
import type { WorkspaceSession, WorkspaceSummary } from "../../types/workspace";

function renderActions(isStandalone: boolean, orchestrator?: WorkspaceSession) {
	render(
		<QueryClientProvider client={new QueryClient()}>
		<TooltipProvider>
			<ProjectRowActions
				workspace={{ id: "proj", name: "proj" } as WorkspaceSummary}
				isStandalone={isStandalone}
				nameWithHost="proj"
				selection={{} as never}
				orchestrator={orchestrator}
				orchestratorActive={false}
				isSpawning={false}
				isProjectProvisioning={false}
				isProjectRestarting={false}
				isRemoving={false}
				projectDragInProgress={false}
				openOrchestrator={async () => {}}
				removeProject={() => {}}
			/>
		</TooltipProvider>
		</QueryClientProvider>,
	);
}

describe("ProjectRowActions lane entry", () => {
	beforeEach(() => useUiStore.setState({ laneOrchestratorRequest: null }));

	it("opens the lane dialog for the project from the row menu", async () => {
		renderActions(false);
		await userEvent.click(screen.getByRole("button", { name: /Project actions/i }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "New lane…" }));
		expect(useUiStore.getState().laneOrchestratorRequest?.projectId).toBe("proj");
	});

	it("offers no row menu for the standalone workspace", () => {
		renderActions(true);
		expect(screen.queryByRole("button", { name: /Project actions/i })).toBeNull();
		expect(screen.queryByRole("menuitem", { name: "New lane…" })).toBeNull();
	});
});

// B673: an orchestrator whose agent stopped (its saved conversation may be gone
// for good) still occupies the project, so the row only opens it. The row menu
// offers Reset so it can be replaced without the orchestrator's help.
describe("ProjectRowActions orchestrator reset", () => {
	const orchestrator = (state: string) =>
		({ id: "proj-4", projectId: "proj", kind: "orchestrator", status: "idle", activity: { state } }) as unknown as WorkspaceSession;

	beforeEach(() => {
		navigate.mockReset();
		ask.mockReset();
		handOff.mockReset().mockResolvedValue({ kind: "handedOff", sessionId: "proj-5", projectId: "proj" });
	});

	it("resets a stopped orchestrator from the row menu", async () => {
		renderActions(false, orchestrator("exited"));
		await userEvent.click(screen.getByRole("button", { name: /Project actions/i }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Reset orchestrator" }));
		await userEvent.click(await screen.findByRole("button", { name: "Reset orchestrator" }));
		await vi.waitFor(() => expect(handOff).toHaveBeenCalledWith("proj-4", undefined));
		await vi.waitFor(() =>
			expect(navigate).toHaveBeenCalledWith(expect.objectContaining({ params: { projectId: "proj", sessionId: "proj-5" } })),
		);
		expect(ask).not.toHaveBeenCalled();
	});

	it("offers no reset for a running orchestrator or none at all", async () => {
		renderActions(false, orchestrator("active"));
		await userEvent.click(screen.getByRole("button", { name: /Project actions/i }));
		await screen.findByRole("menuitem", { name: "New lane…" });
		expect(screen.queryByRole("menuitem", { name: "Reset orchestrator" })).toBeNull();
	});
});
