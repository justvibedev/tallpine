import { useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { agentReadiness } from "../test/agent-readiness-fixtures";

const { getMock, putMock, postMock, navigateMock, closeSettingsMock, openGlobalSettingsMock, setOrchestratorReplacementErrorMock, ensureAgentReadinessMock, trackerIntakeGate } = vi.hoisted(() => ({
	getMock: vi.fn(),
	putMock: vi.fn(),
	postMock: vi.fn(),
	navigateMock: vi.fn(),
	closeSettingsMock: vi.fn(),
	openGlobalSettingsMock: vi.fn(),
	setOrchestratorReplacementErrorMock: vi.fn(),
	ensureAgentReadinessMock: vi.fn(),
	trackerIntakeGate: { enabled: true, planningMode: "auto", planAutoApprove: false, lock: { mode: "", autoApproveOff: false } },
}));

vi.mock("../hooks/useAgentReadinessQuery", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../hooks/useAgentReadinessQuery")>();
	return { ...actual, useEnsureAgentReadiness: ensureAgentReadinessMock };
});

vi.mock("@tanstack/react-router", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@tanstack/react-router")>();
	return {
		...actual,
		useNavigate: () => navigateMock,
	};
});

vi.mock("../stores/ui-store", () => ({
	useUiStore: (selector: (state: Record<string, unknown>) => unknown) =>
		selector({
			closeSettings: closeSettingsMock,
			openGlobalSettings: openGlobalSettingsMock,
			setOrchestratorReplacementError: setOrchestratorReplacementErrorMock,
		}),
}));


vi.mock("../hooks/useSettings", () => ({
	useSettings: () => ({ settings: { trackerIntakeEnabled: trackerIntakeGate.enabled, workflowDefaults: { autoReview: false, planningMode: trackerIntakeGate.planningMode, planAutoApprove: trackerIntakeGate.planAutoApprove } }, isLoading: false, error: undefined }),
}));
vi.mock("../hooks/usePlannerLocks", () => ({
	usePlannerLocks: () => trackerIntakeGate.lock,
}));

vi.mock("../lib/api-client", () => ({
	apiClient: {
		GET: getMock,
		PUT: putMock,
		POST: postMock,
	},
	apiErrorCode: (error: unknown) =>
		typeof error === "object" && error !== null && "code" in error
			? String((error as { code: unknown }).code)
			: undefined,
	apiErrorRequestId: (error: unknown) =>
		typeof error === "object" && error !== null && "requestId" in error
			? String((error as { requestId: unknown }).requestId)
			: undefined,
	apiErrorDetails: (error: unknown) =>
		typeof error === "object" && error !== null && "details" in error
			? (error as { details: Record<string, unknown> }).details
			: undefined,
	apiErrorMessage: (error: unknown) => {
		if (error instanceof Error) return error.message;
		if (typeof error === "object" && error !== null && "message" in error) {
			return String((error as { message: unknown }).message);
		}
		return "Request failed";
	},
}));

import { ProjectSettingsForm, type ProjectSettingsSaveState, type ProjectSettingsSection } from "./ProjectSettingsForm";
import { renderWithTooltip as render } from "../test/render-with-tooltip";

vi.mock("./settings/ProjectSecurityModeSwitch", () => ({ ProjectSecurityModeSwitch: ({ projectId }: { projectId: string }) => <div data-testid="security-mode-switch">{projectId}</div> }));

function Harness({ section }: { section: ProjectSettingsSection }) {
	const [, setSaveState] = useState<ProjectSettingsSaveState>({ phase: "idle" });
	return <ProjectSettingsForm projectId="proj-1" section={section} onSaveState={setSaveState} />;
}

function renderSection(section: ProjectSettingsSection, config: Record<string, unknown> = {}, kind = "single_repo") {
	const project = { id: "proj-1", name: "Project One", kind, path: "/repo", repo: "", defaultBranch: "main", config: { worker: { agent: "codex" }, orchestrator: { agent: "claude-code" }, ...config } };
	getMock.mockImplementation(async (path: string) => {
		if (path === "/api/v1/agents/readiness") return { data: { agents: [agentReadiness("claude-code", "Claude Code"), agentReadiness("codex", "Codex")] }, error: undefined };
		if (path === "/api/v1/agents/{agent}/models") return { data: { agentId: "x", selectionMode: "text", models: [], allowCustom: true, source: "manual", fetchedAt: "2026-07-31T00:00:00Z", stale: false }, error: undefined };
		return { data: { status: "ok", project }, error: undefined };
	});
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(<QueryClientProvider client={queryClient}><Harness section={section} /></QueryClientProvider>);
}

const submit = () => fireEvent.submit(document.getElementById("project-settings-form")!);
const savedConfig = async () => {
	await waitFor(() => expect(putMock).toHaveBeenCalled());
	return putMock.mock.calls.at(-1)?.[1]?.body.config;
};
async function choose(name: string, option: string) {
	await userEvent.click(await screen.findByRole("button", { name }));
	await userEvent.click(await screen.findByRole("menuitem", { name: option }));
}
async function typeInto(label: string, text: string) {
	const field = await screen.findByLabelText(label);
	await userEvent.clear(field);
	if (text) await userEvent.type(field, text);
}
/** Click-to-edit single-line rows: open the editor, then replace the value. */
async function typeInline(label: string, text: string) {
	await userEvent.click(await screen.findByRole("button", { name: `Edit ${label}` }));
	await typeInto(label, text);
}
const inlineValue = async (label: string) => (await screen.findByRole("button", { name: `Edit ${label}` })).textContent;

beforeEach(() => {
	getMock.mockReset();
	putMock.mockReset();
	postMock.mockReset();
	ensureAgentReadinessMock.mockReset();
	trackerIntakeGate.enabled = true;
	trackerIntakeGate.planningMode = "auto";
	trackerIntakeGate.planAutoApprove = false;
	trackerIntakeGate.lock = { mode: "", autoApproveOff: false };
	putMock.mockResolvedValue({ data: { project: {} }, error: undefined });
	postMock.mockResolvedValue({ data: { orchestrator: { id: "o" } }, error: undefined, response: { status: 200 } });
});

describe("local project settings sections", () => {
	it("repository: symlinks and clone-worktree-include load and save", async () => {
		renderSection("repository", { symlinks: ["node_modules", ".env.local"], cloneWorktreeInclude: true });
		expect(await screen.findByLabelText("Symlinked paths")).toHaveValue("node_modules\n.env.local");
		expect(screen.getByRole("switch", { name: "Clone ignored folders" })).toBeChecked();
		await typeInto("Symlinked paths", "vendor");
		await userEvent.click(screen.getByRole("switch", { name: "Clone ignored folders" }));
		submit();
		const config = await savedConfig();
		expect(config.symlinks).toEqual(["vendor"]);
		expect(config.cloneWorktreeInclude).toBeUndefined();
	});

	it("reviews: review limits load and save", async () => {
		renderSection("reviews", { autoReviewMaxRounds: 3, approveIdenticalPatch: true });
		expect(await inlineValue("Stop auto-reviews after")).toContain("3");
		expect(screen.getByRole("switch", { name: "Keep approval after a clean rebase" })).toBeChecked();
		await typeInline("Stop auto-reviews after", "5");
		await userEvent.click(screen.getByRole("switch", { name: "Keep approval after a clean rebase" }));
		submit();
		const config = await savedConfig();
		expect(config.autoReviewMaxRounds).toBe(5);
		expect(config.approveIdenticalPatch).toBeUndefined();
	});

	it("workflow: planning mode, verify command and plan auto-approve load and save", async () => {
		renderSection("workflow", { planningMode: "always", verifyCommand: "make test", planAutoApprove: true });
		expect(await screen.findByRole("button", { name: "Plan before building" })).toHaveTextContent("Always plan");
		expect(await inlineValue("Verify command")).toContain("make test");
		expect(screen.getByRole("button", { name: "Auto-approve plans (unattended test projects only)" })).toHaveTextContent("On");
		await choose("Plan before building", "Never plan");
		await typeInline("Verify command", "npm test");
		submit();
		const config = await savedConfig();
		expect(config.planningMode).toBe("off");
		expect(config.verifyCommand).toBe("npm test");
		expect(config.planAutoApprove).toBe(true);
	});

	it("workflow: an unset planning mode shows the app default and an explicit auto stays explicit", async () => {
		trackerIntakeGate.planningMode = "always";
		renderSection("workflow", {});
		expect(await screen.findByRole("button", { name: "Plan before building" })).toHaveTextContent("Use app default (Always plan)");
		await choose("Plan before building", "Automatic");
		submit();
		expect((await savedConfig()).planningMode).toBe("auto");
	});

	it("workflow: a stored auto is shown as Automatic, not as the app default", async () => {
		trackerIntakeGate.planningMode = "always";
		renderSection("workflow", { planningMode: "auto" });
		expect(await screen.findByRole("button", { name: "Plan before building" })).toHaveTextContent("Automatic");
	});

	it("workflow: unset planning mode saves unset", async () => {
		renderSection("workflow", {});
		await screen.findByRole("button", { name: "Plan before building" });
		submit();
		expect((await savedConfig()).planningMode).toBeUndefined();
	});

	it("workflow: plan auto-approve is three-way and an explicit off is kept", async () => {
		trackerIntakeGate.planAutoApprove = true;
		renderSection("workflow", {});
		const row = "Auto-approve plans (unattended test projects only)";
		expect(await screen.findByRole("button", { name: row })).toHaveTextContent("Use app default (On)");
		await choose(row, "Off");
		submit();
		expect((await savedConfig()).planAutoApprove).toBe(false);
	});

	it("workflow: a stored explicit false shows Off, not the app default", async () => {
		trackerIntakeGate.planAutoApprove = true;
		renderSection("workflow", { planAutoApprove: false });
		expect(await screen.findByRole("button", { name: "Auto-approve plans (unattended test projects only)" })).toHaveTextContent(/^Off$/);
	});

	it("workflow: managed locks show read-only rows", async () => {
		trackerIntakeGate.lock = { mode: "always", autoApproveOff: true };
		renderSection("workflow", { planningMode: "off", planAutoApprove: true });
		expect(await screen.findByTestId("project-planning-mode-managed")).toHaveTextContent("Set by your organization: Always plan");
		expect(screen.getByTestId("project-plan-auto-approve-managed")).toHaveTextContent("Set by your organization: Off");
		expect(screen.queryByRole("button", { name: "Plan before building" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Auto-approve plans (unattended test projects only)" })).not.toBeInTheDocument();
	});

	it("sessions: default session mode loads and saves, and empty clears it", async () => {
		renderSection("sessions", { defaultSessionMode: "chat" });
		expect(await screen.findByRole("button", { name: "New worker sessions open as" })).toHaveTextContent("Chat");
		await choose("New worker sessions open as", "App default");
		submit();
		expect((await savedConfig()).defaultSessionMode).toBeUndefined();
	});

	it("advanced: worker rules, rules file and orchestrator rules load and save", async () => {
		renderSection("advanced", { agentRules: "be kind", agentRulesFile: "docs/rules.md", orchestratorRules: "plan first" });
		expect(await screen.findByLabelText("Worker rules")).toHaveValue("be kind");
		expect(await inlineValue("Worker rules file")).toContain("docs/rules.md");
		expect(screen.getByLabelText("Orchestrator rules")).toHaveValue("plan first");
		await typeInto("Worker rules", "be brief");
		await typeInline("Worker rules file", "");
		await typeInto("Orchestrator rules", "ask first");
		submit();
		const config = await savedConfig();
		expect(config.agentRules).toBe("be brief");
		expect(config.agentRulesFile).toBeUndefined();
		expect(config.orchestratorRules).toBe("ask first");
	});

	it("security hosts the project Security mode switch; plan auto-approve is not under Reviews", async () => {
		renderSection("security");
		expect(await screen.findByTestId("security-mode-switch")).toHaveTextContent("proj-1");
	});

	it("reviews no longer carry plan auto-approve", async () => {
		renderSection("reviews");
		await inlineValue("Stop auto-reviews after");
		expect(screen.queryByRole("button", { name: "Auto-approve plans (unattended test projects only)" })).not.toBeInTheDocument();
	});

	it("scratch: workflow hides plan auto-approve, which a scratch project does not save", async () => {
		renderSection("workflow", { }, "scratch");
		await screen.findByRole("button", { name: "Plan before building" });
		expect(screen.queryByRole("button", { name: "Auto-approve plans (unattended test projects only)" })).not.toBeInTheDocument();
	});

	it("agents: shows worker and orchestrator but not the reviewer", async () => {
		renderSection("agents");
		expect(await screen.findByRole("button", { name: "Worker agent" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Reviewer agent" })).not.toBeInTheDocument();
	});
});
