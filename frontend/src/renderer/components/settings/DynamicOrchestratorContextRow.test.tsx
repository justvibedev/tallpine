import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { DynamicOrchestratorContextRow } from "./DynamicOrchestratorContextRow";

const { update, state } = vi.hoisted(() => ({ update: vi.fn(), state: { locked: false, enabled: true } }));

vi.mock("../../hooks/useManagedLocks", () => ({ useManagedLocks: () => new Set(state.locked ? ["app.dynamicOrchestratorContext"] : []) }));
vi.mock("../../hooks/useSettings", () => ({
	useSettings: () => ({ settings: { dynamicOrchestratorContext: state.enabled }, isLoading: false }),
}));
vi.mock("../../hooks/useUpdateDynamicOrchestratorContext", () => ({
	useUpdateDynamicOrchestratorContext: () => ({ update, saving: false, error: undefined }),
}));

beforeEach(() => { update.mockReset(); state.locked = false; state.enabled = true; });

describe("DynamicOrchestratorContextRow", () => {
	it("shows the daemon value and explains the feature", () => {
		render(<DynamicOrchestratorContextRow />);
		expect(screen.getByRole("switch", { name: "Dynamic orchestrator context" })).toBeChecked();
		expect(screen.getByText(/research/i)).toBeInTheDocument();
	});

	it("turns it off through the daemon", async () => {
		render(<DynamicOrchestratorContextRow />);
		await userEvent.setup().click(screen.getByRole("switch", { name: "Dynamic orchestrator context" }));
		expect(update).toHaveBeenCalledWith(false);
	});

	it("is read-only when the organization manages the setting", () => {
		state.locked = true;
		render(<DynamicOrchestratorContextRow />);
		expect(screen.getByRole("switch", { name: "Dynamic orchestrator context" })).toBeDisabled();
	});
});
