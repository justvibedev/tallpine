import { TooltipProvider } from "@/components/ui/tooltip";
import { SidebarProvider } from "@/components/ui/sidebar";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../../stores/ui-store";
import { STANDALONE_WORKSPACE_ID } from "../../types/workspace";
import { SessionsPanelHeader } from "./SessionsPanelHeader";
import type { Selection } from "./types";

const selectionFor = (over: Partial<Selection> = {}) => ({ activeProjectId: undefined, activeRemoteHostId: undefined, activeRemoteProjectId: undefined, ...over }) as Selection;

function renderHeader(selection: Selection, toggle = vi.fn(), settingsOpen = false) {
	render(
		<TooltipProvider>
			<SidebarProvider>
				<SessionsPanelHeader selection={selection} settingsOpen={settingsOpen} toggleSidebar={toggle} />
			</SidebarProvider>
		</TooltipProvider>,
	);
	return toggle;
}

const requestNewTask = vi.fn();
const requestCreateProject = vi.fn();

beforeEach(() => {
	requestNewTask.mockReset();
	requestCreateProject.mockReset();
	useUiStore.setState({ requestNewTask, requestCreateProject });
});

describe("SessionsPanelHeader", () => {
	it("leads with the panel toggle (pressed while open), then the title", async () => {
		const toggle = renderHeader(selectionFor());
		const button = screen.getByRole("button", { name: "Toggle sessions panel" });
		expect(button).toHaveAttribute("aria-pressed", "true");
		expect(screen.getByRole("heading", { name: "Sessions" })).toBeInTheDocument();
		await userEvent.setup().click(button);
		expect(toggle).toHaveBeenCalledTimes(1);
	});

	it("on the settings layer keeps only the panel toggle: no Sessions title, no + New", async () => {
		const toggle = renderHeader(selectionFor(), vi.fn(), true);
		expect(screen.queryByRole("heading", { name: "Sessions" })).toBeNull();
		expect(screen.queryByTestId("sessions-panel-new")).toBeNull();
		expect(screen.queryByRole("button", { name: "More ways to start" })).toBeNull();
		await userEvent.setup().click(screen.getByRole("button", { name: "Toggle sessions panel" }));
		expect(toggle).toHaveBeenCalledTimes(1);
	});

	it("+ New starts a session in the project in scope", async () => {
		renderHeader(selectionFor({ activeProjectId: "proj-7" }));
		await userEvent.setup().click(screen.getByTestId("sessions-panel-new-button"));
		expect(requestNewTask).toHaveBeenCalledWith("proj-7");
	});

	it("+ New falls back to a standalone agent with no project in scope", async () => {
		renderHeader(selectionFor());
		await userEvent.setup().click(screen.getByTestId("sessions-panel-new-button"));
		expect(requestNewTask).toHaveBeenCalledWith(STANDALONE_WORKSPACE_ID);
	});

	it("+ New keeps the remote host of the scoped project", async () => {
		renderHeader(selectionFor({ activeRemoteHostId: "host-1", activeRemoteProjectId: "proj-r" }));
		await userEvent.setup().click(screen.getByTestId("sessions-panel-new-button"));
		expect(requestNewTask).toHaveBeenCalledWith("proj-r", "host-1");
	});

	it("the caret offers a standalone agent and a new project", async () => {
		const user = userEvent.setup();
		renderHeader(selectionFor({ activeProjectId: "proj-7" }));
		await user.click(screen.getByRole("button", { name: "More ways to start" }));
		await user.click(await screen.findByRole("menuitem", { name: "New standalone agent" }));
		expect(requestNewTask).toHaveBeenCalledWith(STANDALONE_WORKSPACE_ID);
		await user.click(screen.getByRole("button", { name: "More ways to start" }));
		await user.click(await screen.findByRole("menuitem", { name: "New project…" }));
		expect(requestCreateProject).toHaveBeenCalledTimes(1);
	});
});
