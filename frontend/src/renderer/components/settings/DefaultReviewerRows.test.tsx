import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { agentReadiness } from "../../test/agent-readiness-fixtures";
import { DefaultReviewerRows } from "./DefaultReviewerRows";

const { combobox, update, state } = vi.hoisted(() => ({ combobox: vi.fn(() => null), update: vi.fn(), state: { error: undefined as string | undefined, agents: undefined as unknown } }));
vi.mock("../../hooks/useSettings", () => ({ useSettings: () => ({ settings: { workflowDefaults: { reviewerHarness: "codex", reviewerModel: "" } }, isLoading: false }) }));
vi.mock("../../hooks/useUpdateWorkflowDefaults", () => ({ useUpdateWorkflowDefaults: () => ({ update, saving: false, error: state.error }) }));
vi.mock("../../hooks/useAgentReadinessQuery", () => ({ useAgentReadinessQuery: () => ({ data: state.agents ? { agents: state.agents } : undefined }) }));
vi.mock("./AgentModelCombobox", async (importOriginal) => ({ ...(await importOriginal<object>()), AgentModelCombobox: combobox }));

const renderRows = () => render(<QueryClientProvider client={new QueryClient()}><DefaultReviewerRows /></QueryClientProvider>);

beforeEach(() => { state.error = undefined; state.agents = undefined; combobox.mockClear(); update.mockClear(); });

describe("DefaultReviewerRows", () => {
	it("offers no reasoning effort, since the app default stores none", () => {
		renderRows();
		expect(combobox).toHaveBeenCalled();
		for (const [props] of combobox.mock.calls as unknown as [{ tuning?: unknown }][]) expect(props.tuning).toBeUndefined();
	});

	it("shows a failed save instead of silently reverting the picker", () => {
		state.error = "Could not save";
		renderRows();
		expect(screen.getByRole("alert")).toHaveTextContent("Could not save");
	});

	it("does not offer a reviewer harness that is not ready on this machine", async () => {
		state.agents = [agentReadiness("claude-code", "Claude Code"), agentReadiness("codex", "Codex"), agentReadiness("kiro", "Kiro", { installation: "not_installed" })];
		renderRows();
		await userEvent.setup().click(screen.getByRole("button", { name: "Default reviewer agent" }));
		expect(await screen.findByRole("menuitem", { name: /Claude Code/ })).toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: /Kiro/ })).not.toBeInTheDocument();
	});

	it("does not save, so the model is kept, when the current harness is picked again", async () => {
		state.agents = [agentReadiness("claude-code", "Claude Code"), agentReadiness("codex", "Codex")];
		renderRows();
		const user = userEvent.setup();
		await user.click(screen.getByRole("button", { name: "Default reviewer agent" }));
		await user.click(await screen.findByRole("menuitem", { name: /^Codex/ }));
		expect(update).not.toHaveBeenCalled();
	});
});

