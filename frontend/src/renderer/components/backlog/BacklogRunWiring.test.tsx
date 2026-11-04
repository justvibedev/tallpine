import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { backlogItem as item } from "../../test/backlog-fixtures";
import type { WorkspaceSession } from "../../types/workspace";
import { backlogApi, renderBacklog, resetBacklogApi, workspaceData } from "../../test/backlog-harness";
import { backlogRun, resetRunApi, runApi, runItem, runSwitch } from "../../test/backlog-run-fixtures";
import { BacklogView, type BacklogViewProps } from "./BacklogView";

vi.mock("../../lib/backlog-api", async (importActual) => ({ ...(await importActual<typeof import("../../lib/backlog-api")>()), ...(await import("../../test/backlog-harness")).backlogApi }));
vi.mock("../../lib/backlog-run-api", async (importActual) => ({ ...(await importActual<typeof import("../../lib/backlog-run-api")>()), ...(await import("../../test/backlog-run-fixtures")).runApi }));
vi.mock("../../hooks/useWorkspaceQuery", async (importActual) => {
	const harness = await import("../../test/backlog-harness");
	return { ...(await importActual<typeof import("../../hooks/useWorkspaceQuery")>()), useWorkspaceQuery: () => ({ data: harness.workspaceData.current }) };
});
vi.mock("../../hooks/useSettings", async () => {
	const fixtures = await import("../../test/backlog-run-fixtures");
	return { useSettings: () => ({ settings: { backlogRunEnabled: fixtures.runSwitch.enabled } }) };
});

const noop = () => undefined;
function view(overrides: Partial<BacklogViewProps> = {}) {
	return renderBacklog(<BacklogView projectId="proj-1" itemId={undefined} onProjectChange={noop} onItemChange={noop} onOpenSession={noop} {...overrides} />);
}
const openRun = () => backlogRun({ items: [runItem({ title: "Planned thing", status: "active", displayStatus: "active" })], counts: { active: 1 } });

const orchestrator: WorkspaceSession = {
	id: "proj-1-orch",
	workspaceId: "proj-1",
	workspaceName: "Project One",
	title: "Orchestrator",
	provider: "claude-code",
	status: "working",
	updatedAt: "2026-10-28T10:00:00Z",
	createdAt: "2026-10-01T00:00:00Z",
	kind: "orchestrator",
	prs: [],
};

beforeEach(() => {
	workspaceData.current = [{ id: "proj-1", name: "Project One", path: "/p1", sessions: [orchestrator] }, { id: "proj-2", name: "Project Two", path: "/p2", sessions: [] }];
	resetBacklogApi([item({ id: "par", ref: "B1", title: "Big feature", childCount: 2 }), item({ id: "leaf", ref: "B2", title: "Small thing" })]);
	resetRunApi(openRun());
});

describe("run mode on the Backlog page", () => {
	it("puts the Run button in the toolbar and the run card above the list for a project", async () => {
		resetRunApi(undefined);
		view();
		expect(await screen.findByRole("button", { name: "Run" })).toBeInTheDocument();
		resetRunApi(openRun());
		view();
		expect(await screen.findByRole("region", { name: "Run R1" })).toBeInTheDocument();
		expect(screen.getByText("Planned thing")).toBeInTheDocument();
	});

	it("shows neither across all projects, where there is no single orchestrator to run", async () => {
		view({ projectId: undefined });
		await screen.findByRole("region", { name: "Open" });
		expect(screen.queryByRole("button", { name: "Run" })).not.toBeInTheDocument();
		expect(screen.queryByRole("region", { name: /^Run R/ })).not.toBeInTheDocument();
	});

	it("shows nothing and asks for nothing when run mode is off", async () => {
		runSwitch.enabled = false;
		view({ itemId: "par" });
		await screen.findByRole("article");
		expect(screen.queryByRole("button", { name: "Run" })).not.toBeInTheDocument();
		expect(screen.queryByRole("region", { name: /^Run R/ })).not.toBeInTheDocument();
		await new Promise((resolve) => setTimeout(resolve, 30));
		expect(runApi.getLatestBacklogRun).not.toHaveBeenCalled();
		expect(runApi.getBacklogItemUsage).not.toHaveBeenCalled();
	});

	it("offers Run on a parent's detail but not on a leaf", async () => {
		resetRunApi(undefined);
		const { unmount } = view({ projectId: "proj-1", itemId: "par" });
		const article = await screen.findByRole("article", { name: /B1/ });
		expect(article).toBeInTheDocument();
		await waitFor(() => expect(screen.getAllByRole("button", { name: "Run" })).toHaveLength(2)); // toolbar + parent detail
		unmount();
		view({ projectId: "proj-1", itemId: "leaf" });
		await screen.findByRole("article", { name: /B2/ });
		expect(screen.getAllByRole("button", { name: "Run" })).toHaveLength(1); // toolbar only
	});

	it("starts a run for a parent's children from its detail", async () => {
		resetRunApi(undefined);
		view({ projectId: "proj-1", itemId: "par" });
		await screen.findByRole("article", { name: /B1/ });
		await waitFor(() => expect(screen.getAllByRole("button", { name: "Run" })).toHaveLength(2));
		await userEvent.click(within(screen.getByRole("article")).getByRole("button", { name: "Run" }));
		await screen.findByRole("dialog", { name: /Run B1 and its children/ });
		await userEvent.click(screen.getByRole("button", { name: "Start run" }));
		await waitFor(() => expect(runApi.startBacklogRun).toHaveBeenCalledWith("proj-1", "pr", { kind: "parent", ref: "B1" }));
		expect(runApi.startBacklogRun).toHaveBeenCalledTimes(1);
	});

	it("shows an item's cost and elapsed time on its detail", async () => {
		runApi.getBacklogItemUsage.mockResolvedValue({ costNanos: 2_000_000_000, costComplete: true, elapsedSeconds: 5400 });
		view({ itemId: "leaf" });
		expect(await screen.findByText("Cost $2.00")).toBeInTheDocument();
		expect(screen.getByText("Elapsed 1h 30m")).toBeInTheDocument();
		expect(runApi.getBacklogItemUsage).toHaveBeenCalledWith("proj-1", "B2");
		expect(backlogApi.getBacklogItem).toHaveBeenCalled();
	});

	it("shows no usage line for an item with no session", async () => {
		view({ itemId: "leaf" });
		await screen.findByRole("article", { name: /B2/ });
		await waitFor(() => expect(runApi.getBacklogItemUsage).toHaveBeenCalled());
		expect(screen.queryByText("Usage")).not.toBeInTheDocument();
	});
});
