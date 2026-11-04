import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import { AccountPoolSection } from "./AccountPoolSection";
import { AgentPolicySection } from "./AgentPolicySection";

const { get, patch } = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn() }));

vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: get, PATCH: patch },
	apiErrorMessage: (error: unknown) => (error instanceof Error ? error.message : "request failed"),
}));

const placement = {
	name: "placement",
	kind: "enum",
	enum: ["default", "random", "most-headroom", "least-used"],
	labelKey: "settings.accountPool.placement.label",
	descriptionKey: "settings.accountPool.placement.description",
	optionLabelKeys: {
		default: "settings.accountPool.placement.default",
		random: "settings.accountPool.placement.random",
		"most-headroom": "settings.accountPool.placement.mostHeadroom",
		"least-used": "settings.accountPool.placement.leastUsed",
	},
	default: "default",
	value: "default",
	source: "default",
	locked: false,
	projectOverride: "never",
};
const failover = {
	name: "failover",
	kind: "bool",
	labelKey: "settings.accountPool.failover.label",
	descriptionKey: "settings.accountPool.failover.description",
	default: false,
	value: false,
	source: "default",
	locked: false,
	projectOverride: "never",
};
const cap = {
	name: "max",
	kind: "enum",
	enum: ["none", "auto"],
	labelKey: "settings.agentPolicy.cap.label",
	descriptionKey: "settings.agentPolicy.cap.description",
	optionLabelKeys: { none: "settings.agentPolicy.cap.none", auto: "settings.agentPolicy.cap.auto" },
	default: "none",
	value: "none",
	source: "default",
	locked: false,
	projectOverride: "tightenOnly",
};

const view = (pool: Record<string, unknown>[] = [placement, failover]) => ({
	schemaVersion: 2,
	rules: [
		{ name: "toyCap", titleKey: "settings.agentPolicy.title", fields: [cap] },
		{ name: "accountPool", titleKey: "settings.accountPool.title", fields: pool },
	],
	managedPath: "",
	warnings: [],
});

function renderUi(ui: React.ReactNode) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

beforeEach(async () => {
	await appI18n.changeLanguage("en");
	get.mockReset();
	patch.mockReset();
});

describe("AccountPoolSection", () => {
	it("shows both controls off by default, with the provider-terms note", async () => {
		get.mockResolvedValue({ data: view() });
		renderUi(<AccountPoolSection />);
		expect(await screen.findByRole("button", { name: /new session account/i })).toBeEnabled();
		expect(screen.getByRole("switch", { name: /switch account at a usage limit/i })).not.toBeChecked();
		expect(screen.getByText(/may conflict with the provider's terms/i)).toBeInTheDocument();
	});

	it("sends the chosen placement and the failover switch", async () => {
		get.mockResolvedValue({ data: view() });
		patch.mockResolvedValue({ data: view() });
		renderUi(<AccountPoolSection />);
		const menu = await screen.findByRole("button", { name: /new session account/i });
		await waitFor(() => expect(menu).toBeEnabled());
		await userEvent.click(menu);
		await userEvent.click(await screen.findByRole("menuitem", { name: "Most headroom" }));
		await waitFor(() =>
			expect(patch).toHaveBeenCalledWith("/api/v1/settings/agent-policy", { body: { rules: { accountPool: { placement: "most-headroom" } } } }),
		);
		await userEvent.click(screen.getByRole("switch", { name: /switch account at a usage limit/i }));
		await waitFor(() =>
			expect(patch).toHaveBeenCalledWith("/api/v1/settings/agent-policy", { body: { rules: { accountPool: { failover: true } } } }),
		);
	});

	it("locks a setting an administrator manages", async () => {
		get.mockResolvedValue({ data: view([{ ...placement, value: "least-used", source: "managed", locked: true }, failover]) });
		renderUi(<AccountPoolSection />);
		expect(await screen.findByTestId("agent-policy-lock")).toHaveTextContent("Managed by your organization");
		expect(screen.getByRole("button", { name: /new session account/i })).toBeDisabled();
	});

	it("renders nothing for a daemon without the rule", async () => {
		get.mockResolvedValue({ data: { ...view(), rules: view().rules.slice(0, 1) } });
		const { container } = renderUi(<AccountPoolSection />);
		await waitFor(() => expect(get).toHaveBeenCalled());
		expect(container).toBeEmptyDOMElement();
	});

	it("keeps the pool rows out of the Agent policy section", async () => {
		get.mockResolvedValue({ data: view() });
		renderUi(<AgentPolicySection />);
		expect(await screen.findByRole("button", { name: /highest agent permission/i })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /new session account/i })).toBeNull();
	});
});
