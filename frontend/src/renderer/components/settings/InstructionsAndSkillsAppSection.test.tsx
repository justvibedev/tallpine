import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { InstructionsAndSkillsAppSection } from "./InstructionsAndSkillsAppSection";

const { getMock, patchMock, postMock } = vi.hoisted(() => ({ getMock: vi.fn(), patchMock: vi.fn(), postMock: vi.fn() }));
vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: getMock, PATCH: patchMock, POST: postMock },
	apiErrorMessage: (error: { message?: string }) => error?.message ?? "error",
}));

const role = (name: string, on: boolean, set: boolean, def: boolean) => ({ role: name, on, set, default: def, app: false, effects: [{ harness: "claude-code", effect: "enforced" }] });
const skill = (name: string, convention: string, reviewerOn: boolean, reviewerSet: boolean) => ({
	name, description: `${name} help`, scope: convention === "custom-app" ? "custom" : "user", convention, path: `/s/${name}`, harnesses: [], deselected: false, removable: false,
	roles: [role("worker", true, false, true), role("orchestrator", true, false, true), role("reviewer", reviewerOn, reviewerSet, false)],
	access: ["all", "agentClaudeCode", "agentCodex", "agentKiro", "agentDeepseek"].map((key) => ({ key, on: true, set: false, default: true, app: false })),
});

let values: { key: string; value: unknown }[] = [];

function renderSection() {
	getMock.mockImplementation(async (path: string) => {
		if (path === "/api/v1/settings/values") return { data: { values }, error: undefined };
		if (path === "/api/v1/settings/skills") return { data: { harnesses: ["claude-code"], skills: [skill("house", "custom-app", true, true), skill("design", "claude", false, false)] }, error: undefined };
		return { data: undefined, error: { message: `unexpected GET ${path}` } };
	});
	return render(
		<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
			<InstructionsAndSkillsAppSection />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	getMock.mockReset();
	patchMock.mockReset();
	patchMock.mockResolvedValue({ data: {}, error: undefined });
	values = [
		{ key: "app.workerRules", value: "Saved worker rule." },
		{ key: "app.orchestratorRules", value: "" },
		{ key: "app.reviewerRules", value: "" },
		{ key: "app.skillRoles", value: { reviewer: { house: true } } },
	];
});

describe("Settings > Instructions and skills", () => {
	it("saves each role's app rules through the settings registry", async () => {
		renderSection();
		const worker = await screen.findByLabelText("Worker rules");
		expect(worker).toHaveValue("Saved worker rule.");
		const save = screen.getAllByRole("button", { name: "Save" })[0];
		expect(save).toBeDisabled(); // nothing changed yet
		await userEvent.clear(worker);
		await userEvent.type(worker, "Every worker runs the linter.");
		await userEvent.click(save);
		await waitFor(() => expect(patchMock).toHaveBeenCalledWith("/api/v1/settings/values", { body: { set: { "app.workerRules": "Every worker runs the linter." } } }));
	});

	it("switches a skill app-wide, resets it, and manages app custom skills", async () => {
		renderSection();
		const design = await screen.findByTestId("skill-role-row-app-skill-design");
		await userEvent.click(within(design).getByRole("switch", { name: "design for Reviewer" }));
		await waitFor(() => expect(patchMock).toHaveBeenCalledWith("/api/v1/settings/values", { body: { set: { "app.skillRoles": { reviewer: { house: true, design: true } } } } }));
		const house = screen.getByTestId("skill-role-row-app-skill-house");
		await userEvent.click(within(house).getByRole("button", { name: "Reset house to the inherited switches" }));
		await waitFor(() => expect(patchMock).toHaveBeenLastCalledWith("/api/v1/settings/values", { body: { set: { "app.skillRoles": {} } } }));
		expect(screen.getByTestId("custom-skill-house")).toBeInTheDocument();
		expect(screen.queryByTestId("custom-skill-design")).toBeNull(); // account skills are not app custom skills
	});

	it("saves the master and per-agent skill switches, and locks the role switches while the master is off", async () => {
		renderSection();
		const design = await screen.findByTestId("skill-role-row-app-skill-design");
		expect(within(design).getByTestId("skill-agents-app-skill-design")).toHaveTextContent("All agents");
		await userEvent.click(within(design).getByTestId("skill-agents-app-skill-design"));
		await userEvent.click(screen.getByRole("switch", { name: "design for Codex" }));
		await userEvent.keyboard("{Escape}");
		await waitFor(() => expect(patchMock).toHaveBeenLastCalledWith("/api/v1/settings/values", { body: { set: { "app.skillRoles": { reviewer: { house: true }, agentCodex: { design: false } } } } }));
		await userEvent.click(within(design).getByRole("switch", { name: "design: enabled everywhere" }));
		await waitFor(() => expect(patchMock).toHaveBeenLastCalledWith("/api/v1/settings/values", { body: { set: { "app.skillRoles": { reviewer: { house: true }, all: { design: false } } } } }));
	});
});
