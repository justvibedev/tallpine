import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { TooltipProvider } from "../ui/tooltip";
import { SidebarProvider } from "../ui/sidebar";
import type { WorkspaceSession } from "../../types/workspace";
import { SessionRow } from "./SessionRow";

const endLane = vi.hoisted(() => vi.fn());
vi.mock("../../lib/end-lane", () => ({ endLane }));

function session(overrides: Partial<WorkspaceSession> = {}): WorkspaceSession {
	return { id: "tallpine-orch-2", title: "mobile", kind: "orchestrator", status: "working", ...overrides } as WorkspaceSession;
}

function renderRow(s: WorkspaceSession) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<TooltipProvider>
				<SidebarProvider>
					<ul>
						<SessionRow active={false} onOpen={() => {}} session={s} />
					</ul>
				</SidebarProvider>
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

describe("SessionRow context menu", () => {
	beforeEach(() => {
		endLane.mockReset();
		endLane.mockResolvedValue({ orchestrator: "", workers: ["w1", "w2"] });
	});

	it("offers Archive on every row and End lane only on a lane orchestrator", async () => {
		renderRow(session());
		fireEvent.contextMenu(screen.getByText("mobile"));
		expect(await screen.findByRole("menuitem", { name: "Archive session" })).toBeTruthy();
		expect(screen.queryByRole("menuitem", { name: "End lane…" })).toBeNull();
	});

	it("confirms End lane with the lane and the live-worker count, then ends it", async () => {
		renderRow(session({ lane: "B12" }));
		fireEvent.contextMenu(screen.getByText("mobile"));
		await userEvent.click(await screen.findByRole("menuitem", { name: "End lane…" }));
		expect(await screen.findByText("End lane B12?")).toBeTruthy();
		expect(await screen.findByText(/2 live workers move to the default orchestrator/)).toBeTruthy();
		expect(endLane).toHaveBeenCalledWith("tallpine-orch-2", { dryRun: true, hostId: undefined });
		await userEvent.click(screen.getByRole("button", { name: "Confirm, end lane B12" }));
		await waitFor(() => expect(endLane).toHaveBeenCalledWith("tallpine-orch-2", { hostId: undefined }));
	});

	it("shows the daemon's refusal and keeps Confirm disabled", async () => {
		endLane.mockRejectedValue(new Error("worktree has uncommitted changes"));
		renderRow(session({ lane: "B12" }));
		fireEvent.contextMenu(screen.getByText("mobile"));
		await userEvent.click(await screen.findByRole("menuitem", { name: "End lane…" }));
		expect(await screen.findByText(/worktree has uncommitted changes/)).toBeTruthy();
		expect((screen.getByRole("button", { name: "Confirm, end lane B12" }) as HTMLButtonElement).disabled).toBe(true);
	});
});
