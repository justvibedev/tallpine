import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import { ContextMenu, ContextMenuTrigger } from "../ui/context-menu";
import { useUiStore } from "../../stores/ui-store";
import { ProjectContextMenuContent } from "./ProjectContextMenuContent";
import type { WorkspaceSummary } from "../../types/workspace";

function open(isStandalone: boolean) {
	render(
		<ContextMenu>
			<ContextMenuTrigger>target</ContextMenuTrigger>
			<ProjectContextMenuContent workspace={{ id: "proj" } as WorkspaceSummary} selection={{} as never} isStandalone={isStandalone} isProjectRestarting={false} isRemoving={false} removeProject={() => {}} />
		</ContextMenu>,
	);
	return userEvent.pointer({ keys: "[MouseRight]", target: screen.getByText("target") });
}

describe("ProjectContextMenuContent lane entry", () => {
	beforeEach(() => useUiStore.setState({ laneOrchestratorRequest: null }));

	it("opens the lane dialog for the project", async () => {
		await open(false);
		await userEvent.click(await screen.findByRole("menuitem", { name: "New lane…" }));
		expect(useUiStore.getState().laneOrchestratorRequest?.projectId).toBe("proj");
	});

	it("is not offered for the standalone workspace", async () => {
		await open(true);
		await screen.findByRole("menuitem", { name: "New task" });
		expect(screen.queryByRole("menuitem", { name: "New lane…" })).toBeNull();
	});
});
