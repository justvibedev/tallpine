import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { backlogItem } from "../../test/backlog-fixtures";
import { renderWithClient } from "../../test/backlog-run-harness";
import { planFixture } from "../../test/plan-fixtures";
import { BacklogPlanSection } from "./BacklogPlanSection";

const api = vi.hoisted(() => ({
	isPlannerEnabled: vi.fn(),
	getItemPlan: vi.fn(),
	startPlan: vi.fn(),
	replanItem: vi.fn(),
	buildPlanAnyway: vi.fn(),
	answerPlan: vi.fn(),
	acceptPlanDefaults: vi.fn(),
	approvePlan: vi.fn(),
}));
vi.mock("../../lib/plan-api", async (importActual) => ({ ...(await importActual<typeof import("../../lib/plan-api")>()), ...api }));

beforeEach(() => {
	for (const fn of Object.values(api)) fn.mockReset().mockResolvedValue(planFixture());
	api.isPlannerEnabled.mockResolvedValue(true);
});

const summaryOf = (status: string, over: Record<string, boolean> = {}) => ({ id: "plan_1", status, stale: false, itemChanged: false, ...over }) as never;
const render = (item = backlogItem()) => renderWithClient(<BacklogPlanSection item={item} />);

describe("BacklogPlanSection", () => {
	it("offers Plan now on an unplanned open item and starts a plan", async () => {
		const item = backlogItem({ ref: "B7" });
		render(item);
		await userEvent.click(await screen.findByRole("button", { name: "Plan now" }));
		await waitFor(() => expect(api.startPlan).toHaveBeenCalledWith(item.projectId, "B7"));
	});

	it("does not offer Plan now on a child made from a plan", async () => {
		render(backlogItem({ fields: { "plan.pr": "2" } }));
		await waitFor(() => expect(api.isPlannerEnabled).not.toHaveBeenCalled());
		expect(screen.queryByRole("button", { name: "Plan now" })).toBeNull();
	});

	it("shows nothing while the planner is off", async () => {
		api.isPlannerEnabled.mockResolvedValue(false);
		render();
		await waitFor(() => expect(api.isPlannerEnabled).toHaveBeenCalled());
		expect(screen.queryByRole("button", { name: "Plan now" })).toBeNull();
		expect(screen.queryByTestId("backlog-plan")).toBeNull();
	});

	it("does not offer Plan now on a parent item", async () => {
		render(backlogItem({ childCount: 2 }));
		await waitFor(() => expect(api.isPlannerEnabled).not.toHaveBeenCalled());
		expect(screen.queryByRole("button", { name: "Plan now" })).toBeNull();
	});

	it("shows the status chip and the plan card while a plan waits for the owner", async () => {
		api.getItemPlan.mockResolvedValue(planFixture({ status: "awaiting_answers" }));
		render(backlogItem({ plan: summaryOf("awaiting_answers") }));
		expect(await screen.findByTestId("plan-chip")).toHaveTextContent("Needs your answers");
		expect(screen.getByTestId("plan-card")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Plan now" })).toBeNull();
	});

	it("shows an approved plan to read, with no card", async () => {
		api.getItemPlan.mockResolvedValue(planFixture({ status: "approved", decisionsMarkdown: "Use SQLite" }));
		render(backlogItem({ plan: summaryOf("approved") }));
		expect(await screen.findByTestId("plan-reader")).toBeInTheDocument();
		expect(screen.getByTestId("plan-chip")).toHaveTextContent("Approved");
		expect(screen.queryByTestId("plan-card")).toBeNull();
		expect(screen.queryByRole("button", { name: "Build anyway" })).toBeNull();
	});

	it("warns on a stale plan and offers Re-plan and Build anyway", async () => {
		api.getItemPlan.mockResolvedValue(planFixture({ status: "approved", stale: true }));
		const item = backlogItem({ ref: "B9", plan: summaryOf("approved", { stale: true }) });
		render(item);
		expect(await screen.findByText(/changed on the default branch/)).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Build anyway" }));
		await waitFor(() => expect(api.buildPlanAnyway).toHaveBeenCalledWith(item.projectId, "B9"));
		await userEvent.click(screen.getByRole("button", { name: "Re-plan" }));
		await waitFor(() => expect(api.replanItem).toHaveBeenCalledWith(item.projectId, "B9"));
	});

	it("flags an item edited after planning began", async () => {
		api.getItemPlan.mockResolvedValue(planFixture({ status: "awaiting_approval", itemChanged: true }));
		render(backlogItem({ plan: summaryOf("awaiting_approval", { itemChanged: true }) }));
		expect(await screen.findByText("This item was edited after planning began.")).toBeInTheDocument();
	});

	it("keeps Build anyway out of a plan that is not stale", async () => {
		api.getItemPlan.mockResolvedValue(planFixture({ status: "approved", stale: false, buildAnyway: true }));
		render(backlogItem({ plan: summaryOf("approved") }));
		expect(await screen.findByText(/build from this plan although it is out of date/)).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Build anyway" })).toBeNull();
	});
});
