import { useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
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
		if (path === "/api/v1/projects/{id}/skills") return { data: { plugins: skillsPlugins, skills: skillsList }, error: undefined };
		if (path === "/api/v1/agents/{agent}/models") return { data: { agentId: "x", selectionMode: "text", models: [], allowCustom: true, source: "manual", fetchedAt: "2026-07-31T00:00:00Z", stale: false }, error: undefined };
		return { data: { status: "ok", project }, error: undefined };
	});
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(<QueryClientProvider client={queryClient}><Harness section={section} /></QueryClientProvider>);
}

let skillsPlugins: unknown[] = [];
let skillsList: unknown[] = [];
const enforced = (harness: string) => ({ harness, effect: "enforced" });
const skillRow = (name: string, scope: string, reviewerEffect = "enforced") => ({
	name, description: `${name} help`, scope, convention: "claude", path: `/s/${name}`, harnesses: [], deselected: false, removable: false,
	roles: [
		{ role: "worker", on: true, set: false, default: true, effects: [enforced("claude-code"), { harness: "deepseek-harness", effect: "advisory" }] },
		{ role: "orchestrator", on: true, set: false, default: true, effects: [enforced("claude-code"), { harness: "deepseek-harness", effect: "advisory" }] },
		{ role: "reviewer", on: false, set: false, default: false, effects: [{ harness: "claude-code", effect: reviewerEffect }] },
	],
});
const plugin = (id: string, accountEnabled: boolean) => ({ id, name: id.split("@")[0], marketplace: id.split("@")[1], description: `${id} help`, version: "1", path: `/plugins/${id}`, installed: true, accountEnabled });

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
	skillsPlugins = [];
	skillsList = [];
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

	it("reviews no longer carry the project review rules, which moved to Instructions and skills", async () => {
		renderSection("reviews", { reviewerRules: "check migrations" });
		await screen.findByRole("switch", { name: "Keep approval after a clean rebase" });
		expect(screen.queryByLabelText("Project review rules")).toBeNull();
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

	it("browser: Chrome mode loads and saves, and the in-app browser clears it", async () => {
		renderSection("browser", { browserBackend: "chrome" });
		expect(await screen.findByRole("button", { name: "Agent browser" })).toHaveTextContent("Google Chrome (passkeys, Touch ID)");
		await choose("Agent browser", "In-app browser (default)");
		submit();
		expect((await savedConfig()).browserBackend).toBeUndefined();
	});

	it("browser: choosing Google Chrome saves chrome", async () => {
		renderSection("browser", {});
		expect(await screen.findByRole("button", { name: "Agent browser" })).toHaveTextContent("In-app browser (default)");
		await choose("Agent browser", "Google Chrome (passkeys, Touch ID)");
		submit();
		expect((await savedConfig()).browserBackend).toBe("chrome");
	});

	it("advanced no longer carries the rules, which moved to Instructions and skills", async () => {
		renderSection("advanced", { agentRules: "be kind" });
		await waitFor(() => expect(document.getElementById("project-settings-form")).not.toBeNull());
		expect(screen.queryByLabelText("Worker rules")).toBeNull();
	});

	it("instructions: each role tab loads and saves its rules and rules file", async () => {
		renderSection("instructions", { agentRules: "be kind", agentRulesFile: "docs/rules.md", orchestratorRules: "plan first", orchestratorRulesFile: "docs/orch.md", reviewerRules: "check migrations", reviewerRulesFile: "docs/review.md" });
		expect(await screen.findByRole("tab", { name: "Worker" })).toHaveAttribute("aria-selected", "true");
		expect(await screen.findByLabelText("Worker rules")).toHaveValue("be kind");
		expect(await inlineValue("Worker rules file")).toContain("docs/rules.md");
		expect(screen.queryByLabelText("Orchestrator rules")).toBeNull();
		await typeInto("Worker rules", "be brief");
		await typeInline("Worker rules file", "");

		await userEvent.click(screen.getByRole("tab", { name: "Orchestrator" }));
		expect(await screen.findByLabelText("Orchestrator rules")).toHaveValue("plan first");
		expect(await inlineValue("Orchestrator rules file")).toContain("docs/orch.md");
		await typeInto("Orchestrator rules", "ask first");
		await typeInline("Orchestrator rules file", "docs/o2.md");

		await userEvent.click(screen.getByRole("tab", { name: "Reviewer" }));
		expect(await screen.findByLabelText("Project review rules")).toHaveValue("check migrations");
		expect(await inlineValue("Review rules file")).toContain("docs/review.md");
		await typeInto("Project review rules", "check the API");
		await typeInline("Review rules file", "");
		submit();
		const config = await savedConfig();
		expect(config.agentRules).toBe("be brief");
		expect(config.agentRulesFile).toBeUndefined();
		expect(config.orchestratorRules).toBe("ask first");
		expect(config.orchestratorRulesFile).toBe("docs/o2.md");
		expect(config.reviewerRules).toBe("check the API");
		expect(config.reviewerRulesFile).toBeUndefined();
	});

	it("instructions: plugin rows show the account value per role and save claudePlugins per role", async () => {
		skillsPlugins = [plugin("design@m", true), plugin("lsp@m", false)];
		renderSection("instructions", { claudePlugins: { reviewer: { "design@m": false } } });
		const design = await screen.findByTestId("skill-role-row-design@m");
		const sw = (row: HTMLElement, role: string) => within(row).getByRole("switch", { name: new RegExp(`for ${role}$`) });
		expect(sw(design, "Worker")).toBeChecked(); // account value
		expect(sw(design, "Reviewer")).not.toBeChecked(); // project override
		const lsp = screen.getByTestId("skill-role-row-lsp@m");
		expect(sw(lsp, "Orchestrator")).not.toBeChecked();
		await userEvent.click(sw(design, "Worker"));
		await userEvent.click(sw(lsp, "Reviewer"));
		submit();
		const config = await savedConfig();
		expect(config.claudePlugins).toEqual({ worker: { "design@m": false }, reviewer: { "design@m": false, "lsp@m": true } });
	});

	it("instructions: skill rows follow role defaults, name non-enforced effects and save skillRoles", async () => {
		skillsList = [skillRow("design", "user"), skillRow("design", "repo"), skillRow("repo-a", "repo", "unsupported"), skillRow("using-tallpine", "tallpine")];
		renderSection("instructions", { skillRoles: { worker: { "repo-a": false } } });
		const design = await screen.findByTestId("skill-role-row-skill-design");
		const sw = (row: HTMLElement, role: string) => within(row).getByRole("switch", { name: new RegExp(`for ${role}$`) });
		expect(sw(design, "Worker")).toBeChecked();
		expect(sw(design, "Reviewer")).not.toBeChecked(); // reviewers default off
		expect(screen.getAllByTestId("skill-role-row-skill-design")).toHaveLength(1); // one row per name
		expect(screen.queryByTestId("skill-role-row-skill-using-tallpine")).toBeNull();
		const repo = screen.getByTestId("skill-role-row-skill-repo-a");
		expect(sw(repo, "Worker")).not.toBeChecked(); // project switch
		expect(within(repo).getByTestId("skill-role-effect-skill-repo-a")).toHaveTextContent("Worker, Orchestrator: advisory on DeepSeek");
		expect(within(repo).getByTestId("skill-role-effect-skill-repo-a")).toHaveTextContent("Reviewer: not applied on Claude Code.");
		// Two rows with different notes share none: each keeps its own, nothing is promoted above the list.
		expect(screen.queryByTestId("skills-effect-note")).toBeNull();
		expect(screen.getByTestId("skill-role-effect-skill-design")).toHaveTextContent("Worker, Orchestrator: advisory on DeepSeek");
		await userEvent.click(sw(design, "Reviewer"));
		await userEvent.click(sw(repo, "Worker"));
		submit();
		const config = await savedConfig();
		expect(config.skillRoles).toEqual({ worker: { "repo-a": true }, reviewer: { design: true } });
	});

	it("instructions: a long skill list gets a filter and the path is only a tooltip", async () => {
		skillsList = ["alpha", "beta", "gamma", "delta", "epsilon", "zeta"].map((n) => skillRow(n, "user"));
		renderSection("instructions");
		const filter = await screen.findByRole("searchbox", { name: "Filter skills" });
		expect(await screen.findAllByTestId(/^skill-role-row-skill-/)).toHaveLength(6);
		expect(screen.getByTestId("skills-effect-note")).toHaveTextContent("advisory on DeepSeek"); // shared by every row: shown once
		expect(screen.queryByTestId("skill-role-effect-skill-alpha")).toBeNull();
		expect(screen.queryByText(/\/skills\/alpha/)).toBeNull(); // path is not a visible line
		await userEvent.type(filter, "GAM");
		expect(screen.getAllByTestId(/^skill-role-row-skill-/)).toHaveLength(1);
		await userEvent.clear(filter);
		await userEvent.type(filter, "nothing-like-this");
		expect(screen.queryAllByTestId(/^skill-role-row-skill-/)).toHaveLength(0);
		expect(screen.getByText(/No skills match/)).toBeInTheDocument();
	});

	it("instructions: a default-branch reviewer skill switches for the reviewer only, on by default", async () => {
		const row = skillRow("sql-review", "reviewer-repo");
		row.roles[2] = { ...row.roles[2], on: true, default: true };
		skillsList = [row, skillRow("mine", "custom")];
		renderSection("instructions");
		const repo = await screen.findByTestId("skill-role-row-skill-sql-review");
		const sw = (r: HTMLElement, role: string) => within(r).getByRole("switch", { name: new RegExp(`for ${role}$`) });
		expect(sw(repo, "Worker")).toBeDisabled();
		expect(sw(repo, "Orchestrator")).toBeDisabled();
		expect(sw(repo, "Reviewer")).toBeChecked();
		expect(sw(repo, "Reviewer")).toBeEnabled();
		const custom = screen.getByTestId("skill-role-row-skill-mine");
		expect(sw(custom, "Worker")).toBeEnabled();
		expect(sw(custom, "Reviewer")).not.toBeChecked();
		await userEvent.click(sw(repo, "Reviewer"));
		submit();
		const config = await savedConfig();
		expect(config.skillRoles).toEqual({ reviewer: { "sql-review": false } });
	});

	it("instructions: rows follow the app default, a project switch wins, and Reset returns to the app value", async () => {
		const row = skillRow("house", "custom");
		row.roles[0] = { ...row.roles[0], on: true, set: true, default: false, app: true } as typeof row.roles[0];
		row.roles[2] = { ...row.roles[2], on: true, default: true, app: true } as typeof row.roles[2];
		skillsList = [row];
		renderSection("instructions", { skillRoles: { worker: { house: true } } });
		const house = await screen.findByTestId("skill-role-row-skill-house");
		const sw = (role: string) => within(house).getByRole("switch", { name: new RegExp(`for ${role}$`) });
		expect(within(house).getByTestId("skill-role-app-skill-house")).toHaveTextContent("Follows the app default: Reviewer");
		expect(sw("Worker")).toBeChecked(); // the project's own switch beats the app's off
		expect(sw("Reviewer")).toBeChecked(); // inherited from the app
		await userEvent.click(within(house).getByRole("button", { name: "Reset house to the inherited switches" }));
		expect(sw("Worker")).not.toBeChecked();
		expect(within(house).getByTestId("skill-role-app-skill-house")).toHaveTextContent("Follows the app default: Worker, Reviewer");
		expect(within(house).queryByRole("button", { name: /^Reset/ })).toBeNull();
		submit();
		const config = await savedConfig();
		expect(config.skillRoles?.worker?.house).toBeUndefined();
	});

	it("instructions: the plugin list has its own filter", async () => {
		skillsPlugins = [plugin("design@m", true), plugin("lsp@m", false)];
		renderSection("instructions");
		await screen.findByTestId("skill-role-row-design@m");
		await userEvent.type(screen.getByRole("searchbox", { name: "Filter plugins" }), "lsp");
		expect(screen.queryByTestId("skill-role-row-design@m")).toBeNull();
		expect(screen.getByTestId("skill-role-row-lsp@m")).toBeInTheDocument();
	});

	it("instructions: auto-memory round-trips per role and an untouched config keeps no plugin map", async () => {
		renderSection("instructions", { claudeAutoMemory: { reviewer: false } });
		const row = await screen.findByTestId("skill-role-row-claude-auto-memory");
		expect(within(row).getByRole("switch", { name: "Auto-memory for Reviewer" })).not.toBeChecked();
		expect(within(row).getByRole("switch", { name: "Auto-memory for Worker" })).toBeChecked();
		expect(await screen.findByText("No Claude Code plugins in your account.")).toBeInTheDocument();
		await userEvent.click(within(row).getByRole("switch", { name: "Auto-memory for Orchestrator" }));
		submit();
		const config = await savedConfig();
		expect(config.claudeAutoMemory).toEqual({ reviewer: false, orchestrator: false });
		expect(config.claudePlugins).toBeUndefined();
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
