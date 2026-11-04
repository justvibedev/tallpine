import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { globalSettingsItem, visibleGlobalSettings } from "./settingsCatalog";

vi.mock("./CodexAccountsSection", () => ({ CodexAccountsSection: () => <div data-testid="codex-accounts" /> }));
vi.mock("./AgentAccountsSection", () => ({ AgentAccountsPanel: ({ provider }: { provider: string }) => <div data-testid="agent-accounts" data-provider={provider} /> }));
vi.mock("./AccountPoolSection", () => ({ AccountPoolSection: () => <div data-testid="account-pool" /> }));
vi.mock("./HarnessSettingsSection", () => ({ HarnessSettingsSection: () => <div data-testid="harness" /> }));
vi.mock("./AgentPolicySection", () => ({ AgentPolicySection: ({ scope }: { scope?: string }) => <div data-testid="policy" data-scope={scope ?? "general"} /> }));
vi.mock("./ReviewsSettingsSection", () => ({ ReviewsSettingsSection: () => <div data-testid="reviews-section" /> }));
vi.mock("./MobileDevicesSection", () => ({ MobileDevicesSection: () => null }));
vi.mock("./ConnectMobileContent", () => ({ ConnectMobileContent: () => null }));
vi.mock("./RemoteHostsSettings", () => ({ RemoteHostsSettings: () => <div data-testid="remote-hosts" /> }));

const t = ((k: string) => k) as never;
const context = { diagnostics: false };

describe("settings catalog", () => {
	it("shows the Accounts page with the account pool mounted exactly once, with no developer flag", () => {
		const item = globalSettingsItem("agents", context);
		expect(item.visible?.(context) ?? true).toBe(true);
		expect(item.label(((k: string) => k) as never)).toBe("settings.agents");
		render(<>{item.render(t, false, context)}</>);
		expect(screen.getByTestId("codex-accounts")).toBeInTheDocument();
		expect(screen.getByTestId("agent-accounts")).toHaveAttribute("data-provider", "claude");
		expect(screen.getAllByTestId("account-pool")).toHaveLength(1);
	});

	it("keeps the account pool off the Agents page and puts the planner locks on the Reviews page", () => {
		render(<>{globalSettingsItem("harness", context).render(t, false, context)}</>);
		expect(screen.queryByTestId("account-pool")).not.toBeInTheDocument();
		expect(screen.getAllByTestId("policy").map((el) => el.getAttribute("data-scope"))).toEqual(["general"]);
	});

	it("renders the Reviews page with the planning section on it", () => {
		const item = globalSettingsItem("reviews", context);
		expect(item.label(t)).toBe("settings.reviews");
		render(<>{item.render(t, false, context)}</>);
		expect(screen.getByTestId("reviews-section")).toBeInTheDocument();
	});

	it("shows other computers on Connections and hides Diagnostics only by its own toggle", () => {
		render(<>{globalSettingsItem("connections", context).render(t, false, context)}</>);
		expect(screen.getByTestId("remote-hosts")).toBeInTheDocument();
		expect(visibleGlobalSettings(context).map((item) => item.id)).not.toContain("diagnostics");
		expect(visibleGlobalSettings({ diagnostics: true }).map((item) => item.id)).toContain("diagnostics");
	});
});
