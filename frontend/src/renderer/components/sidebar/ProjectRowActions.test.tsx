import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import { useUiStore } from "../../stores/ui-store";
import { TooltipProvider } from "../ui/tooltip";
import { ProjectRowActions } from "./ProjectRowActions";
import type { WorkspaceSummary } from "../../types/workspace";

function renderActions(isStandalone: boolean) {
	render(
		<TooltipProvider>
			<ProjectRowActions
				workspace={{ id: "proj", name: "proj" } as WorkspaceSummary}
				isStandalone={isStandalone}
				nameWithHost="proj"
				selection={{} as never}
				orchestrator={undefined}
				orchestratorActive={false}
				isSpawning={false}
				isProjectProvisioning={false}
				isProjectRestarting={false}
				isRemoving={false}
				projectDragInProgress={false}
				openOrchestrator={async () => {}}
				removeProject={() => {}}
			/>
		</TooltipProvider>,
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
