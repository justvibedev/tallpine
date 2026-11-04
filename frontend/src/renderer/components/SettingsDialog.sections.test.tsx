import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import type { ProjectSettingsSaveState } from "./ProjectSettingsForm";
import { projectSettingsSections } from "./project-settings/projectSections";
import { SettingsDialog } from "./SettingsPageTestHarness";

const { postMock, cloudProjectsState, localWorkspacesState } = vi.hoisted(() => ({
	postMock: vi.fn(),
	cloudProjectsState: {
		data: [] as Array<{ id: string; orgId?: string; displayName: string }> | undefined,
		isLoading: false,
		isError: false,
		error: null as Error | null,
		refetch: vi.fn(),
	},
	localWorkspacesState: { ids: [] as string[] },
}));

const accountsResponse = {
	accountRevision: 0,
	accounts: [],
	capabilities: {},
	deviceReconciliation: { status: "verified", activeAccountVerified: false, reasonCode: "verified", retryable: false },
};

vi.mock("../lib/api-client", () => ({
	apiClient: { POST: postMock },
	apiErrorCode: (error: { code?: string }) => error?.code,
	apiErrorMessage: () => "request failed",
	hasTrustedApiBaseUrl: () => true,
}));

const formSubmitted = vi.hoisted(() => vi.fn());
const scriptsSubmitted = vi.hoisted(() => vi.fn());

vi.mock("./ProjectSettingsForm", () => ({
	ProjectSettingsForm: ({
		cloudOrgId,
		onSaveState,
	}: {
		cloudOrgId?: string;
		onSaveState?: (state: ProjectSettingsSaveState) => void;
	}) => (
		<>
			{cloudOrgId ? <div data-testid="cloud-project-settings">{cloudOrgId}</div> : null}
			<button
				type="button"
				onClick={() =>
					onSaveState?.({
						phase: "pending",
					})
				}
			>
				Start pending save
			</button>
			<button
				type="button"
				onClick={() =>
					onSaveState?.({
						phase: "failed",
						error: "Display name must be 100 characters or fewer",
					})
				}
			>
				Trigger failed save
			</button>
			<form id="project-settings-form" onSubmit={(event) => { event.preventDefault(); formSubmitted(); }} />
			<button type="button" onClick={() => onSaveState?.({ phase: "failed", dirty: true, draftFailed: true, error: "bad" })}>
				Failed draft unchanged
			</button>
			<button type="button" onClick={() => onSaveState?.({ phase: "failed", dirty: true, draftFailed: false, error: "bad" })}>
				Failed draft edited
			</button>
			<button type="button" onClick={() => onSaveState?.({ phase: "saved" })}>
				Complete save
			</button>
		</>
	),
}));

vi.mock("./GlobalSettingsForm", () => ({
	GlobalSettingsForm: ({ focusAgentId, hostId, section }: { focusAgentId?: string; hostId?: string; section: string }) => (
		<div data-focus-agent={focusAgentId} data-host={hostId} data-testid="global-settings-section">{section}</div>
	),
}));

// Cloud projects come from the control plane; none by default, so the
// local-daemon form stays in play unless a test seeds one.
vi.mock("../hooks/useWorkspaceQuery", () => ({
	useCloudProjectsQuery: () => cloudProjectsState,
	workspaceQueryOptions: {
		queryKey: ["workspaces"],
		queryFn: () => Promise.resolve(localWorkspacesState.ids.map((id: string) => ({ id }))),
	},
}));

vi.mock("./CuesDialog", () => ({
	CuesSettings: ({ projectId, onBusyChange }: { projectId: string; onBusyChange?: (busy: boolean) => void }) => (
		<div data-testid="project-cues-settings">{projectId}<button onClick={() => onBusyChange?.(true)} type="button">Make cues busy</button></div>
	),
}));

vi.mock("./ProjectScriptsSettings", () => ({
	ProjectScriptsSettings: ({ projectId, formId, onSaveState }: { projectId: string; formId?: string; onSaveState?: (state: ProjectSettingsSaveState) => void }) => (
		<form id={formId ?? "project-settings-form"} data-testid="project-scripts-settings" onSubmit={(event) => { event.preventDefault(); scriptsSubmitted(); }}>{projectId}
			<button type="button" onClick={() => onSaveState?.({ phase: "pending", dirty: true })}>Make scripts dirty</button>
		</form>
	),
	CloudProjectScriptsSettings: ({ projectId, cloudOrgId }: { projectId: string; cloudOrgId: string }) => <div data-testid="cloud-project-scripts-settings">{cloudOrgId}/{projectId}</div>,
}));

// The dialog reads the cloud gate to decide whether the Cloud nav page exists;
// mocked so these tests need no QueryClientProvider (same pattern as Sidebar).

// The dialog reads the org's offering to gate the Coder and Upgrade pages.
// Nothing resolved here (signed out), so neither page is visible.

describe("SettingsDialog", () => {
	beforeEach(() => {
		postMock.mockReset().mockImplementation((path: string) => path === "/api/v1/agents/codex/accounts/ensure"
			? Promise.resolve({ data: accountsResponse })
			: Promise.resolve({ data: { operationId: "login-1", status: "cancelled" } }));
		useUiStore.setState({ settingsModal: null });
		cloudProjectsState.data = [];
		cloudProjectsState.isLoading = false;
		cloudProjectsState.isError = false;
		cloudProjectsState.error = null;
		cloudProjectsState.refetch.mockReset();
		localWorkspacesState.ids = [];
	});

	function renderSettingsDialog() {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		return render(<QueryClientProvider client={queryClient}><SettingsDialog /></QueryClientProvider>);
	}

	it("flushes a dirty setup-commands draft on close without clashing with the project form id", async () => {
		useUiStore.getState().openProjectSettings("proj-1", { section: "repository" });
		renderSettingsDialog();
		await userEvent.click(await screen.findByRole("button", { name: "Make scripts dirty" }));
		expect(document.querySelectorAll("#project-settings-form").length).toBeLessThanOrEqual(1);
		await userEvent.click(screen.getByRole("button", { name: "Close settings" }));
		expect(scriptsSubmitted).toHaveBeenCalledTimes(1);
	});

	it("closing with only the main form dirty does not submit the clean setup-commands editor", async () => {
		scriptsSubmitted.mockClear();
		formSubmitted.mockClear();
		useUiStore.getState().openProjectSettings("proj-1", { section: "repository" });
		renderSettingsDialog();
		await userEvent.click(await screen.findByRole("button", { name: "Failed draft edited" }));
		await userEvent.click(screen.getByRole("button", { name: "Close settings" }));
		expect(formSubmitted).toHaveBeenCalledTimes(1);
		expect(scriptsSubmitted).not.toHaveBeenCalled();
	});

	it("omits Reviews & PRs for scratch projects", () => {
		const names = (scratch: boolean) => projectSettingsSections(((k: string) => k) as never, false, scratch).map((s) => s.id);
		expect(names(false)).toContain("reviews");
		expect(names(true)).not.toContain("reviews");
	});

	it("lists the local project sections in order", async () => {
		useUiStore.getState().openProjectSettings("proj-1");
		renderSettingsDialog();
		const names = ["Details", "Repository & worktrees", "Agents", "Reviews & PRs", "Workflow", "Sessions", "Security", "Advanced"];
		const buttons = await Promise.all(names.map((name) => screen.findByRole("button", { name })));
		buttons.slice(1).forEach((button, i) => expect(buttons[i].compareDocumentPosition(button) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy());
	});
});
