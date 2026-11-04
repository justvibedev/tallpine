import userEvent from "@testing-library/user-event";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ProjectOrchestratorAction } from "../hooks/useProjectOrchestratorAction";
import { useUiStore } from "../stores/ui-store";
import { ProjectBoardActions } from "./ProjectBoardActions";
import { TooltipProvider } from "./ui/tooltip";

const actions: ProjectOrchestratorAction = {
	projectId: undefined,
	orchestrator: undefined,
	isSpawning: false,
	isProjectRestarting: false,
	isProvisioning: false,
	spawnError: "",
	canCreateAsTui: false,
	openNewTask: vi.fn(),
	openOrchestrator: vi.fn(),
};

describe("ProjectBoardActions", () => {

	it("preserves the existing empty local board controls", () => {
		render(
			<TooltipProvider>
				<ProjectBoardActions actions={actions} placement="header" quiet />
			</TooltipProvider>,
		);
		expect(screen.getByRole("button", { name: "New task" })).toHaveClass("topbar-control--secondary");
		expect(screen.getByRole("button", { name: /orchestrator/i })).toHaveClass("topbar-control--secondary");
	});

	it("shows a labelled + Lane button that opens the lane dialog, only when a project is given", async () => {
		useUiStore.setState({ laneOrchestratorRequest: null });
		const { rerender } = render(
			<TooltipProvider>
				<ProjectBoardActions actions={actions} placement="header" />
			</TooltipProvider>,
		);
		expect(screen.queryByRole("button", { name: "New lane…" })).toBeNull();
		rerender(
			<TooltipProvider>
				<ProjectBoardActions actions={actions} projectId="proj" placement="header" />
			</TooltipProvider>,
		);
		const button = screen.getByRole("button", { name: "New lane…" });
		expect(button).toHaveTextContent("Lane");
		await userEvent.click(button);
		expect(useUiStore.getState().laneOrchestratorRequest?.projectId).toBe("proj");
	});
});
