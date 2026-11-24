import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { agentReadiness } from "../../test/agent-readiness-fixtures";
import { DefaultRoleRows } from "./DefaultRoleRows";

const { field, update, state } = vi.hoisted(() => ({ field: vi.fn(() => null), update: vi.fn(), state: { defaults: {} as Record<string, string> } }));
vi.mock("../../hooks/useSettings", () => ({ useSettings: () => ({ settings: { workflowDefaults: state.defaults }, isLoading: false }) }));
vi.mock("../../hooks/useUpdateWorkflowDefaults", () => ({ useUpdateWorkflowDefaults: () => ({ update, saving: false, error: undefined }) }));
vi.mock("../../hooks/useAgentReadinessQuery", () => ({
	useAgentReadinessQuery: () => ({ data: { agents: [agentReadiness("claude-code", "Claude Code"), agentReadiness("codex", "Codex"), agentReadiness("kiro", "Kiro", { installation: "not_installed" })] } }),
}));
vi.mock("./ProjectAgentRoleControls", () => ({ AgentModelField: field }));

const renderRows = () => render(<QueryClientProvider client={new QueryClient()}><DefaultRoleRows /></QueryClientProvider>);

beforeEach(() => { state.defaults = {}; field.mockClear(); update.mockClear(); });

describe("DefaultRoleRows", () => {
	it("shows no model picker until an agent is chosen, and never offers an agent that is not ready", async () => {
		renderRows();
		expect(field).not.toHaveBeenCalled();
		await userEvent.setup().click(screen.getByRole("button", { name: "Default worker agent" }));
		expect(await screen.findByRole("menuitem", { name: /Claude Code/ })).toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: /Kiro/ })).not.toBeInTheDocument();
	});

	it("saves the picked worker agent under its own key", async () => {
		renderRows();
		const user = userEvent.setup();
		await user.click(screen.getByRole("button", { name: "Default worker agent" }));
		await user.click(await screen.findByRole("menuitem", { name: /^Codex/ }));
		expect(update).toHaveBeenCalledWith({ workerAgent: "codex" });
	});

	it("passes the role's model and effort to the picker, which honours disabled models, and saves each under its key", () => {
		state.defaults = { orchestratorAgent: "claude-code", orchestratorModel: "claude-opus-5-5", orchestratorEffort: "medium" };
		renderRows();
		const props = (field.mock.calls as unknown as [{ role: string; agentId: string; model: string; effort: string; onModelChange: (v: string) => void; onEffortChange: (v: string) => void }][])[0][0];
		expect(props).toMatchObject({ role: "orchestrator", agentId: "claude-code", model: "claude-opus-5-5", effort: "medium" });
		props.onModelChange("m2");
		props.onEffortChange("high");
		expect(update).toHaveBeenCalledWith({ orchestratorModel: "m2" });
		expect(update).toHaveBeenCalledWith({ orchestratorEffort: "high" });
	});
});
