import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import { AgentPolicySection } from "./AgentPolicySection";

const { get, patch } = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn() }));

vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: get, PATCH: patch },
	apiErrorMessage: (error: unknown) => (error instanceof Error ? error.message : "request failed"),
}));

const capField = {
	name: "max",
	kind: "enum",
	enum: ["none", "auto", "accept_edits"],
	labelKey: "settings.agentPolicy.cap.label",
	descriptionKey: "settings.agentPolicy.cap.description",
	optionLabelKeys: { none: "settings.agentPolicy.cap.none", auto: "settings.agentPolicy.cap.auto", accept_edits: "settings.agentPolicy.cap.acceptEdits" },
	default: "none",
	value: "none",
	source: "default",
	locked: false,
	projectOverride: "tightenOnly",
};

const view = (field: Record<string, unknown> = {}, rules: unknown[] = []) => ({
	schemaVersion: 2,
	rules: [{ name: "toyCap", titleKey: "settings.agentPolicy.title", fields: [{ ...capField, ...field }] }, ...rules],
	managedPath: "",
	warnings: [],
});

function renderSection(scope: "general" | "security" | "planning" = "general") {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<AgentPolicySection scope={scope} />
		</QueryClientProvider>,
	);
}

beforeEach(async () => {
	await appI18n.changeLanguage("en");
	get.mockReset();
	patch.mockReset();
});

describe("AgentPolicySection (metadata driven)", () => {
	it("renders the daemon's rule as an editable row with no lock", async () => {
		get.mockResolvedValue({ data: view() });
		renderSection();
		const control = await screen.findByRole("button", { name: /highest agent permission/i });
		await waitFor(() => expect(control).toBeEnabled());
		expect(screen.queryByTestId("agent-policy-lock")).toBeNull();
	});

	it("keeps security rules off the general page", async () => {
		const sec = { name: "repoAgentConfig", titleKey: "t", fields: [{ ...capField, name: "enabled", kind: "bool", labelKey: "settings.agentPolicy.repoAgentConfig.label", descriptionKey: "", default: true, value: true }] };
		get.mockResolvedValue({ data: view({}, [sec]) });
		renderSection("general");
		await screen.findByRole("button", { name: /highest agent permission/i });
		expect(screen.queryByRole("switch", { name: /repository's agent config/i })).toBeNull();
	});

	it("shows the managed lock and disables the row", async () => {
		get.mockResolvedValue({ data: view({ value: "auto", source: "managed", locked: true }) });
		renderSection();
		expect(await screen.findByTestId("agent-policy-lock")).toHaveTextContent("Managed by your organization");
		expect(screen.getByRole("button", { name: /highest agent permission/i })).toBeDisabled();
	});

	it("shows the hardened profile note and its floors as managed locks", async () => {
		get.mockResolvedValue({ data: { ...view({ value: "auto", source: "managed_profile", locked: true }), profile: "hardened" } });
		renderSection();
		expect(await screen.findByTestId("agent-policy-profile")).toHaveTextContent("Hardened profile (managed by your organization)");
		expect(screen.getByTestId("agent-policy-profile")).toHaveTextContent("next relaunch or restore");
		expect(screen.getByTestId("agent-policy-lock")).toHaveTextContent("Managed by your organization");
		expect(screen.getByRole("button", { name: /highest agent permission/i })).toBeDisabled();
	});

	it("shows no profile note without a profile", async () => {
		get.mockResolvedValue({ data: view() });
		renderSection();
		await screen.findByRole("button", { name: /highest agent permission/i });
		expect(screen.queryByTestId("agent-policy-profile")).toBeNull();
	});

	it("warns with the daemon's detail", async () => {
		get.mockResolvedValue({ data: { ...view({ value: "accept_edits", source: "managed", locked: true }), warnings: ["TALLPINE_AGENT_MAX_PERMISSION has an invalid value"] } });
		renderSection();
		expect(await screen.findByRole("alert")).toHaveTextContent("TALLPINE_AGENT_MAX_PERMISSION has an invalid value");
	});

	it("sends rule, field and value when an option is chosen", async () => {
		get.mockResolvedValue({ data: view() });
		patch.mockResolvedValue({ data: view({ value: "accept_edits", source: "user" }) });
		renderSection();
		const control = await screen.findByRole("button", { name: /highest agent permission/i });
		await waitFor(() => expect(control).toBeEnabled());
		await userEvent.click(control);
		await userEvent.click(await screen.findByRole("menuitem", { name: "Accept edits only (strict)" }));
		await waitFor(() =>
			expect(patch).toHaveBeenCalledWith("/api/v1/settings/agent-policy", { body: { rules: { toyCap: { max: "accept_edits" } } } }),
		);
	});

	it("renders a rule it has never heard of from metadata alone", async () => {
		const toy = {
			name: "toyRule",
			titleKey: "settings.agentPolicy.title",
			fields: [
				{ name: "enabled", kind: "bool", labelKey: "settings.agentPolicy.warning", descriptionKey: "", default: false, value: true, source: "user", locked: false, projectOverride: "never" },
				{ name: "names", kind: "stringList", labelKey: "settings.agentPolicy.managed", descriptionKey: "", default: [], value: ["A", "B_*"], source: "managed", locked: true, projectOverride: "never", visibleWhen: { field: "enabled", equals: "true" } },
			],
		};
		get.mockResolvedValue({ data: view({}, [toy]) });
		renderSection("general");
		expect(await screen.findByRole("switch", { name: /has a problem/i })).toBeChecked();
		expect(screen.getByText("A, B_*")).toBeInTheDocument();
	});

	describe("harness and model option sources", () => {
		const plannerRule = (harness: string) => ({
			name: "planner",
			titleKey: "settings.agentPolicy.title",
			fields: ["harness", "model"].map((name) => ({
				name,
				kind: "string",
				labelKey: `settings.agentPolicy.planner.${name}.label`,
				descriptionKey: "",
				default: "",
				value: name === "harness" ? harness : "",
				source: "default",
				locked: false,
				projectOverride: "never",
				optionSource: name,
				...(name === "model" ? { sourceField: "harness" } : {}),
			})),
		});

		it("keeps planner rows out of the general list and shows them under Planning without '(managed)'", async () => {
			get.mockResolvedValue({ data: view({}, [plannerRule("")]) });
			const general = renderSection("general");
			await screen.findByRole("heading", { name: appI18n.t("settings.agentPolicy.title") });
			expect(screen.queryByRole("button", { name: /planner agent/i })).toBeNull();
			general.unmount();
			renderSection("planning");
			expect(await screen.findByRole("button", { name: /planner agent/i })).toBeInTheDocument();
			expect(screen.getByRole("heading", { name: "Planning" })).toBeInTheDocument();
			expect(screen.queryByText(/\(managed\)/)).toBeNull();
		});

		it("renders the planner agent as a dropdown with Each project chooses first", async () => {
			get.mockResolvedValue({ data: view({}, [plannerRule("")]) });
			renderSection("planning");
			const control = await screen.findByRole("button", { name: /planner agent/i });
			await userEvent.click(control);
			const items = await screen.findAllByRole("menuitem");
			expect(items[0]).toHaveTextContent("Each project chooses");
			expect(items.map((i) => i.textContent)).toContain("Codex");
			expect(screen.queryByRole("textbox", { name: /planner agent/i })).toBeNull();
		});

		it("keeps a stored model-only lock visible and enabled without an agent", async () => {
			const rule = plannerRule("");
			rule.fields[1].value = "opus";
			get.mockResolvedValue({ data: view({}, [rule]) });
			renderSection("planning");
			const control = await screen.findByRole("button", { name: /planner model/i });
			expect(control).toHaveTextContent("opus");
			expect(control).toBeEnabled();
		});

		it("offers the locked agent's own models", async () => {
			const catalogs: Record<string, string[]> = { codex: ["gpt-5.5", "gpt-5.4"], "claude-code": ["opus", "sonnet"] };
			get.mockImplementation(async (path: string, init?: { params?: { path?: { agent?: string } } }) => {
				if (path === "/api/v1/agents/{agent}/models") {
					const ids = catalogs[init?.params?.path?.agent ?? ""] ?? [];
					return { data: { models: ids.map((id) => ({ id, label: id })), selectionMode: "model" } };
				}
				return { data: view({}, [plannerRule("codex")]) };
			});
			renderSection("planning");
			await userEvent.click(await screen.findByRole("button", { name: /planner model/i }));
			const labels = (await screen.findAllByRole("menuitem")).map((i) => i.textContent);
			expect(labels[0]).toBe("Each project chooses");
			expect(labels.join(" ")).toContain("gpt-5.5");
			expect(labels.join(" ")).not.toContain("opus");
		});

		it("clears the locked model when the agent changes", async () => {
			const rule = plannerRule("claude-code");
			rule.fields[1].value = "opus";
			get.mockResolvedValue({ data: view({}, [rule]) });
			patch.mockResolvedValue({ data: view({}, [plannerRule("codex")]) });
			renderSection("planning");
			await userEvent.click(await screen.findByRole("button", { name: /planner agent/i }));
			await userEvent.click(await screen.findByRole("menuitem", { name: "Codex" }));
			await waitFor(() => expect(patch).toHaveBeenCalled());
			expect(patch.mock.calls[0][1].body).toEqual({ rules: { planner: { model: "", harness: "codex" } } });
		});

		it("disables the planner model picker until an agent is locked", async () => {
			get.mockResolvedValue({ data: view({}, [plannerRule("")]) });
			renderSection("planning");
			expect(await screen.findByRole("button", { name: /planner model/i })).toBeDisabled();
		});
	});
});
