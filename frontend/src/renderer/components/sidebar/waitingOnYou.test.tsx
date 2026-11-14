import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { OwnerAsk } from "../../lib/ask-api";
import type { WorkspaceSession, WorkspaceSummary } from "../../types/workspace";
import { planFixture } from "../../test/plan-fixtures";
import { SidebarProvider } from "../ui/sidebar";
import { TooltipProvider } from "../ui/tooltip";
import { ProjectRowButton } from "./ProjectRowButton";
import { SessionRow } from "./SessionRow";
import { useWaitingOnYouCount } from "./orchestratorNeedsYou";

const api = vi.hoisted(() => ({ listProjectAsks: vi.fn(), listProjectPlans: vi.fn() }));
vi.mock("../../lib/ask-api", async (importActual) => ({ ...(await importActual<typeof import("../../lib/ask-api")>()), listProjectAsks: api.listProjectAsks }));
vi.mock("../../lib/plan-api", async (importActual) => ({ ...(await importActual<typeof import("../../lib/plan-api")>()), listProjectPlans: api.listProjectPlans }));

const ask = (o: Partial<OwnerAsk>): OwnerAsk => ({ id: "a", projectId: "p1", ref: "Q1", text: "t", state: "open", anchored: false, options: [], recommended: "", createdAt: "", updatedAt: "", ...o });

beforeEach(() => {
	api.listProjectAsks.mockReset().mockResolvedValue([ask({ id: "1" }), ask({ id: "2", lane: "B7", ref: "Q2" }), ask({ id: "3", lane: "B7", ref: "Q3" })]);
	api.listProjectPlans.mockReset().mockResolvedValue([{ itemRef: "B1", itemTitle: "x", plan: planFixture() }]);
});

function wrap(ui: React.ReactElement) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}><TooltipProvider><SidebarProvider>{ui}</SidebarProvider></TooltipProvider></QueryClientProvider>);
}

function Probe({ lane, sessionId }: { lane?: string; sessionId?: string }) {
	return <span data-testid="count">{useWaitingOnYouCount("p1", { lane, sessionId }, true)}</span>;
}

describe("sidebar waiting count", () => {
	it("sums open asks and waiting plans for the main chat", async () => {
		wrap(<Probe />);
		await waitFor(() => expect(screen.getByTestId("count")).toHaveTextContent("2"));
	});

	it("counts only the lane's asks on a lane row", async () => {
		wrap(<Probe lane="B7" sessionId="lane-1" />);
		await waitFor(() => expect(screen.getByTestId("count")).toHaveTextContent("2"));
	});

	it("shows the count badge on the project row and the Needs-you dot at zero", () => {
		const workspace = { id: "p1", name: "App" } as WorkspaceSummary;
		const row = (waiting: number, needsYou: boolean) => (
			<ProjectRowButton workspace={workspace} nameWithHost="App" isStandalone={false} expanded={false} dashboardActive={false} projectActive={false}
				needsYou={needsYou} waiting={waiting} projectIsDragging={false} projectDragInProgress={false} onProjectClick={() => {}} onFolderClick={() => {}} />
		);
		const view = wrap(<ul>{row(3, true)}</ul>);
		expect(document.querySelector("[data-project-waiting]")).toHaveTextContent("3");
		expect(document.querySelector("[data-project-needs-you]")).toBeNull();
		view.unmount();
		wrap(<ul>{row(0, true)}</ul>);
		expect(document.querySelector("[data-project-waiting]")).toBeNull();
		expect(document.querySelector("[data-project-needs-you]")).not.toBeNull();
	});

	it("shows the count on a lane orchestrator row, and none on a plain session", async () => {
		const s = (o: Partial<WorkspaceSession>) => ({ id: "lane-1", title: "mobile", kind: "orchestrator", status: "working", workspaceId: "p1", ...o }) as WorkspaceSession;
		const view = wrap(<ul><SessionRow active={false} onOpen={() => {}} session={s({ lane: "B7" })} /></ul>);
		await waitFor(() => expect(document.querySelector("[data-lane-waiting]")).toHaveTextContent("2"));
		view.unmount();
		api.listProjectAsks.mockClear();
		wrap(<ul><SessionRow active={false} onOpen={() => {}} session={s({ id: "w1", kind: "worker" })} /></ul>);
		expect(document.querySelector("[data-lane-waiting]")).toBeNull();
		expect(api.listProjectAsks).not.toHaveBeenCalled();
	});
});
