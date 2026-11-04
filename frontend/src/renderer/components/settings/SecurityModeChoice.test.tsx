import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import { AgentPolicySection } from "./AgentPolicySection";
import { ProjectSecurityModeSwitch } from "./ProjectSecurityModeSwitch";

const { get, patch } = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn() }));
vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: get, PATCH: patch },
	apiErrorMessage: (error: unknown) => (error instanceof Error ? error.message : "request failed"),
}));

type Sw = { value: boolean; source?: string; locked?: boolean };
const boolField = (name: string, labelKey: string, sw: Sw) => ({ name, kind: "bool", labelKey, descriptionKey: labelKey, default: false, value: sw.value, source: sw.source ?? "default", locked: sw.locked ?? false, projectOverride: "any" });
const enumField = (name: string, value: string, def: string, source = "default") => ({ name, kind: "enum", enum: [def, value], labelKey: "settings.agentPolicy.cap.label", descriptionKey: "settings.agentPolicy.cap.label", default: def, value, source, locked: false, projectOverride: "tightenOnly" });

function view(opts: { security?: Sw; dev?: Sw; profile?: string; extra?: unknown[] } = {}) {
	const security = opts.security ?? { value: false };
	const dev = opts.dev ?? { value: false };
	return {
		schemaVersion: 2,
		profile: opts.profile,
		rules: [
			{ name: "securityMode", titleKey: "t", fields: [boolField("enabled", "settings.agentPolicy.securityMode.label", security)] },
			{ name: "devMode", titleKey: "t", fields: [boolField("enabled", "settings.agentPolicy.devMode.label", dev)] },
			{ name: "protectState", titleKey: "t", fields: [enumField("mode", "on", "off")] },
			{ name: "orchestratorApprovals", titleKey: "t", fields: [boolField("enabled", "settings.agentPolicy.orchestratorApprovals.label", { value: false })] },
			{ name: "crossProjectOrchestration", titleKey: "t", fields: [boolField("enabled", "settings.agentPolicy.crossProjectOrchestration.label", { value: false })] },
			{ name: "repoAgentConfig", titleKey: "t", fields: [boolField("enabled", "settings.agentPolicy.repoAgentConfig.label", { value: true })] },
			...(opts.extra ?? []),
		],
		securityMode: { enabled: security.value, source: security.source ?? "default", locked: security.locked ?? false },
		managedPath: "",
		warnings: [],
	};
}

function wrap(ui: React.ReactElement) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}
const radio = (name: RegExp) => screen.findByRole("radio", { name });
const body = (rules: unknown) => ["/api/v1/settings/agent-policy", { body: { rules } }];

beforeEach(async () => {
	await appI18n.changeLanguage("en");
	get.mockReset();
	patch.mockReset();
	patch.mockResolvedValue({ data: {} });
});

describe("Security tab mode choice", () => {
	it("renders three modes with plain copy and no internal names", async () => {
		get.mockResolvedValue({ data: view() });
		wrap(<AgentPolicySection scope="security" />);
		expect(await radio(/^Standard/)).toBeChecked();
		expect(await radio(/^Protected/)).not.toBeChecked();
		expect(await radio(/^Unrestricted/)).not.toBeChecked();
		const group = screen.getByTestId("security-mode-choice");
		expect(group).not.toHaveTextContent(/devMode|Dev mode|protectState|envFilter/i);
	});

	it.each([
		["Protected", { securityMode: { enabled: true }, devMode: { enabled: null } }],
		["Standard", { securityMode: { enabled: null }, devMode: { enabled: null } }],
	])("writes %s in one PATCH", async (name, rules) => {
		get.mockResolvedValue({ data: view(name === "Standard" ? { security: { value: true } } : {}) });
		wrap(<AgentPolicySection scope="security" />);
		const target = await radio(new RegExp(`^${name}`));
		await waitFor(() => expect(target).toBeEnabled());
		await userEvent.click(target);
		expect(patch).toHaveBeenCalledTimes(1);
		expect(patch).toHaveBeenCalledWith(...body(rules));
	});

	it("asks for confirmation before Unrestricted, then writes dev on and security reset", async () => {
		get.mockResolvedValue({ data: view({ security: { value: true } }) });
		wrap(<AgentPolicySection scope="security" />);
		const target = await radio(/^Unrestricted/);
		await waitFor(() => expect(target).toBeEnabled());
		await userEvent.click(target);
		expect(patch).not.toHaveBeenCalled();
		await userEvent.click(await screen.findByRole("button", { name: "Turn on Unrestricted" }));
		expect(patch).toHaveBeenCalledWith(...body({ securityMode: { enabled: null }, devMode: { enabled: true } }));
	});

	it("keeps Standard and Protected selectable when only Dev mode is locked off", async () => {
		get.mockResolvedValue({ data: view({ dev: { value: false, source: "managed", locked: true } }) });
		wrap(<AgentPolicySection scope="security" />);
		await waitFor(async () => expect(await radio(/^Protected/)).toBeEnabled());
		expect(await radio(/^Unrestricted/)).toBeDisabled();
		expect(screen.getByTestId("agent-policy-lock")).toHaveTextContent(/Managed by your organization/);
	});

	it("names the Dev mode variable when the environment locks Dev mode", async () => {
		get.mockResolvedValue({ data: view({ dev: { value: false, source: "env", locked: true } }) });
		wrap(<AgentPolicySection scope="security" />);
		expect(await screen.findByTestId("agent-policy-lock")).toHaveTextContent("TALLPINE_AGENT_DEV_MODE_ENABLED");
	});

	it("shows Protected locked under the hardened profile", async () => {
		get.mockResolvedValue({ data: view({ profile: "hardened", security: { value: true, source: "managed_profile", locked: true }, dev: { value: false, source: "managed_profile", locked: true } }) });
		wrap(<AgentPolicySection scope="security" />);
		expect(await radio(/^Protected/)).toBeChecked();
		expect(await radio(/^Standard/)).toBeDisabled();
		expect(await radio(/^Unrestricted/)).toBeDisabled();
	});

	it("shows the pick-one notice for a stored both-on combination", async () => {
		get.mockResolvedValue({ data: view({ security: { value: true }, dev: { value: true } }) });
		wrap(<AgentPolicySection scope="security" />);
		expect(await screen.findByTestId("security-mode-conflict")).toHaveTextContent(/Pick one/i);
		expect(await radio(/^Unrestricted/)).toBeChecked();
	});

	it("lists protections set outside the tab as one read-only line, without rows", async () => {
		get.mockResolvedValue({ data: view({ extra: [{ name: "envFilter", titleKey: "t", fields: [enumField("mode", "allowlist", "inherit", "user")] }] }) });
		wrap(<AgentPolicySection scope="security" />);
		expect(await screen.findByTestId("security-custom-protections")).toHaveTextContent(/agent environment/);
		expect(screen.queryByRole("button", { name: /highest agent permission/i })).toBeNull();
		expect(screen.queryByText(/protectState|State fence/i, { selector: "label,span,button" })).toBeNull();
	});

	it("omits the line when no protection is customised", async () => {
		get.mockResolvedValue({ data: view() });
		wrap(<AgentPolicySection scope="security" />);
		await radio(/^Standard/);
		expect(screen.queryByTestId("security-custom-protections")).toBeNull();
	});

	it("groups orchestrator and load rows, and disables the orchestrator group under Unrestricted", async () => {
		get.mockResolvedValue({ data: view({ dev: { value: true } }) });
		wrap(<AgentPolicySection scope="security" />);
		const orch = await screen.findByRole("group", { name: "What orchestrators may do" });
		const switches = within(orch).getAllByRole("switch");
		expect(switches).toHaveLength(2);
		for (const s of switches) expect(s).toBeDisabled();
		const load = screen.getByRole("group", { name: "What agents may load" });
		await waitFor(() => expect(within(load).getByRole("switch")).toBeEnabled());
	});
});

describe("Project Security mode under Unrestricted", () => {
	it("disables the project row with a note", async () => {
		get.mockResolvedValue({ data: view({ dev: { value: true } }) });
		wrap(<ProjectSecurityModeSwitch projectId="p1" />);
		expect(await screen.findByTestId("project-security-unrestricted")).toBeVisible();
		expect(screen.getByRole("button", { name: "Security mode" })).toBeDisabled();
	});

	it("keeps the row enabled otherwise", async () => {
		get.mockResolvedValue({ data: view() });
		wrap(<ProjectSecurityModeSwitch projectId="p1" />);
		await waitFor(() => expect(screen.getByRole("button", { name: "Security mode" })).toBeEnabled());
		expect(screen.queryByTestId("project-security-unrestricted")).toBeNull();
	});
});
