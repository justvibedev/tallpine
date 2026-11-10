import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithClient } from "../../test/backlog-run-harness";
import { planFixture } from "../../test/plan-fixtures";
import { PlanCard } from "./PlanCard";
import { PlanCards } from "./PlanCards";

const api = vi.hoisted(() => ({
	listProjectPlans: vi.fn(),
	answerPlan: vi.fn(),
	acceptPlanDefaults: vi.fn(),
	approvePlan: vi.fn(),
	replanItem: vi.fn(),
	cancelPlan: vi.fn(),
	startPlan: vi.fn(),
}));
vi.mock("../../lib/plan-api", async (importActual) => ({ ...(await importActual<typeof import("../../lib/plan-api")>()), ...api }));

beforeEach(() => {
	localStorage.clear();
	for (const fn of Object.values(api)) fn.mockReset().mockResolvedValue(planFixture());
});

const card = (plan = planFixture()) => renderWithClient(<PlanCard projectId="p1" itemRef="B7" itemTitle="Seat sharing" plan={plan} />);

describe("PlanCard", () => {
	it("says the planner waits for a usage reset, with the time, instead of writing", async () => {
		card(planFixture({ status: "writing", pausedUntil: "2026-11-02T06:20:00Z" }));
		await userEvent.click(screen.getByTestId("plan-card").querySelector("button")!);
		expect(screen.getByRole("status")).toHaveTextContent(/usage limit.*picks the plan up again by itself at/);
		expect(screen.getByText("Waiting for usage reset")).toBeInTheDocument();
		expect(screen.queryByText("The planner is writing the plan from your answers.")).not.toBeInTheDocument();
	});

	it("starts collapsed once answered and expanded while awaiting answers", () => {
		card(planFixture({ status: "writing" }));
		expect(screen.getByTestId("plan-card").querySelector("button")).toHaveAttribute("aria-expanded", "false");
	});

	it("stays open while awaiting answers and folds when the plan moves to writing", () => {
		const { rerender } = card();
		const exp = () => screen.getByTestId("plan-card").querySelector("button")?.getAttribute("aria-expanded");
		expect(exp()).toBe("true");
		rerender(<PlanCard projectId="p1" itemRef="B7" itemTitle="Seat sharing" plan={planFixture({ status: "writing" })} />);
		expect(exp()).toBe("false");
	});

	it("keeps the writing text while the planner is not paused", async () => {
		card(planFixture({ status: "writing" }));
		await userEvent.click(screen.getByTestId("plan-card").querySelector("button")!);
		expect(screen.getByRole("status")).toHaveTextContent("The planner is writing the plan from your answers.");
	});

	it("shows how many assumptions are accepted as written and updates on a change", async () => {
		card();
		expect(screen.getByText("2 assumptions will be accepted as written")).toBeInTheDocument();
		await userEvent.click(screen.getAllByRole("button", { name: "Change" })[0] as HTMLElement);
		await userEvent.type(screen.getByRole("textbox", { name: /Correction for: No new dependency/ }), "x");
		expect(screen.getByText("1 assumption will be accepted as written")).toBeInTheDocument();
	});

	it("highlights only assumptions the planner flagged as scopeCut", () => {
		card(planFixture({ assumptions: [{ id: "a1", text: "Windows support comes later", scopeCut: true }, { id: "a2", text: "English only" }] }));
		expect(screen.getAllByText("Drops or postpones scope")).toHaveLength(1);
		expect(screen.getByText("Windows support comes later").closest("li")).toHaveAttribute("data-scope-cut", "true");
		expect(screen.getByText("English only").closest("li")).not.toHaveAttribute("data-scope-cut");
	});

	it("preselects the recommended answers and marks them", () => {
		card();
		expect(screen.getByRole("radio", { name: /Add Postgres/ })).toBeChecked();
		expect(screen.getByRole("radio", { name: /^No/ })).toBeChecked();
		expect(screen.getAllByText("Recommended")).toHaveLength(2);
	});

	it("sends an Other answer and a correction through the answers route", async () => {
		card();
		await userEvent.click(screen.getAllByRole("radio", { name: "Other" })[0] as HTMLElement);
		await userEvent.type(await screen.findByRole("textbox", { name: "Your answer" }), "Use DuckDB");
		await userEvent.click(screen.getAllByRole("button", { name: "Change" })[0] as HTMLElement);
		await userEvent.type(screen.getByRole("textbox", { name: /Correction for: No new dependency/ }), "DuckDB is fine");
		await userEvent.click(screen.getByRole("button", { name: "Send answers" }));
		await waitFor(() => expect(api.answerPlan).toHaveBeenCalledTimes(1));
		expect(api.answerPlan).toHaveBeenCalledWith("p1", "B7", {
			questions: { q1: { other: "Use DuckDB" }, q2: { option: "no" } },
			corrections: { a1: "DuckDB is fine" },
		});
		expect(api.acceptPlanDefaults).not.toHaveBeenCalled();
	});

	it("takes the defaults route when nothing was changed", async () => {
		card();
		await userEvent.click(screen.getByRole("button", { name: "Send answers" }));
		await waitFor(() => expect(api.acceptPlanDefaults).toHaveBeenCalledWith("p1", "B7"));
		expect(api.answerPlan).not.toHaveBeenCalled();
	});

	it("Go with defaults ignores edits and accepts everything", async () => {
		card();
		await userEvent.click(screen.getByRole("radio", { name: /Keep SQLite/ }));
		await userEvent.click(screen.getByRole("button", { name: "Go with defaults" }));
		await waitFor(() => expect(api.acceptPlanDefaults).toHaveBeenCalledWith("p1", "B7"));
		expect(api.answerPlan).not.toHaveBeenCalled();
	});

	it("will not send an empty Other answer", async () => {
		card();
		await userEvent.click(screen.getAllByRole("radio", { name: "Other" })[0] as HTMLElement);
		expect(screen.getByRole("button", { name: "Send answers" })).toBeDisabled();
	});

	it("shows the decisions and approves the plan", async () => {
		card(planFixture({ status: "awaiting_approval", decisionsMarkdown: "| Q | A |\n|---|---|\n| Store | Postgres |", prSplit: [{ title: "Add store" }], planMarkdown: "# Plan" }));
		expect(screen.getByText("Add store")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Approve plan" }));
		await waitFor(() => expect(api.approvePlan).toHaveBeenCalledWith("p1", "B7"));
	});

	it("offers Plan again when the planner stopped", async () => {
		card(planFixture({ status: "failed", failure: "the planner ended without submitting" }));
		expect(screen.getByText("the planner ended without submitting")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Plan again" }));
		await waitFor(() => expect(api.startPlan).toHaveBeenCalledWith("p1", "B7"));
	});

	it("shows an API failure on the card", async () => {
		api.approvePlan.mockRejectedValue(new Error("The plan changed"));
		card(planFixture({ status: "awaiting_approval" }));
		await userEvent.click(screen.getByRole("button", { name: "Approve plan" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("The plan changed");
	});
});

describe("PlanCard cancel", () => {
	it.each(["awaiting_answers", "awaiting_approval", "failed"] as const)("offers Cancel plan while %s and calls the cancel route", async (status) => {
		card(planFixture({ status }));
		await userEvent.click(screen.getByRole("button", { name: "Cancel plan" }));
		await waitFor(() => expect(api.cancelPlan).toHaveBeenCalledWith("p1", "B7"));
	});

	it("shows no Cancel plan while the planner is still writing", async () => {
		card(planFixture({ status: "writing" }));
		await userEvent.click(screen.getByTestId("plan-card").querySelector("button")!);
		expect(screen.queryByRole("button", { name: "Cancel plan" })).not.toBeInTheDocument();
	});
});

describe("PlanCards", () => {
	it("drops a cancelled card from the stack once the list refetches", async () => {
		api.listProjectPlans.mockResolvedValueOnce([{ itemRef: "B7", itemTitle: "t", plan: planFixture() }]).mockResolvedValue([]);
		renderWithClient(<PlanCards projectId="p1" />);
		await userEvent.click(await screen.findByRole("button", { name: "Cancel plan" }));
		await waitFor(() => expect(screen.queryByTestId("plan-card")).not.toBeInTheDocument());
	});

	it("renders an answered (writing) card collapsed and lets the owner expand it", async () => {
		api.listProjectPlans.mockResolvedValue([{ itemRef: "B7", itemTitle: "t", plan: planFixture({ status: "writing" }) }]);
		renderWithClient(<PlanCards projectId="p1" />);
		const header = (await screen.findByTestId("plan-card")).querySelector("button")!;
		expect(header).toHaveAttribute("aria-expanded", "false");
		await userEvent.click(header);
		expect(header).toHaveAttribute("aria-expanded", "true");
	});

	it("draws nothing while no plan waits", async () => {
		api.listProjectPlans.mockResolvedValue([]);
		const { container } = renderWithClient(<PlanCards projectId="p1" />);
		await waitFor(() => expect(api.listProjectPlans).toHaveBeenCalledWith("p1"));
		expect(container).toBeEmptyDOMElement();
	});

	it("draws one card per waiting plan", async () => {
		api.listProjectPlans.mockResolvedValue([{ itemRef: "B7", itemTitle: "Seat sharing", plan: planFixture() }]);
		renderWithClient(<PlanCards projectId="p1" />);
		expect(await screen.findByTestId("plan-card")).toHaveTextContent("B7 · Seat sharing");
	});

	it("keeps only one card open and opens the next one after answering", async () => {
		const mk = (r: string) => ({ itemRef: r, itemTitle: "t", plan: planFixture({ id: `plan_${r}` }) });
		api.listProjectPlans.mockResolvedValue([mk("B1"), mk("B2"), mk("B3")]);
		renderWithClient(<PlanCards projectId="p1" />);
		await screen.findAllByTestId("plan-card");
		const open = () => screen.getAllByTestId("plan-card").map((c) => c.querySelector("button")?.getAttribute("aria-expanded"));
		expect(open()).toEqual(["true", "false", "false"]);
		await userEvent.click(screen.getAllByTestId("plan-card")[2].querySelector("button")!);
		expect(open()).toEqual(["false", "false", "true"]);
		await userEvent.click(screen.getAllByTestId("plan-card")[0].querySelector("button")!);
		await userEvent.click(screen.getByRole("button", { name: "Send answers" }));
		await waitFor(() => expect(api.acceptPlanDefaults).toHaveBeenCalled());
		await waitFor(() => expect(open()).toEqual(["false", "true", "false"]));
	});

	it("opens the card that needs the owner, not a drafting one above it", async () => {
		api.listProjectPlans.mockResolvedValue([
			{ itemRef: "B1", itemTitle: "t", plan: planFixture({ id: "plan_B1", status: "drafting", questions: [] }) },
			{ itemRef: "B2", itemTitle: "t", plan: planFixture({ id: "plan_B2" }) },
		]);
		renderWithClient(<PlanCards projectId="p1" />);
		await screen.findAllByTestId("plan-card");
		expect(screen.getAllByTestId("plan-card").map((c) => c.querySelector("button")?.getAttribute("aria-expanded"))).toEqual(["false", "true"]);
	});

	it("lets the owner reopen an answered card once it reaches review", async () => {
		const mk = (r: string, over = {}) => ({ itemRef: r, itemTitle: "t", plan: planFixture({ id: `plan_${r}`, ...over }) });
		api.listProjectPlans.mockResolvedValue([mk("B1"), mk("B2")]);
		renderWithClient(<PlanCards projectId="p1" />);
		await screen.findAllByTestId("plan-card");
		await userEvent.click(screen.getAllByRole("button").find((b) => /default/i.test(b.textContent ?? ""))!);
		await waitFor(() => expect(api.acceptPlanDefaults).toHaveBeenCalled());
		api.listProjectPlans.mockResolvedValue([mk("B1", { status: "awaiting_approval", questions: [] }), mk("B2")]);
		const header = () => screen.getAllByTestId("plan-card")[0].querySelector("button")!;
		await userEvent.click(header());
		await waitFor(() => expect(header().getAttribute("aria-expanded")).toBe("true"));
	});

	it("lets the owner collapse the default open card", async () => {
		api.listProjectPlans.mockResolvedValue([{ itemRef: "B1", itemTitle: "t", plan: planFixture({ id: "plan_B1" }) }, { itemRef: "B2", itemTitle: "t", plan: planFixture({ id: "plan_B2" }) }]);
		renderWithClient(<PlanCards projectId="p1" />);
		await screen.findAllByTestId("plan-card");
		const header = () => screen.getAllByTestId("plan-card")[0].querySelector("button")!;
		expect(header().getAttribute("aria-expanded")).toBe("true");
		await userEvent.click(header());
		expect(header().getAttribute("aria-expanded")).toBe("false");
	});

	it("collapses and opens the next card after Go with defaults", async () => {
		const mk = (r: string) => ({ itemRef: r, itemTitle: "t", plan: planFixture({ id: `plan_${r}` }) });
		api.listProjectPlans.mockResolvedValue([mk("B1"), mk("B2")]);
		renderWithClient(<PlanCards projectId="p1" />);
		await screen.findAllByTestId("plan-card");
		await userEvent.click(screen.getAllByRole("button").find((b) => /default/i.test(b.textContent ?? ""))!);
		await waitFor(() => expect(api.acceptPlanDefaults).toHaveBeenCalledWith("p1", "B1"));
		await waitFor(() => expect(screen.getAllByTestId("plan-card").map((c) => c.querySelector("button")?.getAttribute("aria-expanded"))).toEqual(["false", "true"]));
	});

	it("keeps draft answers across collapse and expand", async () => {
		card();
		await userEvent.click(screen.getAllByRole("radio", { name: /Other/ })[0]);
		await userEvent.type(screen.getByRole("textbox"), "my own");
		const header = screen.getByTestId("plan-card").querySelector("button")!;
		await userEvent.click(header);
		await userEvent.click(header);
		expect(screen.getByRole("textbox")).toHaveValue("my own");
	});

	it("stacks several long cards in a bounded scroller so every card stays reachable", async () => {
		const long = (id: string) => planFixture({ id, questions: Array.from({ length: 8 }, (_, i) => ({ id: `q${i}`, text: `Question ${i} `.repeat(30), recommended: "a", options: [{ id: "a", label: "A" }, { id: "b", label: "B" }] })) });
		api.listProjectPlans.mockResolvedValue(["B1", "B2", "B3"].map((r) => ({ itemRef: r, itemTitle: "t", plan: long(`plan_${r}`) })));
		renderWithClient(<PlanCards projectId="p1" />);
		const stack = await screen.findByTestId("plan-cards");
		expect(stack.className).toMatch(/max-h-/);
		expect(stack.className).toMatch(/overflow-y-auto/);
		const cards = screen.getAllByTestId("plan-card");
		expect(cards).toHaveLength(3);
		for (const c of cards) {
			expect(stack.contains(c)).toBe(true);
			expect(c.className).toMatch(/shrink-0/);
		}
		expect(screen.getAllByTestId("plan-card-scroll")).toHaveLength(1);
	});
});

describe("PlanCard tall plan", () => {
	it("caps the card height and scrolls the body while keeping Approve and Re-plan outside the scroller", () => {
		card(planFixture({ status: "awaiting_approval", questions: [], prSplit: Array.from({ length: 40 }, (_, i) => ({ title: `PR ${i}`, summary: "s" })) }));
		const cardEl = screen.getByTestId("plan-card");
		expect(cardEl.className).toContain("max-h-");
		const scroller = screen.getByTestId("plan-card-scroll");
		expect(scroller.className).toContain("overflow-y-auto");
		expect(screen.getByText("PR 39")).toBeInTheDocument();
		expect(scroller.contains(screen.getByRole("button", { name: "Approve plan" }))).toBe(false);
		expect(scroller.contains(screen.getByRole("button", { name: "Re-plan" }))).toBe(false);
	});
});

describe("PlanCard tall answering phase", () => {
	it("keeps Send answers and the defaults button outside the scroller", () => {
		card(planFixture({ questions: Array.from({ length: 30 }, (_, i) => ({ id: `q${i}`, text: `Question ${i}`, recommended: "a", options: [{ id: "a", label: `Opt ${i}` }] })) }));
		const scroller = screen.getByTestId("plan-card-scroll");
		expect(scroller.className).toContain("overflow-y-auto");
		expect(scroller.contains(screen.getByText("Question 29"))).toBe(true);
		expect(scroller.contains(screen.getByRole("button", { name: "Send answers" }))).toBe(false);
		const defaults = screen.getAllByRole("button").find((b) => /default/i.test(b.textContent ?? ""));
		expect(defaults && scroller.contains(defaults)).toBe(false);
	});
});
