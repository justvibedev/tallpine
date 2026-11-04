import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import { useUiStore } from "../../stores/ui-store";
import { SessionFenceGapLine } from "../SessionInspector/SessionFenceGapLine";
import { ProjectSecurityModeSwitch } from "./ProjectSecurityModeSwitch";

const { get, patch } = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn() }));
vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: get, PATCH: patch },
	apiErrorMessage: (error: unknown) => (error instanceof Error ? error.message : "request failed"),
}));

const field = (name: string, value: unknown, source = "default", locked = false) => ({
	name,
	kind: "bool",
	labelKey: name === "enabled" ? "settings.agentPolicy.securityMode.label" : "settings.agentPolicy.securityMode.allowDocker",
	descriptionKey: "settings.agentPolicy.securityMode.description",
	default: name === "enabled",
	value,
	source,
	locked,
	projectOverride: "any",
});

const view = (enabled: { value: boolean; source?: string; locked?: boolean }, allowDocker = false) => ({
	schemaVersion: 2,
	rules: [{ name: "securityMode", titleKey: "settings.agentPolicy.title", fields: [field("enabled", enabled.value, enabled.source, enabled.locked), field("allowDocker", allowDocker)] }],
	securityMode: { enabled: enabled.value, source: enabled.source ?? "default", locked: enabled.locked ?? false },
	managedPath: "",
	warnings: [],
});

function wrap(ui: React.ReactElement) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

beforeEach(async () => {
	await appI18n.changeLanguage("en");
	get.mockReset();
	patch.mockReset();
	patch.mockResolvedValue({ data: {} });
});

describe("Project Security mode switch", () => {
	it("shows inherit when the project sets nothing and writes on, off and null", async () => {
		get.mockResolvedValue({ data: view({ value: true }) });
		wrap(<ProjectSecurityModeSwitch projectId="p1" />);
		const menu = await screen.findByRole("button", { name: "Security mode" });
		expect(menu).toHaveTextContent("Use the app setting");
		await userEvent.click(menu);
		await userEvent.click(await screen.findByRole("menuitem", { name: "Off" }));
		expect(patch).toHaveBeenLastCalledWith("/api/v1/projects/{id}/agent-policy", { params: { path: { id: "p1" } }, body: { rules: { securityMode: { enabled: false } } } });
		await userEvent.click(menu);
		await userEvent.click(await screen.findByRole("menuitem", { name: "On" }));
		expect(patch).toHaveBeenLastCalledWith("/api/v1/projects/{id}/agent-policy", { params: { path: { id: "p1" } }, body: { rules: { securityMode: { enabled: true } } } });
	});

	it("shows a project's own off and can reset it to inherit", async () => {
		get.mockResolvedValue({ data: view({ value: false, source: "project" }) });
		wrap(<ProjectSecurityModeSwitch projectId="p1" />);
		const menu = await screen.findByRole("button", { name: "Security mode" });
		expect(menu).toHaveTextContent("Off");
		await userEvent.click(menu);
		await userEvent.click(await screen.findByRole("menuitem", { name: "Use the app setting" }));
		expect(patch).toHaveBeenLastCalledWith("/api/v1/projects/{id}/agent-policy", { params: { path: { id: "p1" } }, body: { rules: { securityMode: { enabled: null } } } });
	});

	it("warns that Docker gives full host access only while it is allowed", async () => {
		get.mockResolvedValue({ data: view({ value: true }, true) });
		const first = wrap(<ProjectSecurityModeSwitch projectId="p1" />);
		expect(await screen.findByRole("alert")).toHaveTextContent(/full access to this computer/);
		first.unmount();
		get.mockResolvedValue({ data: view({ value: true }, false) });
		wrap(<ProjectSecurityModeSwitch projectId="p1" />);
		await screen.findByRole("switch", { name: "Allow Docker" });
		expect(screen.queryByRole("alert")).toBeNull();
	});
});

describe("Session fence gap line", () => {
	it("shows the reason and opens the project's settings", async () => {
		const openProjectSettings = vi.fn();
		useUiStore.setState({ openProjectSettings });
		get.mockResolvedValue({ data: { sessionId: "s1", recordedAt: "", record: { rules: { protectState: { fenceGap: "codex cannot be fenced" } } } } });
		wrap(<SessionFenceGapLine sessionId="s1" projectId="p1" />);
		expect(await screen.findByText(/Not fenced: codex cannot be fenced/)).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Change in project settings" }));
		expect(openProjectSettings).toHaveBeenCalledWith("p1");
	});

	it("renders nothing for a fenced session", async () => {
		get.mockResolvedValue({ data: { sessionId: "s1", recordedAt: "", record: { rules: { protectState: {} } } } });
		wrap(<SessionFenceGapLine sessionId="s1" projectId="p1" />);
		await waitFor(() => expect(get).toHaveBeenCalled());
		expect(screen.queryByTestId("session-fence-gap")).toBeNull();
	});
});
