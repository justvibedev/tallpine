import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import en from "../../i18n/en.json";
import { ReviewsSettingsSection } from "./ReviewsSettingsSection";

const { update, state } = vi.hoisted(() => ({ update: vi.fn(), state: { autoReview: false, planningMode: "auto", planAutoApprove: false, reviewerHarness: "", lock: { mode: "", autoApproveOff: false } } }));

vi.mock("../../hooks/useSettings", () => ({
	useSettings: () => ({ settings: { workflowDefaults: { autoReview: state.autoReview, planningMode: state.planningMode, planAutoApprove: state.planAutoApprove, reviewerHarness: state.reviewerHarness, reviewerModel: "" } }, isLoading: false }),
}));
vi.mock("../../hooks/useUpdateWorkflowDefaults", () => ({
	useUpdateWorkflowDefaults: () => ({ update, saving: false, error: undefined }),
}));
vi.mock("../../hooks/usePlannerLocks", () => ({ usePlannerLocks: () => state.lock }));
vi.mock("./AgentPolicySection", () => ({ AgentPolicySection: ({ scope, children }: { scope?: string; children?: React.ReactNode }) => <div data-testid="policy" data-scope={scope}>{children}</div> }));

beforeEach(() => { update.mockReset(); state.autoReview = false; state.planningMode = "auto"; state.planAutoApprove = false; state.reviewerHarness = ""; state.lock = { mode: "", autoApproveOff: false }; });

const renderSection = () => render(<QueryClientProvider client={new QueryClient()}><ReviewsSettingsSection /></QueryClientProvider>);

describe("ReviewsSettingsSection", () => {
	it("shows the app default and saves a change through the daemon", async () => {
		renderSection();
		expect(screen.getByRole("heading", { name: "Reviews" })).toBeInTheDocument();
		const toggle = screen.getByRole("switch", { name: "Review pull requests automatically" });
		expect(toggle).not.toBeChecked();
		await userEvent.setup().click(toggle);
		expect(update).toHaveBeenCalledWith({ autoReview: true });
	});

	it("saves claude-code as the app default reviewer and an empty one for worker-derived", async () => {
		renderSection();
		const user = userEvent.setup();
		expect(screen.getByRole("button", { name: "Default reviewer agent" })).toHaveTextContent("Worker-derived");
		await user.click(screen.getByRole("button", { name: "Default reviewer agent" }));
		await user.click(await screen.findByRole("menuitem", { name: /Claude Code/ }));
		expect(update).toHaveBeenCalledWith({ reviewerHarness: "claude-code" });
	});

	it("shows the model row once an app default reviewer is set", () => {
		state.reviewerHarness = "codex";
		renderSection();
		expect(screen.getByText("Default reviewer model")).toBeInTheDocument();
	});

	it("reflects an app default that is on", () => {
		state.autoReview = true;
		renderSection();
		expect(screen.getByRole("switch", { name: "Review pull requests automatically" })).toBeChecked();
	});

	it("carries the planning section, with no Locked off or Project decides wording", () => {
		const { container } = renderSection();
		expect(screen.getByTestId("policy")).toHaveAttribute("data-scope", "planning");
		expect(container.textContent).not.toMatch(/Locked off|Project decides/);
	});

	it("saves the app default planning mode and plan auto-approve", async () => {
		renderSection();
		const user = userEvent.setup();
		expect(screen.getByRole("button", { name: "Plan before building" })).toHaveTextContent("Automatic");
		await user.click(screen.getByRole("button", { name: "Plan before building" }));
		await user.click(await screen.findByRole("menuitem", { name: "Always plan" }));
		expect(update).toHaveBeenCalledWith({ planningMode: "always" });
		const toggle = screen.getByRole("switch", { name: "Approve plans automatically" });
		expect(toggle).not.toBeChecked();
		await user.click(toggle);
		expect(update).toHaveBeenCalledWith({ planAutoApprove: true });
	});

	it("warns that an app-wide auto-approve applies to every project without its own value", () => {
		expect(en["settings.reviews.planAutoApprove.description"]).toMatch(/every project that sets nothing of its own/);
	});

	it("shows managed planner locks read-only", () => {
		state.lock = { mode: "always", autoApproveOff: true };
		renderSection();
		const managed = screen.getAllByTestId("planning-default-managed");
		expect(managed[0]).toHaveTextContent("Set by your organization: Always plan");
		expect(managed[1]).toHaveTextContent("Set by your organization: Off");
		expect(screen.queryByRole("switch", { name: "Approve plans automatically" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Plan before building" })).not.toBeInTheDocument();
	});

	it("has no Locked off or Project decides copy in the planning strings", () => {
		const planning = Object.entries(en).filter(([key]) => key.startsWith("settings.agentPolicy.planner.") || key === "settings.agentPolicy.projectDecides");
		expect(planning.length).toBeGreaterThan(10);
		for (const [key, value] of planning) expect(`${key}: ${value}`).not.toMatch(/Locked off|Project decides/);
	});
});
