import {
	cloudMocks,
	terminalFocusRequested,
	terminalStateCallback,
	terminalMuxFactory,
	catalogWithInstalled,
	catalog,
	plans,
	findListRow,
	findAgentRow,
	backToList,
	renderSection,
	readyCatalog,
	mockInstalledOperations,
} from "./HarnessSettingsSection.test.setup";

import { QueryClient } from "@tanstack/react-query";
import {
	act,
	cleanup,
	screen,
	waitFor,
	within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
	afterEach,
	beforeEach,
	describe,
	expect,
	it,
	vi,
} from "vitest";
import { apiClient } from "../../lib/api-client";
import { tallpineBridge } from "../../lib/bridge";
import { appI18n } from "../../i18n";
import { agentReadiness } from "../../test/agent-readiness-fixtures";



describe("HarnessSettingsSection", () => {
	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		terminalFocusRequested.value = false;
		terminalStateCallback.value = undefined;
		cloudMocks.cloudEnabled = false;
		cloudMocks.org = undefined;
		cloudMocks.connections = [];
		window.tallpine!.clipboard.writeText = vi.fn().mockResolvedValue(undefined);
		vi.spyOn(apiClient, "GET").mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			return { data: undefined } as never;
		});
		vi.spyOn(apiClient, "POST").mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness/ensure") return { data: catalog } as never;
			if (path === "/api/v1/agents/refresh") return { data: catalog } as never;
			if (path === "/api/v1/agents/{agent}/install") {
				return { data: { target: "codex", status: "failed", error: "npm failed" } } as never;
			}
			return { data: undefined } as never;
		});
	});

	afterEach(() => {
		vi.useRealTimers();
		vi.restoreAllMocks();
	});

	function mockModelSwitches(disabledModels: string[], catalog: { id: string; label: string; isDefault?: boolean; provider?: string }[] = [
		{ id: "gpt-a", label: "GPT A", isDefault: true },
		{ id: "gpt-b", label: "GPT B" },
		{ id: "gpt-c", label: "GPT C" },
	]) {
		mockInstalledOperations("npm");
		const models = { agentId: "codex", models: catalog.map((model) => ({ ...model, efforts: [] })) };
		let saved = disabledModels;
		const get = vi.mocked(apiClient.GET).getMockImplementation()!;
		vi.mocked(apiClient.GET).mockImplementation(async (path, options) => path === "/api/v1/agents/{agent}/models" ? { data: models } as never
			: path === "/api/v1/settings" ? { data: { harnessDefaults: { codex: { disabledModels: saved } } } } as never
			: (get as (path: string, options: unknown) => Promise<never>)(path, options));
		const post = vi.mocked(apiClient.POST).getMockImplementation()!;
		vi.mocked(apiClient.POST).mockImplementation(async (path, options) => path === "/api/v1/agents/{agent}/models/refresh"
			? { data: models } as never : (post as (path: string, options: unknown) => Promise<never>)(path, options));
		const patch = vi.spyOn(apiClient, "PATCH").mockImplementation(async (_path, options) => {
			saved = (options as { body: { disabledModels: string[] } }).body.disabledModels;
			return { data: { harnessDefaults: { codex: { disabledModels: saved } } } } as never;
		});
		return patch;
	}

	async function openModels() {
		renderSection();
		const page = await findAgentRow("codex");
		await userEvent.click(within(page).getByRole("tab", { name: "Models" }));
		await within(page).findByRole("list", { name: "Available models" });
		return page;
	}

	it("lists DeepSeek when the catalog has it", async () => {
		renderSection();
		expect(await findListRow("deepseek-harness")).toHaveAccessibleName("Open DeepSeek details");
	});

	it("lists names with a status and update mark, then opens one harness page with its actions", async () => {
		mockInstalledOperations("npm");
		renderSection();
		const listRow = await findListRow("codex");
		expect(listRow).toHaveAccessibleName("Open Codex details");
		expect(await within(listRow).findByRole("img", { name: "v1.3.0 available" })).toBeInTheDocument();
		expect(listRow).toHaveAccessibleDescription("Connected");
		// Versions live on the harness page, not in the list.
		expect(within(listRow).queryByText("v1.2.3")).toBeNull();
		expect(within(listRow).queryByText("v1.3.0 available")).toBeNull();
		// The list only navigates; actions live on the harness page.
		expect(within(listRow).queryByRole("button")).toBeNull();
		listRow.focus();
		await userEvent.keyboard("{Enter}");
		const page = await findAgentRow("codex");
		expect(screen.queryByRole("textbox", { name: "Search harnesses" })).toBeNull();
		expect(within(page).getByRole("tab", { name: "Account" })).toHaveAttribute("aria-selected", "true");
		expect(within(page).getByRole("button", { name: "Update" })).toBeEnabled();
		expect(within(page).getByText("Installed version")).toBeInTheDocument();
		// A historical install/check date is not a verified update date.
		expect(within(page).queryByText("Last updated")).toBeNull();
		expect(within(page).queryByRole("combobox")).toBeNull();
		expect(within(page).queryByRole("button", { name: "Refresh login" })).toBeNull();
		backToList();
		expect(await findListRow("claude-code")).toBeInTheDocument();
		expect(screen.getByRole("textbox", { name: "Search harnesses" })).toBeInTheDocument();
	});

	it("lists model metadata without a manual refresh button", async () => {
		mockInstalledOperations("npm");
		const models = {
			agentId: "codex", lastSuccessAt: "2026-10-08T09:00:00Z",
			models: [
				{ id: "gpt-5.3-codex", label: "GPT-5.3 Codex", isDefault: true, efforts: ["low", "medium", "high"], contextWindow: 200000, inputs: ["text", "image"] },
				{ id: "gpt-5.3-mini", label: "gpt-5.3-mini", efforts: [] },
			],
		};
		const get = vi.mocked(apiClient.GET).getMockImplementation()!;
		vi.mocked(apiClient.GET).mockImplementation(async (path, options) => path === "/api/v1/agents/{agent}/models"
			? { data: models } as never : (get as (path: string, options: unknown) => Promise<never>)(path, options));
		const post = vi.mocked(apiClient.POST).getMockImplementation()!;
		vi.mocked(apiClient.POST).mockImplementation(async (path, options) => path === "/api/v1/agents/{agent}/models/refresh"
			? { data: models } as never : (post as (path: string, options: unknown) => Promise<never>)(path, options));
		renderSection();
		const page = await findAgentRow("codex");
		await userEvent.click(within(page).getByRole("tab", { name: "Models" }));
		const list = await within(page).findByRole("list", { name: "Available models" });
		expect(within(list).getByText("GPT-5.3 Codex")).toBeInTheDocument();
		expect(within(list).queryByText("gpt-5.3-codex")).toBeNull();
		expect(within(list).getByText("Default")).toBeInTheDocument();
		expect(within(list).getByText("Effort: low · medium · high")).toBeInTheDocument();
		// A model without effort levels shows nothing in their place.
		expect(within(list).queryByText("No effort control")).toBeNull();
		// Context remains visible; duplicate IDs and input types are omitted.
		expect(within(list).getAllByText(/Context/)).toHaveLength(1);
		expect(within(list).queryByText(/Inputs/)).toBeNull();
		const unreported = within(list).getByText("gpt-5.3-mini").closest("li") as HTMLElement;
		expect(within(unreported).queryByText(/Context|Inputs|Unknown/)).toBeNull();
		expect(within(page).queryByText("Defaults for new sessions")).toBeNull();
		expect(within(page).queryByRole("button", { name: "Default model" })).toBeNull();
		expect(within(page).queryByRole("button", { name: "Save defaults" })).toBeNull();
		expect(within(list).getByText("Context (tokens): 200,000")).toBeInTheDocument();
		expect(within(page).queryByRole("button", { name: /Refresh/ })).toBeNull();
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/models/refresh", { params: { path: { agent: "codex" }, query: { projectId: undefined, revalidate: undefined, catalogOnly: true } } });
	});


	it.each([
		["memory", "success"], ["memory", "failure"], ["database", "success"], ["database", "failure"],
	])("renders %s cached models before background refresh completes (%s)", async (source, outcome) => {
		mockInstalledOperations("npm");
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const key = ["agent-models", "codex", "", "catalog"];
		const cached = { agentId: "codex", models: [{ id: "cached", label: "Cached model" }] };
		if (source === "memory") queryClient.setQueryData(key, cached);
		const get = vi.mocked(apiClient.GET).getMockImplementation()!;
		vi.mocked(apiClient.GET).mockImplementation(async (path, options) => path === "/api/v1/agents/{agent}/models"
			? { data: cached } as never : (get as (path: string, options: unknown) => Promise<never>)(path, options));
		let resolveRefresh!: (value: unknown) => void;
		let rejectRefresh!: (reason: Error) => void;
		const pending = new Promise((resolve, reject) => { resolveRefresh = resolve; rejectRefresh = reject; });
		const post = vi.mocked(apiClient.POST).getMockImplementation()!;
		vi.mocked(apiClient.POST).mockImplementation(async (path, options) => path === "/api/v1/agents/{agent}/models/refresh"
			? await pending as never : (post as (path: string, options: unknown) => Promise<never>)(path, options));
		renderSection(undefined, undefined, queryClient);
		const page = await findAgentRow("codex");
		const refreshCalls = () => vi.mocked(apiClient.POST).mock.calls.filter((call: readonly unknown[]) => call[0] === "/api/v1/agents/{agent}/models/refresh");
		await waitFor(() => expect(refreshCalls()).toHaveLength(1));
		// Opening Account already requests refresh; Models never waits for it.
		await userEvent.click(within(page).getByRole("tab", { name: "Models" }));
		expect(await within(page).findByText("Cached model")).toBeInTheDocument();
		if (source === "database") expect(apiClient.GET).toHaveBeenCalledWith("/api/v1/agents/{agent}/models", {
			params: { path: { agent: "codex" }, query: { projectId: undefined, catalogOnly: true } },
		});
		expect(within(page).queryByText("Loading models…")).toBeNull();
		await act(async () => {
			if (outcome === "success") resolveRefresh({ data: { agentId: "codex", models: [{ id: "fresh", label: "Fresh model" }] } });
			else rejectRefresh(new Error("Refresh unavailable"));
		});
		expect(await within(page).findByText(outcome === "success" ? "Fresh model" : "Cached model")).toBeInTheDocument();
		backToList();
		await findAgentRow("codex");
		await waitFor(() => expect(refreshCalls()).toHaveLength(2));
	});

	it("shows cached models at once and picks up the background refresh", async () => {
		mockInstalledOperations("npm");
		let reads = 0;
		const get = vi.mocked(apiClient.GET).getMockImplementation()!;
		vi.mocked(apiClient.GET).mockImplementation(async (path, options) => {
			if (path !== "/api/v1/agents/{agent}/models") return (get as (path: string, options: unknown) => Promise<never>)(path, options);
			reads += 1;
			// The daemon serves its cache while it refreshes, then the new catalog.
			return { data: reads === 1
				? { agentId: "codex", refreshState: "refreshing", models: [{ id: "gpt-cached", label: "GPT Cached", isDefault: true }] }
				: { agentId: "codex", models: [{ id: "gpt-fresh", label: "GPT Fresh", isDefault: true }] } } as never;
		});
		renderSection();
		const page = await findAgentRow("codex");
		await userEvent.click(within(page).getByRole("tab", { name: "Models" }));
		expect(await within(page).findByText("GPT Cached")).toBeInTheDocument();
		expect(within(page).queryByText("Loading models…")).toBeNull();
		expect(await within(page).findByText("GPT Fresh", undefined, { timeout: 6_000 })).toBeInTheDocument();
	}, 15_000);

	it("pages Claude models in New Task order without folding", async () => {
		mockInstalledOperations();
		const models = { agentId: "claude-code", models: [
			{ id: "claude-opus-4-8", label: "Claude Opus 4.8" },
			...Array.from({ length: 6 }, (_, i) => ({ id: `claude-opus-4-${i}`, label: `Claude Opus 4.${i}` })),
			{ id: "claude-haiku-5-5", label: "Claude Haiku 5.5" },
			{ id: "claude-fable-5", label: "Claude Fable 5" },
			{ id: "claude-sonnet-5-5", label: "Claude Sonnet 5.5" },
			{ id: "claude-opus-5-5", label: "Claude Opus 5.5", isDefault: true },
			{ id: "claude-fable-5-1", label: "Claude Fable 5.1" },
		] };
		const get = vi.mocked(apiClient.GET).getMockImplementation()!;
		vi.mocked(apiClient.GET).mockImplementation(async (path, options) => path === "/api/v1/agents/{agent}/models"
			? { data: models } as never : (get as (path: string, options: unknown) => Promise<never>)(path, options));
		const post = vi.mocked(apiClient.POST).getMockImplementation()!;
		vi.mocked(apiClient.POST).mockImplementation(async (path, options) => path === "/api/v1/agents/{agent}/models/refresh"
			? { data: models } as never : (post as (path: string, options: unknown) => Promise<never>)(path, options));
		renderSection();
		const page = await findAgentRow("claude-code");
		await userEvent.click(within(page).getByRole("tab", { name: "Models" }));
		const list = await within(page).findByRole("list", { name: "Available models" });
		const labels = () => within(list).getAllByRole("listitem").map((row) => row.textContent);
		expect(labels()).toEqual([
			"Fable 5.1", "Opus 5.5Default", "Sonnet 5.5", "Haiku 5.5", "Fable 5", "Opus 4.8",
			"Opus 4.5", "Opus 4.4", "Opus 4.3", "Opus 4.2",
		]);
		expect(within(page).queryByRole("button", { name: "Other models" })).toBeNull();
		expect(within(page).getByText("1–10 of 12")).toBeInTheDocument();
		await userEvent.click(within(page).getByRole("button", { name: "Next models" }));
		expect(labels()).toEqual(["Opus 4.1", "Opus 4.0"]);
		expect(within(page).getByText("11–12 of 12")).toBeInTheDocument();
		expect(within(page).getByRole("button", { name: "Next models" })).toBeDisabled();
	});

	it("pages through more than ten models", async () => {
		mockInstalledOperations("npm");
		const models = { agentId: "codex", models: Array.from({ length: 12 }, (_, index) => ({ id: `model-${index + 1}`, label: `Model ${index + 1}`, efforts: [] })) };
		const get = vi.mocked(apiClient.GET).getMockImplementation()!;
		vi.mocked(apiClient.GET).mockImplementation(async (path, options) => path === "/api/v1/agents/{agent}/models"
			? { data: models } as never : (get as (path: string, options: unknown) => Promise<never>)(path, options));
		renderSection();
		const page = await findAgentRow("codex");
		await userEvent.click(within(page).getByRole("tab", { name: "Models" }));
		const list = await within(page).findByRole("list", { name: "Available models" });
		expect(within(list).getAllByRole("listitem")).toHaveLength(10);
		expect(within(page).getByText("1–10 of 12")).toBeInTheDocument();
		expect(within(page).getByRole("button", { name: "Previous models" })).toBeDisabled();
		await userEvent.click(within(page).getByRole("button", { name: "Next models" }));
		expect(within(list).getAllByRole("listitem")).toHaveLength(2);
		expect(within(list).getByText("Model 11")).toBeInTheDocument();
		expect(within(page).getByText("11–12 of 12")).toBeInTheDocument();
		expect(within(page).getByRole("button", { name: "Next models" })).toBeDisabled();
	});

	it("shows every model in pickers until one is switched off", async () => {
		const patch = mockModelSwitches([]);
		const page = await openModels();
		for (const label of ["GPT A", "GPT B", "GPT C"]) expect(within(page).getByRole("switch", { name: `Show ${label} in model pickers` })).toBeChecked();
		expect(within(page).getByText("3 of 3 shown in model pickers")).toBeInTheDocument();
		await userEvent.click(within(page).getByRole("switch", { name: "Show GPT B in model pickers" }));
		await waitFor(() => expect(patch).toHaveBeenCalledWith("/api/v1/settings/harness-defaults/{agent}", { params: { path: { agent: "codex" } }, body: { disabledModels: ["gpt-b"] } }));
		expect(within(page).getByRole("switch", { name: "Show GPT B in model pickers" })).not.toBeChecked();
		expect(await within(page).findByText("2 of 3 shown in model pickers")).toBeInTheDocument();
	});

	it("ignores saved models the catalog no longer lists, and a list that switches off every model", async () => {
		for (const disabled of [["retired-model"], ["gpt-a", "gpt-b", "gpt-c"]]) {
			mockModelSwitches(disabled);
			const page = await openModels();
			expect(await within(page).findByText("3 of 3 shown in model pickers")).toBeInTheDocument();
			for (const label of ["GPT A", "GPT B", "GPT C"]) expect(within(page).getByRole("switch", { name: `Show ${label} in model pickers` })).toBeChecked();
			cleanup();
		}
	});

	it("saves Enable all as an empty list and keeps the default model on for Disable all", async () => {
		const patch = mockModelSwitches(["gpt-a", "gpt-c"]);
		const page = await openModels();
		expect(await within(page).findByText("1 of 3 shown in model pickers")).toBeInTheDocument();
		// The last model left on cannot be switched off.
		expect(within(page).getByRole("switch", { name: "Show GPT B in model pickers" })).toBeDisabled();
		await userEvent.click(within(page).getByRole("button", { name: "Enable all" }));
		await waitFor(() => expect(patch).toHaveBeenLastCalledWith("/api/v1/settings/harness-defaults/{agent}", { params: { path: { agent: "codex" } }, body: { disabledModels: [] } }));
		expect(await within(page).findByText("3 of 3 shown in model pickers")).toBeInTheDocument();
		expect(within(page).getByRole("button", { name: "Enable all" })).toBeDisabled();
		await userEvent.click(within(page).getByRole("button", { name: "Disable all" }));
		await waitFor(() => expect(patch).toHaveBeenLastCalledWith("/api/v1/settings/harness-defaults/{agent}", { params: { path: { agent: "codex" } }, body: { disabledModels: ["gpt-b", "gpt-c"] } }));
		expect(await within(page).findByText("1 of 3 shown in model pickers")).toBeInTheDocument();
		expect(within(page).getByRole("switch", { name: "Show GPT A in model pickers" })).toBeChecked();
		expect(within(page).getByRole("switch", { name: "Show GPT A in model pickers" })).toBeDisabled();
		expect(within(page).getByRole("button", { name: "Disable all" })).toBeDisabled();
	});

	it("switches off every provider's models with Disable all, keeping the one on in view", async () => {
		const patch = mockModelSwitches(["gpt-a"], [
			{ id: "gpt-a", label: "GPT A", isDefault: true, provider: "openai" },
			{ id: "gpt-b", label: "GPT B", provider: "openai" },
			{ id: "claude-x", label: "Claude X", provider: "anthropic" },
		]);
		const page = await openModels();
		expect(await within(page).findByText("2 of 3 shown in model pickers")).toBeInTheDocument();
		await userEvent.click(within(page).getByRole("button", { name: "Disable all" }));
		await waitFor(() => expect(patch).toHaveBeenLastCalledWith("/api/v1/settings/harness-defaults/{agent}", { params: { path: { agent: "codex" } }, body: { disabledModels: ["gpt-a", "claude-x"] } }));
		expect(await within(page).findByText("1 of 3 shown in model pickers")).toBeInTheDocument();
		expect(within(page).getByRole("switch", { name: "Show GPT B in model pickers" })).toBeChecked();
	});

	it("filters catalog by provider and resets pagination on provider change", async () => {
		mockInstalledOperations("npm");
		const models = {
			agentId: "codex", providers: ["alpha", "beta", "empty"], configuredProviders: ["alpha"], models: [
				...Array.from({ length: 12 }, (_, i) => ({ id: `alpha/model-${i}`, label: `Alpha ${i}`, provider: "alpha" })),
				{ id: "beta/org/model", label: "Beta model", provider: "beta" },
				{ id: "legacy/model", label: "Legacy model" },
			],
		};
		const get = vi.mocked(apiClient.GET).getMockImplementation()!;
		vi.mocked(apiClient.GET).mockImplementation(async (path, options) => path === "/api/v1/agents/{agent}/models"
			? { data: models } as never : (get as (path: string, options: unknown) => Promise<never>)(path, options));
		renderSection();
		const page = await findAgentRow("codex");
		await userEvent.click(within(page).getByRole("tab", { name: "Models" }));
		const list = await within(page).findByRole("list", { name: "Available models" });
		expect(within(page).getByRole("button", { name: "Provider" })).toHaveTextContent("alpha · 12 · Configured");
		expect(within(list).queryByText("Beta model")).toBeNull();
		await userEvent.click(within(page).getByRole("button", { name: "Next models" }));
		expect(within(list).getByText("Alpha 10")).toBeInTheDocument();
		await userEvent.click(within(page).getByRole("button", { name: "Provider" }));
		await userEvent.click(screen.getByRole("menuitem", { name: "beta · 1" }));
		expect(within(list).getByText("Beta model")).toBeInTheDocument();
		expect(within(page).queryByRole("button", { name: "Next models" })).toBeNull();
		await userEvent.click(within(page).getByRole("button", { name: "Provider" }));
		await userEvent.click(screen.getByRole("menuitem", { name: "Other models · 1" }));
		expect(within(list).getByText("Legacy model")).toBeInTheDocument();
		await userEvent.click(within(page).getByRole("button", { name: "Provider" }));
		await userEvent.click(screen.getByRole("menuitem", { name: "alpha · 12 · Configured" }));
		expect(within(list).getByText("Alpha 0")).toBeInTheDocument();
		expect(within(page).getByText("1–10 of 12")).toBeInTheDocument();
		// A reported provider with no models has nothing to choose, so it is not offered.
		await userEvent.click(within(page).getByRole("button", { name: "Provider" }));
		expect(screen.queryByRole("menuitem", { name: "empty · 0" })).toBeNull();
	});

	it("asks for login before refreshing models", async () => {
		const readiness = readyCatalog();
		readiness.agents[1].authentication.state = "unauthorized";
		mockInstalledOperations("npm", readiness);
		renderSection();
		const page = await findAgentRow("codex");
		await userEvent.click(within(page).getByRole("tab", { name: "Models" }));
		// No saved list here, so readiness explains the empty panel. The saved list
		// may be read without readiness; refreshing it waits for login.
		expect(within(page).getByText("Log in to Codex to load its models.")).toBeInTheDocument();
		expect((vi.mocked(apiClient.POST).mock.calls as unknown[][]).some(([path]) => path === "/api/v1/agents/{agent}/models/refresh")).toBe(false);
		await userEvent.click(within(page).getByRole("button", { name: "Account" }));
		expect(within(page).getByRole("tab", { name: "Account" })).toHaveAttribute("aria-selected", "true");
	});

	it("shows read-only health, re-checks on request, and copies a health report", async () => {
		mockInstalledOperations("npm");
		renderSection();
		const page = await findAgentRow("codex");
		await userEvent.click(within(page).getByRole("tab", { name: "Health" }));
		expect(await within(page).findByText("v1.2.3")).toBeInTheDocument();
		// One version row: the installed release with the newer one beside it.
		expect(within(page).getAllByText("v1.3.0 available").length).toBeGreaterThan(0);
		expect(within(page).queryByText("Latest version")).toBeNull();
		expect(within(page).getByText("npm")).toBeInTheDocument();
		expect(within(page).getByText("Authentication")).toBeInTheDocument();
		expect(within(page).getByText("Executable")).toBeInTheDocument();
		expect(within(page).getByText("~/.local/bin/codex")).toBeInTheDocument();
		// Health never changes the harness: no install, update or login here.
		for (const name of ["Install", "Update", "Login"]) expect(within(page).queryByRole("button", { name })).toBeNull();
		await userEvent.click(within(page).getByRole("button", { name: "Check again" }));
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/probe", { params: { path: { agent: "codex" } } });
		await userEvent.click(within(page).getByRole("button", { name: "Copy diagnostics" }));
		await waitFor(() => expect(window.tallpine!.clipboard.writeText).toHaveBeenCalledWith(expect.stringContaining("Codex health\nHost: local\nInstallation: installed")));
		expect(window.tallpine!.clipboard.writeText).toHaveBeenCalledWith(expect.stringContaining("Latest version: 1.3.0"));
	});

	it("keeps auth attempt details in the report instead of the Health UI", async () => {
		const readiness = readyCatalog();
		Object.assign(readiness.agents[1].authentication, {
			state: "authorized", freshness: "stale", reasonCode: "auth_check_timeout",
			reason: "Authentication check timed out.",
			checkedAt: "2026-10-10T10:00:00Z", attemptedAt: "2026-10-10T11:00:00Z",
		});
		mockInstalledOperations("npm", readiness);
		renderSection();
		const page = await findAgentRow("codex");
		await userEvent.click(within(page).getByRole("tab", { name: "Health" }));
		expect(within(page).queryByText("Authentication check timed out.")).toBeNull();
		expect(within(page).queryByText("Last completed auth check")).toBeNull();
		expect(within(page).queryByText("Latest auth attempt")).toBeNull();
		expect(within(page).queryByText("Update checked")).toBeNull();
		await userEvent.click(within(page).getByRole("button", { name: "Copy diagnostics" }));
		await waitFor(() => expect(window.tallpine!.clipboard.writeText).toHaveBeenCalled());
		const report = vi.mocked(window.tallpine!.clipboard.writeText).mock.calls.at(-1)![0];
		expect(report).toContain("Authentication reason code: auth_check_timeout");
		expect(report).toContain("Authentication freshness: stale");
		expect(report).toContain("Authentication checked: 2026-10-10T10:00:00Z");
		expect(report).toContain("Authentication attempted: 2026-10-10T11:00:00Z");
	});

	it("shows a login the harness renews by itself as connected and copies its provider evidence", async () => {
		const readiness = readyCatalog();
		Object.assign(readiness.agents[1].authentication, {
			state: "configured", reason: "Credentials are configured but not verified.",
			diagnostics: [{ method: "provider_validation", code: "token_renewal_pending", source: "keychain", provider: "firstParty",
				reason: "The stored token needs renewal.", checkedAt: "2026-10-10T10:00:00Z", cached: false, durationMs: 0 }],
		});
		mockInstalledOperations("npm", readiness);
		renderSection();
		const page = await findAgentRow("codex");
		await userEvent.click(within(page).getByRole("tab", { name: "Health" }));
		expect(within(page).getByText("Connected · token renews on next run")).toBeInTheDocument();
		expect(within(page).queryByText("Credentials not verified")).toBeNull();
		expect(within(page).getByText("Detected credential")).toBeInTheDocument();
		expect(within(page).getByText("keychain")).toBeInTheDocument();
		expect(within(page).queryByText("The stored token needs renewal.")).toBeNull();
		await userEvent.click(within(page).getByRole("button", { name: "Copy diagnostics" }));
		await waitFor(() => expect(window.tallpine!.clipboard.writeText).toHaveBeenCalled());
		const report = vi.mocked(window.tallpine!.clipboard.writeText).mock.calls.at(-1)![0];
		expect(report).toContain("Source: keychain");
		expect(report).toContain("Checks use Tallpine's process environment on the selected host.");
		expect(within(page).queryByText(/An inconclusive check does not mean you are signed out/)).toBeNull();
		expect(report).toContain("Auth check: provider_validation (token_renewal_pending)");
		expect(report).not.toContain("Next step:");
	});

	it("shows the credential the readiness check selected when the provider rejects it", async () => {
		const readiness = readyCatalog();
		Object.assign(readiness.agents[1].authentication, {
			state: "unauthorized", account: undefined,
			diagnostics: [{ method: "selected_credential", code: "credential_present", source: "OPENAI_API_KEY",
				checkedAt: "2026-10-10T10:00:00Z", cached: false, durationMs: 0 }],
		});
		mockInstalledOperations("npm", readiness);
		renderSection();
		const page = await findAgentRow("codex");
		await userEvent.click(within(page).getByRole("tab", { name: "Health" }));
		expect(within(page).getByText("Detected credential")).toBeInTheDocument();
		expect(within(page).getByText("OPENAI_API_KEY")).toBeInTheDocument();
	});

	it.each([
		{ kind: "file", source: "~/.pi/agent/auth.json", expected: "~/.pi/agent/auth.json" },
		{ kind: "environment", source: "OPENAI_API_KEY", expected: "OPENAI_API_KEY" },
		{ kind: "keychain", source: "", expected: "System keychain" },
		{ kind: "command", source: "codex login status", expected: undefined },
	])("shows only the detected credential for a $kind source", async ({ kind, source, expected }) => {
		const readiness = readyCatalog();
		Object.assign(readiness.agents[1].authentication, {
			account: { sourceKind: kind, source },
			diagnostics: [{ method: "credential_store", code: "credential_file_missing", source: "~/.missing/auth.json",
				reason: "No file at this location.", checkedAt: "2026-10-10T10:00:00Z", cached: false, durationMs: 0 }],
		});
		mockInstalledOperations("npm", readiness);
		renderSection();
		const page = await findAgentRow("codex");
		await userEvent.click(within(page).getByRole("tab", { name: "Health" }));
		if (expected) expect(within(page).getByText(expected)).toBeInTheDocument();
		else expect(within(page).queryByText("Detected credential")).toBeNull();
		expect(within(page).queryByText("~/.missing/auth.json")).toBeNull();
		expect(within(page).queryByText("codex login status")).toBeNull();
		await userEvent.click(within(page).getByRole("button", { name: "Copy diagnostics" }));
		await waitFor(() => expect(window.tallpine!.clipboard.writeText).toHaveBeenCalledWith(expect.stringContaining("Source: ~/.missing/auth.json")));
	});

	it("retains a failed auth request's correlation facts without copying the server message", async () => {
		mockInstalledOperations("npm");
		renderSection();
		const page = await findAgentRow("codex");
		await userEvent.click(within(page).getByRole("tab", { name: "Health" }));
		const post = vi.mocked(apiClient.POST).getMockImplementation()!;
		vi.mocked(apiClient.POST).mockImplementation(async (path, options) => path === "/api/v1/agents/{agent}/probe"
			? { error: { code: "AGENT_CHECK_FAILED", requestId: "request-123", message: "secret-token" }, response: { status: 503 } } as never
			: (post as (path: string, options: unknown) => Promise<never>)(path, options));
		await userEvent.click(within(page).getByRole("button", { name: "Check again" }));
		expect(await within(page).findByRole("alert")).toHaveTextContent("Could not complete the check.");
		expect(within(page).queryByText(/AGENT_CHECK_FAILED/)).toBeNull();
		await userEvent.click(within(page).getByRole("button", { name: "Copy diagnostics" }));
		await waitFor(() => expect(window.tallpine!.clipboard.writeText).toHaveBeenCalled());
		const report = vi.mocked(window.tallpine!.clipboard.writeText).mock.calls.at(-1)![0];
		expect(report).toContain("HTTP 503");
		expect(report).toContain("request request-123");
		expect(report).not.toContain("secret-token");
		expect(report).toContain("Authentication: authorized");
	});

	it.each(["unauthorized", "configured"])("confirms logout and rechecks readiness without assuming success: %s", async (authStatus) => {
		const readiness = readyCatalog();
		mockInstalledOperations("npm", readiness);
		const post = vi.mocked(apiClient.POST).getMockImplementation()!;
		vi.mocked(apiClient.POST).mockImplementation(async (path, options) => {
			if (path === "/api/v1/agents/{agent}/logout") return { data: { agentId: "codex", action: "logout", terminal: { handleId: "logout-codex", title: "Log out of Codex", createdAt: "2026-10-09T00:00:00Z", workingDir: "/tmp" } } } as never;
			if (path === "/api/v1/agents/{agent}/probe") {
				readiness.agents.find(agent => agent.id === "codex")!.authentication.state = authStatus;
				return { data: { agent: { id: "codex", authStatus } } } as never;
			}
			return (post as (path: string, options: unknown) => Promise<never>)(path, options);
		});
		vi.spyOn(apiClient, "DELETE").mockResolvedValue({ data: {} } as never);
		renderSection();
		const page = await findAgentRow("codex");
		await userEvent.click(within(page).getByRole("button", { name: "Log out" }));
		const dialog = screen.getByRole("dialog", { name: "Log out of Codex?" });
		expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/logout", expect.anything());
		await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
		expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/logout", expect.anything());
		await userEvent.click(within(page).getByRole("button", { name: "Log out" }));
		await userEvent.click(within(screen.getByRole("dialog", { name: "Log out of Codex?" })).getByRole("button", { name: "Log out" }));
		expect(await within(page).findByText("Log out of Codex")).toBeInTheDocument();
		// A short-lived logout must bypass the retained shell cache, which prunes
		// exited handles before the panel can attach and observe completion.
		expect(terminalMuxFactory.value).toBeTypeOf("function");
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/logout", { params: { path: { agent: "codex" } } });
		expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/auth", expect.anything());
		await userEvent.click(within(page).getByRole("button", { name: "Complete login terminal" }));
		if (authStatus === "configured") {
			await within(page).findByText("Logout could not be confirmed. The harness may still have another account or an API key configured.", {}, { timeout: 7000 });
			expect(within(page).queryByRole("button", { name: "Login" })).toBeNull();
			return;
		}
		await waitFor(() => expect(within(page).queryByTestId("harness-auth-terminal")).toBeNull());
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/probe", { params: { path: { agent: "codex" } } });
		expect(within(page).getByRole("button", { name: "Login" })).toBeEnabled();
	}, 10000);

	it("offers to log in again for a signed-in harness", async () => {
		mockInstalledOperations("npm");
		const post = vi.mocked(apiClient.POST).getMockImplementation()!;
		vi.mocked(apiClient.POST).mockImplementation(async (path, options) => path === "/api/v1/agents/{agent}/auth" ? { data: {
			agentId: "codex", action: "login", terminal: { handleId: "auth-codex", workingDir: "/tmp", title: "Codex login", createdAt: "2026-10-07T00:00:00Z" },
		} } as never : (post as (path: string, options: unknown) => Promise<never>)(path, options));
		renderSection();
		const page = await findAgentRow("codex");
		expect(await within(page).findByText("Connected")).toBeInTheDocument();
		await userEvent.click(await within(page).findByRole("button", { name: "Log in again" }));
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/auth", { params: { path: { agent: "codex" } } });
		expect(await screen.findByRole("button", { name: "Complete login terminal" })).toBeInTheDocument();
	});


	it("checks updates from initial readiness without waiting for the page refresh", async () => {
		mockInstalledOperations("npm");
		let resolveRefresh!: (value: unknown) => void;
		const pendingRefresh = new Promise((resolve) => { resolveRefresh = resolve; });
		vi.mocked(apiClient.POST).mockReturnValueOnce(pendingRefresh as never);
		renderSection();
		const row = await findAgentRow("codex");
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/refresh"));
		expect(await within(row).findByRole("button", { name: "Update" })).toBeEnabled();
		expect((vi.mocked(apiClient.GET).mock.calls as unknown[][]).some(([path]) => path === "/api/v1/agents/{agent}/update-advisory")).toBe(true);
		await act(async () => resolveRefresh({ data: readyCatalog() }));
		expect(await within(row).findByRole("button", { name: "Update" })).toBeEnabled();
	});

	it.each(["current", "failed"])("shows cached Update immediately while revalidating, then handles a %s result", async (result) => {
		mockInstalledOperations("npm");
		let resolveRefresh!: (value: unknown) => void;
		let resolveAdvisory!: (value: unknown) => void;
		let rejectAdvisory!: (reason: Error) => void;
		const pendingRefresh = new Promise((resolve) => { resolveRefresh = resolve; });
		const pendingAdvisory = new Promise((resolve, reject) => { resolveAdvisory = resolve; rejectAdvisory = reject; });
		vi.mocked(apiClient.POST).mockReturnValueOnce(pendingRefresh as never);
		const original = vi.mocked(apiClient.GET).getMockImplementation()!;
		vi.mocked(apiClient.GET).mockImplementation((path, options) => {
			if (path === "/api/v1/agents/{agent}/update-advisory") return pendingAdvisory as never;
			return (original as (path: string, options: unknown) => Promise<never>)(path, options);
		});
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		client.setQueryData(["agent-readiness"], readyCatalog());
		client.setQueryData(["agent-update-advisory", "local", "codex"], {
			agentId: "codex", status: "behind_latest", currentVersion: "1.2.3", latestVersion: "1.3.0", maintenanceMethod: "npm",
		});
		const view = renderSection(undefined, undefined, client);
		const row = await findAgentRow("codex");
		expect(await within(row).findByRole("button", { name: "Update" })).toBeEnabled();
		expect(within(row).getByText("v1.3.0 available")).toBeInTheDocument();
		expect((vi.mocked(apiClient.GET).mock.calls as unknown[][]).some(([path, options]) => path === "/api/v1/agents/{agent}/update-advisory"
			&& (options as { params?: { path?: { agent?: string } } })?.params?.path?.agent === "codex")).toBe(false);
		await act(async () => resolveRefresh({ data: readyCatalog() }));
		await waitFor(() => expect((vi.mocked(apiClient.GET).mock.calls as unknown[][]).some(([path, options]) => path === "/api/v1/agents/{agent}/update-advisory"
			&& (options as { params?: { path?: { agent?: string } } })?.params?.path?.agent === "codex")).toBe(true));
		expect(within(row).getByRole("button", { name: "Update" })).toBeEnabled();
		if (result === "current") {
			await act(async () => resolveAdvisory({ data: { agentId: "codex", status: "current", currentVersion: "1.3.0", latestVersion: "1.3.0", maintenanceMethod: "npm" } }));
			await waitFor(() => expect(within(row).queryByRole("button", { name: "Update" })).toBeNull());
			expect(within(row).queryByText("v1.3.0 available")).toBeNull();
		} else {
			await act(async () => rejectAdvisory(new Error("offline")));
			await waitFor(() => expect(client.getQueryState(["agent-update-advisory", "local", "codex"])?.status).toBe("error"));
			expect(within(row).getByRole("button", { name: "Update" })).toBeEnabled();
			expect(within(row).getByText("v1.3.0 available")).toBeInTheDocument();
		}
		view.unmount();
	});

	it("keeps cached Login and Install usable while page readiness refreshes", async () => {
		mockInstalledOperations("npm");
		vi.mocked(apiClient.POST).mockReturnValueOnce(new Promise(() => {}) as never);
		const readiness = catalogWithInstalled("codex");
		readiness.agents[1].authentication.state = "unauthorized";
		const original = vi.mocked(apiClient.GET).getMockImplementation()!;
		vi.mocked(apiClient.GET).mockImplementation((path, options) => path === "/api/v1/agents/readiness"
			? Promise.resolve({ data: readiness }) as never : (original as (path: string, options: unknown) => Promise<never>)(path, options));
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		client.setQueryData(["agent-readiness"], readiness);
		const view = renderSection(undefined, undefined, client);
		const codexRow = await findAgentRow("codex");
		expect(await within(codexRow).findByRole("button", { name: "Login" })).toBeEnabled();
		const claudeRow = await findAgentRow("claude-code");
		expect(await within(claudeRow).findByRole("button", { name: "Install" })).toBeEnabled();
		view.unmount();
	});

	it.each(["unknown", "configured"])("does not force Login before an update for %s auth", async (authentication) => {
		const readiness = readyCatalog();
		readiness.agents[1].authentication.state = authentication;
		mockInstalledOperations("npm", readiness);
		renderSection();
		const row = await findAgentRow("codex");
		expect(await within(row).findByRole("button", { name: "Update" })).toBeEnabled();
		expect(within(row).queryByRole("button", { name: "Login" })).toBeNull();
		// A configured credential reads as connected; an unknown one claims nothing.
		if (authentication === "configured") expect(within(row).getByText("Connected")).toBeInTheDocument();
		else expect(within(row).queryByText("Connected")).toBeNull();
	});

	it.each(["future_status", ""])("keeps an observed version without claiming an update for status %s", async (status) => {
		mockInstalledOperations("npm");
		const { client } = renderSection();
		const listRow = await findListRow("codex");
		await within(listRow).findByRole("img", { name: "v1.3.0 available" });
		act(() => { client.setQueryData(["agent-update-advisory", "local", "codex"], {
			agentId: "codex", status, currentVersion: "2025.09.25", source: "npm", checkedAt: "2026-10-06T00:00:00Z",
		}); });
		await waitFor(() => expect(within(listRow).queryByRole("img")).toBeNull());
		const row = await findAgentRow("codex");
		expect(within(row).getByText("2025.09.25")).toBeInTheDocument();
		expect(within(row).queryByText(/available$/)).toBeNull();
		expect(within(row).queryByRole("button", { name: "Update" })).toBeNull();
		expect(within(row).getByText("Update availability could not be verified.")).toBeInTheDocument();
	});

	it("uses the detected manager rather than a stale saved method", async () => {
		mockInstalledOperations("unsupported-old-method");
		const get = vi.mocked(apiClient.GET).getMockImplementation()!;
		vi.mocked(apiClient.GET).mockImplementation(async (path, options) => {
			if (path === "/api/v1/agents/{agent}/update-advisory" && (options as { params?: { query?: { refresh?: boolean } } })?.params?.query?.refresh) return { data: { agentId: "codex", status: "behind_latest", currentVersion: "1.2.3", latestVersion: "1.3.0", source: "homebrew" } } as never;
			return (get as (path: string, options: unknown) => Promise<never>)(path, options);
		});
		const { client } = renderSection();
		await within(await findListRow("codex")).findByRole("img", { name: "v1.3.0 available" });
		act(() => { client.setQueryData(["agent-update-advisory", "local", "codex"], {
			agentId: "codex", status: "behind_latest", currentVersion: "1.2.3", latestVersion: "1.3.0", source: "homebrew",
		}); });
		const row = await findAgentRow("codex");
		await waitFor(() => expect(within(row).getByRole("button", { name: "Update" })).toBeEnabled());
		await userEvent.click(within(row).getByRole("button", { name: "Update" }));
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", { params: { path: { agent: "codex" } }, body: { method: "homebrew", operation: "update", expectedVersion: "1.3.0" } });
		expect(within(row).getByRole("status")).toHaveTextContent("Updating");
		expect(screen.queryByRole("dialog")).toBeNull();
	});

	it.each(["already-current", "different-owner", "unknown"])("does not run an update when the click-time probe reports %s", async (state) => {
		mockInstalledOperations("npm");
		const get = vi.mocked(apiClient.GET).getMockImplementation()!;
		vi.mocked(apiClient.GET).mockImplementation(async (path, options) => {
			if (path === "/api/v1/agents/{agent}/update-advisory" && (options as { params?: { query?: { refresh?: boolean } } })?.params?.query?.refresh) return { data: {
				agentId: "codex", status: state === "already-current" ? "current" : state === "unknown" ? "unknown" : "behind_latest",
				currentVersion: state === "already-current" ? "1.3.0" : "1.2.3", latestVersion: "1.3.0", maintenanceMethod: state === "different-owner" ? "homebrew" : "npm",
			} } as never;
			return (get as (path: string, options: unknown) => Promise<never>)(path, options);
		});
		renderSection();
		await userEvent.click(await within(await findAgentRow("codex")).findByRole("button", { name: "Update" }));
		await waitFor(() => expect(screen.queryByRole("button", { name: "Updating…" })).toBeNull());
		expect(apiClient.GET).toHaveBeenCalledWith("/api/v1/agents/{agent}/update-advisory", { params: { path: { agent: "codex" }, query: { refresh: true } } });
		expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/install", expect.anything());
	});

	it("installs the newer release the click-time check found through the same method", async () => {
		mockInstalledOperations("npm");
		const get = vi.mocked(apiClient.GET).getMockImplementation()!;
		vi.mocked(apiClient.GET).mockImplementation(async (path, options) => {
			if (path === "/api/v1/agents/{agent}/update-advisory" && (options as { params?: { query?: { refresh?: boolean } } })?.params?.query?.refresh) return { data: {
				agentId: "codex", status: "behind_latest", currentVersion: "1.2.3", latestVersion: "2.0.0", maintenanceMethod: "npm",
			} } as never;
			return (get as (path: string, options: unknown) => Promise<never>)(path, options);
		});
		renderSection();
		const row = await findAgentRow("codex");
		await userEvent.click(await within(row).findByRole("button", { name: "Update" }));
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", { params: { path: { agent: "codex" } }, body: { method: "npm", operation: "update", expectedVersion: "2.0.0" } }));
		expect(within(row).queryByText(/Select Update again/)).toBeNull();
	});

	it("does not let saved job history override missing current ownership evidence", async () => {
		mockInstalledOperations("npm");
		const get = vi.mocked(apiClient.GET).getMockImplementation()!;
		vi.mocked(apiClient.GET).mockImplementation(async (path, options) => {
			if (path === "/api/v1/agents/{agent}/update-advisory") return { data: { agentId: "codex", status: "behind_latest", currentVersion: "1.2.3", latestVersion: "1.3.0", source: "official-release" } } as never;
			return (get as (path: string, options: unknown) => Promise<never>)(path, options);
		});
		renderSection();
		const row = await findAgentRow("codex");
		expect(await within(row).findByRole("button", { name: "Update manually" })).toBeEnabled();
		expect(within(row).queryByRole("button", { name: "Update" })).toBeNull();
	});

	it("shows update progress on the page and in the list while it runs", async () => {
		mockInstalledOperations("npm");
		renderSection();
		const row = await findAgentRow("codex");
		await userEvent.click(await within(row).findByRole("button", { name: "Update" }));
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", { params: { path: { agent: "codex" } }, body: { method: "npm", operation: "update", expectedVersion: "1.3.0" } });
		expect(within(row).getByRole("status")).toHaveTextContent("Updating");
		expect(within(row).getByRole("button", { name: "Updating…" })).toBeDisabled();
		expect(within(row).queryByRole("button", { name: "Show diagnostics" })).toBeNull();
		backToList();
		expect(within(await findListRow("codex")).getByRole("status")).toHaveTextContent("Updating…");
	});

	it("does not guess ownership or offer a method picker for a manual installation", async () => {
		mockInstalledOperations();
		renderSection();
		const row = await findAgentRow("codex");
		expect(within(row).queryByRole("button", { name: "Update" })).toBeNull();
		const openExternal = vi.spyOn(tallpineBridge.app, "openExternal").mockResolvedValue(undefined);
		await userEvent.click(within(row).getByRole("button", { name: "Update manually" }));
		expect(openExternal).toHaveBeenCalledWith("https://github.com/openai/codex");
		expect(within(row).getByText("Installation method could not be verified. Manage this CLI using its original installer.")).toBeInTheDocument();
		expect(screen.queryByRole("dialog")).toBeNull();
		expect(within(row).queryByRole("combobox")).toBeNull();
		expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/install", expect.anything());
	});

	it("never substitutes an available manager for an unsupported saved method", async () => {
		mockInstalledOperations("official-installer");
		renderSection();
		const row = await findAgentRow("codex");
		expect(within(row).queryByRole("button", { name: "Update" })).toBeNull();
		expect(within(row).getByRole("button", { name: "Update manually" })).toBeEnabled();
		expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/install", expect.anything());
	});

	it("does not ask a harness with not-applicable authentication to log in before updating", async () => {
		const readiness = readyCatalog();
		readiness.agents[1].authentication.state = "not_applicable";
		mockInstalledOperations("npm", readiness);
		renderSection();
		const row = await findAgentRow("codex");
		expect(await within(row).findByRole("button", { name: "Update" })).toBeEnabled();
		expect(within(row).queryByRole("button", { name: "Login" })).toBeNull();
	});

	it("reopens details for a daemon rejection and retries the same update", async () => {
		mockInstalledOperations("npm");
		const post = vi.mocked(apiClient.POST);
		const previous = post.getMockImplementation()!;
		post.mockImplementation(async (path, options) => {
			if (path === "/api/v1/agents/{agent}/install") return { error: { error: "HARNESS_ACTIVE", code: "HARNESS_ACTIVE", message: "End the active Codex session before updating.", requestId: "test-active-session" } } as never;
			return (previous as (path: string, options: unknown) => Promise<never>)(path, options);
		});
		renderSection();
		const row = await findAgentRow("codex");
		await userEvent.click(await within(row).findByRole("button", { name: "Update" }));
		expect(await within(row).findByRole("alert")).toHaveTextContent("End the active Codex session before updating.");
		expect(within(row).getByRole("tab", { name: "Account" })).toHaveAttribute("aria-selected", "true");
		await userEvent.click(within(row).getByRole("button", { name: "Retry update" }));
		const requests = (vi.mocked(apiClient.POST).mock.calls as unknown[][]).filter(([path]) => path === "/api/v1/agents/{agent}/install");
		expect(requests).toHaveLength(2);
		for (const [, options] of requests) expect(options).toMatchObject({ body: { method: "npm", operation: "update" } });
	});

	it("updates without asking when the installer replaces the running release", async () => {
		mockInstalledOperations("npm", readyCatalog(), { liveSessions: 2, sessionIds: ["s1", "s2"], level: "warn" });
		renderSection();
		const row = await findAgentRow("codex");
		await userEvent.click(await within(row).findByRole("button", { name: "Update" }));
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", {
			params: { path: { agent: "codex" } },
			body: { method: "npm", operation: "update", expectedVersion: "1.3.0" },
		}));
		expect(screen.queryByRole("dialog")).toBeNull();
	});

	it("updates without asking when the installer keeps the running release", async () => {
		mockInstalledOperations("npm", readyCatalog(), { liveSessions: 2, sessionIds: ["s1", "s2"], level: "safe" });
		renderSection();
		const row = await findAgentRow("codex");
		await userEvent.click(await within(row).findByRole("button", { name: "Update" }));
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", {
			params: { path: { agent: "codex" } },
			body: { method: "npm", operation: "update", expectedVersion: "1.3.0" },
		}));
		expect(screen.queryByRole("dialog")).toBeNull();
	});

	it("turns Update into View sessions when the sessions must end first", async () => {
		mockInstalledOperations("npm", readyCatalog(), { liveSessions: 2, sessionIds: ["s1", "s2"], level: "blocked" });
		renderSection();
		const row = await findAgentRow("codex");
		await userEvent.click(await within(row).findByRole("button", { name: "View sessions (2)" }));
		expect(await screen.findByRole("dialog", { name: "Sessions using Codex" })).toHaveTextContent("2 open sessions are using Codex.");
		expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/install", expect.anything());
	});

	it("lists the sessions instead of offering Retry when the daemon refuses for them", async () => {
		mockInstalledOperations("npm");
		const post = vi.mocked(apiClient.POST);
		const previous = post.getMockImplementation()!;
		post.mockImplementation(async (path, options) => {
			if (path === "/api/v1/agents/{agent}/install") return { error: { error: "conflict", code: "HARNESS_ACTIVE", message: "end active sessions", requestId: "r1", details: { count: 1, sessionIds: ["s1"], reviewerSessionIds: [], level: "blocked", starting: false } } } as never;
			return (previous as (path: string, options: unknown) => Promise<never>)(path, options);
		});
		renderSection();
		const row = await findAgentRow("codex");
		await userEvent.click(await within(row).findByRole("button", { name: "Update" }));
		expect(await screen.findByRole("dialog", { name: "Sessions using Codex" })).toBeInTheDocument();
		expect(within(row).queryByRole("button", { name: "Retry update" })).toBeNull();
		expect(within(row).queryByRole("alert")).toBeNull();
	});

	it("prioritizes login and never auto-updates", async () => {
		const readiness = catalogWithInstalled("codex");
		readiness.agents[1].authentication.state = "unauthorized";
		mockInstalledOperations("npm", readiness);
		let probed = false;
		let resolveClose!: (value: unknown) => void;
		const closing = new Promise((resolve) => { resolveClose = resolve; });
		const post = vi.mocked(apiClient.POST);
		const previous = post.getMockImplementation()!;
		post.mockImplementation(async (path, options) => {
			if (path === "/api/v1/agents/{agent}/auth") return { data: {
				agentId: "codex", action: "login", terminal: { handleId: "auth-codex", workingDir: "/tmp", title: "Codex login", createdAt: "2026-10-07T00:00:00Z" },
			} } as never;
			if (path === "/api/v1/agents/{agent}/probe") {
				probed = true;
				return { data: { agent: { id: "codex", authStatus: "authorized" }, installed: true } } as never;
			}
			if (path === "/api/v1/agents/readiness/ensure" && probed) return { data: {
				agents: [agentReadiness("codex", "Codex", { authentication: "authorized" })],
			} } as never;
			return (previous as (path: string, options: unknown) => Promise<never>)(path, options);
		});
		vi.spyOn(apiClient, "DELETE").mockImplementation(async () => await closing as never);
		Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: vi.fn() });
		renderSection();
		const row = await findAgentRow("codex");
		expect(await within(row).findByText("v1.3.0 available")).toBeInTheDocument();
		expect(within(row).queryByRole("button", { name: "Update" })).toBeNull();
		await userEvent.click(within(row).getByRole("button", { name: "Login" }));
		expect(within(row).getByRole("tab", { name: "Account" })).toHaveAttribute("aria-selected", "true");
		await userEvent.click(await screen.findByRole("button", { name: "Complete login terminal" }));
		await waitFor(() => expect(apiClient.DELETE).toHaveBeenCalled());
		expect(within(row).queryByRole("button", { name: "Update" })).toBeNull();
		await act(async () => { resolveClose({ data: undefined }); });
		await waitFor(() => expect(within(row).getByRole("button", { name: "Update" })).toBeEnabled());
		expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/install", expect.anything());
	});

	it("records only a successful update observed here, not job or check timestamps", async () => {
		mockInstalledOperations("npm");
		const { client } = renderSection();
		const row = await findAgentRow("codex");
		await userEvent.click(await within(row).findByRole("button", { name: "Update" }));
		const completed = {
			target: "codex", method: "npm", status: "succeeded", startedAt: "2026-09-27T12:00:00Z",
			finishedAt: "2026-10-07T12:00:00Z", updatedAt: "2026-10-08T12:00:00Z", output: "Update completed",
		};
		const get = vi.mocked(apiClient.GET);
		const previous = get.getMockImplementation()!;
		get.mockImplementation(async (path, options) => path === "/api/v1/agents/install-jobs" ? { data: { jobs: [completed] } } as never : (previous as (path: string, options: unknown) => Promise<never>)(path, options));
		act(() => { client.setQueryData(["agent-install-jobs"], [completed]); });
		await waitFor(() => expect(row.querySelector("time")).toHaveAttribute("datetime", "2026-10-07T12:00:00Z"));
		expect(within(row).queryByRole("button", { name: "Show diagnostics" })).toBeNull();
	});
});
