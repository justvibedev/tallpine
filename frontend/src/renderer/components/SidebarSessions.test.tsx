import {
	cloudGateState,
	cloudSessionState,
	dragEnds,
	dragOvers,
	dragStarts,
	getMock,
	navigateMock,
	mockLocation,
	mockParams,
	postMock,
	renameSessionMock,
	spawnMock,
	resumeOrchestratorMock,
	commandPaletteEnabled,
	workspace,
	session,
	confirmArchiveFromRow,
	sidebarPR,
	CreateProjectHandler,
	InitializeProjectHandler,
	importValidation,
	renderSidebar,
	mockAgentReadinessResponse,
	chooseOption,
	fireDrag,
} from "./Sidebar.test.setup";

import {
	act,
	fireEvent,
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
import { SIDEBAR_DEFAULT_WIDTH, SIDEBAR_MIN_WIDTH } from "./Sidebar";
import { STANDALONE_PROJECT_KIND, STANDALONE_WORKSPACE_ID, type WorkspaceSummary } from "../types/workspace";
import { agentReadiness } from "../test/agent-readiness-fixtures";
import { useUiStore } from "../stores/ui-store";
import { useSidebarOrderStore } from "../stores/sidebar-order-store";

beforeEach(() => {
	window.localStorage.clear();
	useSidebarOrderStore.setState({ projectOrder: [], pendingCreatedProjectIds: [], sessionOrders: {} });
	dragEnds.clear();
	dragOvers.clear();
	dragStarts.clear();
	document.documentElement.style.removeProperty("--tallpine-sidebar-w");
	commandPaletteEnabled.current = true;
	cloudGateState.cloudEnabled = true;
	cloudGateState.localEnabled = true;
	cloudGateState.client = "";
	cloudSessionState.configured = false;
	cloudSessionState.session = null;
	cloudSessionState.status = "unauthenticated";
	cloudSessionState.signIn.mockReset();
	cloudSessionState.signOut.mockReset().mockResolvedValue(undefined);
	useUiStore.setState({
		isCommandPaletteOpen: false,
		newTaskRequest: null,
		settingsModal: null,
		provisioningProjectIds: new Set(),
		restartingProjectIds: new Set(),
	});
	getMock.mockReset();
	getMock.mockResolvedValue({
		data: {
			agents: [agentReadiness("claude-code", "Claude Code"), agentReadiness("codex", "Codex")],
		},
		error: undefined,
	});
	postMock.mockReset();
	postMock.mockImplementation(async (path: string, options?: { body?: { importKind?: string; path?: string; remoteUrl?: string } }) => {
		if (path === "/api/v1/projects/clone/check") {
			return { data: { remoteUrl: options?.body?.remoteUrl ?? "", path: "/repo/web-app", exists: false, canUseExisting: false } };
		}
		if (path === "/api/v1/projects/clone/prepare") {
			return {
				data: { path: "/repo/web-app", remoteUrl: "git@github.com:acme/web-app.git", preparationId: "prep-web-app" },
				error: undefined,
			};
		}
		if (path === "/api/v1/imports/validate") {
			const selectedPath = options?.body?.path ?? "/repo/workspace";
			const importKind = options?.body?.importKind ?? "workspace";
			return {
				data: importValidation(selectedPath, { importKind }),
				error: undefined,
			};
		}
		if (path === "/api/v1/imports/prepare-git") {
			const selectedPath = options?.body?.path ?? "/repo/workspace";
			return {
				data: {
					events: [],
					validation: importValidation(selectedPath),
				},
				error: undefined,
			};
		}
		return { data: undefined, error: undefined };
	});
	window.tallpine!.app.scanImportFolder = vi.fn().mockImplementation(async ({ path }: { path: string }) => ({
		path,
		repos: [],
	}));
	window.tallpine!.app.getGitHubLogin = vi.fn().mockResolvedValue("test-user");
	window.tallpine!.app.getRepositoryBranch = vi.fn().mockResolvedValue(undefined);
	navigateMock.mockReset();
	renameSessionMock.mockReset().mockResolvedValue(undefined);
	spawnMock.mockReset();
	resumeOrchestratorMock.mockReset().mockResolvedValue(undefined);
	mockParams.projectId = undefined;
	mockParams.sessionId = undefined;
	mockParams.hostId = undefined;
	mockLocation.pathname = "/";
});

afterEach(() => {
	vi.restoreAllMocks();
});

describe("Sidebar", () => {
	it("shows ancestor repo warning in agent sheet for workspace inside existing repo", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockResolvedValue({
			data: { project: { id: "ws-1", name: "My Workspace", kind: "workspace", path: "/repo/inner" } },
			error: null,
		}) as unknown as CreateProjectHandler;
		const onInitializeProject = vi.fn().mockResolvedValue(undefined) as InitializeProjectHandler;
		window.tallpine!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/inner");
		window.tallpine!.app.checkAncestorRepo = vi
			.fn()
			.mockResolvedValue(
				"Selected folder is inside an existing Git repository at /repo. Tallpine will initialize this folder as a separate repository.",
			);
		renderSidebar({ onCreateProject, onInitializeProject });

		await user.click(screen.getByLabelText("New project"));
		await user.click(screen.getByRole("button", { name: /^Open a workspace$/i }));
		await screen.findByRole("dialog", { name: "Import workspace" });
		await user.click(screen.getByRole("button", { name: "Continue" }));
		expect(
			screen.getByText(
				"Selected folder is inside an existing Git repository at /repo. Tallpine will initialize this folder as a separate repository.",
			),
		).toBeInTheDocument();
		expect(
			screen.getByText(
				"If this folder needs Git setup, Tallpine will initialize it and create the first commit before starting.",
			),
		).toBeInTheDocument();
		await chooseOption(screen.getByRole("combobox", { name: "Orchestrator agent" }), "Claude Code");
		await user.click(screen.getByRole("button", { name: "Create workspace and start" }));

		await waitFor(() => expect(onCreateProject).toHaveBeenCalledTimes(1));
		expect(onCreateProject).toHaveBeenCalledWith(
			expect.objectContaining({ path: "/repo/inner", asWorkspace: true }),
		);
		expect(onInitializeProject).not.toHaveBeenCalled();
		expect(window.tallpine!.app.checkAncestorRepo).toHaveBeenCalledWith("/repo/inner");
	});

	it("opens global settings from the footer menu when no project is selected", async () => {
		const user = userEvent.setup();
		renderSidebar();

		await user.click(screen.getByRole("button", { name: /project actions/i }));

		expect(await screen.findByRole("menuitem", { name: /settings/i })).toBeInTheDocument();
	});

	it("hides unavailable agents while keeping ready agents selectable", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		window.tallpine!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/new-project");
		mockAgentReadinessResponse({
			data: {
				agents: [
					agentReadiness("claude-code", "Claude Code"),
					agentReadiness("cursor", "Cursor", { authentication: "unauthorized" }),
					agentReadiness("aider", "Aider", { installation: "not_installed", authentication: "unknown" }),
				],
			},
			error: undefined,
		});
		renderSidebar({ onCreateProject, seedAgents: false });

		await user.click(screen.getByLabelText("New project"));
		await user.click(screen.getByRole("button", { name: /^Open a local folder$/i }));
		expect(await screen.findByRole("dialog", { name: "Set up project" })).toBeInTheDocument();

		await user.click(screen.getByRole("combobox", { name: "Orchestrator agent" }));
		const options = await screen.findAllByRole("option");
		expect(options.map((option) => option.textContent)).toEqual([
			"Claude Code",
			"Manage agents…",
		]);
		expect(options[1]).not.toHaveAttribute("aria-disabled", "true");
		await user.keyboard("{Escape}");

		await user.click(screen.getByRole("button", { name: "Create and start" }));

		await waitFor(() =>
			expect(onCreateProject).toHaveBeenCalledWith(expect.objectContaining({ orchestratorAgent: "claude-code" })),
		);
	});

	it("updates project agent options when the catalog loads after the dialog opens", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		window.tallpine!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/new-project");
		let resolveAgents!: (value: {
			data: { agents: ReturnType<typeof agentReadiness>[] };
			error: undefined;
		}) => void;
		mockAgentReadinessResponse(
			new Promise((resolve) => {
				resolveAgents = resolve;
			}),
		);
		renderSidebar({ onCreateProject, seedAgents: false });

		await user.click(screen.getByLabelText("New project"));
		await user.click(screen.getByRole("button", { name: /^Open a local folder$/i }));
		expect(await screen.findByRole("dialog", { name: "Set up project" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Create and start" })).toBeDisabled();

		resolveAgents({
			data: {
				agents: [agentReadiness("claude-code", "Claude Code"), agentReadiness("codex", "Codex")],
			},
			error: undefined,
		});

		await chooseOption(screen.getByRole("combobox", { name: "Orchestrator agent" }), "Claude Code");
		await user.click(screen.getByRole("button", { name: "Create and start" }));

		await waitFor(() =>
			expect(onCreateProject).toHaveBeenCalledWith({
				path: "/repo/new-project",
				workerAgent: "claude-code",
				orchestratorAgent: "claude-code",
				trackerIntake: undefined,
				asWorkspace: false,
			}),
		);
	});

	it("opens the command palette when Search is clicked", async () => {
		const user = userEvent.setup();
		renderSidebar();
		expect(useUiStore.getState().isCommandPaletteOpen).toBe(false);
		await user.click(screen.getByRole("button", { name: /Search/ }));
		expect(useUiStore.getState().isCommandPaletteOpen).toBe(true);
		expect(screen.getByRole("button", { name: /Search/ })).toHaveTextContent(/(?:⌘ |Ctrl\+)K/);
	});

	it("defers opening the palette until the Search click has been dispatched", async () => {
		renderSidebar();
		fireEvent.click(screen.getByRole("button", { name: /Search/ }));
		// Still closed inside the click's task: the palette dialog must not mount
		// while the pointer sequence that opened it is still being handled.
		expect(useUiStore.getState().isCommandPaletteOpen).toBe(false);
		await act(async () => {});
		expect(useUiStore.getState().isCommandPaletteOpen).toBe(true);
	});

	it("hides Search when the command palette feature is disabled", () => {
		commandPaletteEnabled.current = false;
		renderSidebar();
		expect(screen.queryByRole("button", { name: /Search/ })).not.toBeInTheDocument();
	});

	it("shows the project name and context in the ConfirmDialog description", async () => {
		const user = userEvent.setup();
		renderSidebar();

		await user.click(screen.getByLabelText("Project actions for Project One"));
		await user.click(await screen.findByRole("menuitem", { name: "Remove project" }));

		const dialog = await screen.findByRole("dialog", { name: "Remove project" });
		expect(dialog).toHaveTextContent("Project One");
		expect(dialog).toHaveTextContent("live sessions");
		expect(dialog).toHaveTextContent("repository folder");
	});

	it("warns when removing a project would hide open pull requests", async () => {
		const user = userEvent.setup();
		const workspaceWithPullRequests = {
			...workspace,
			sessions: [
				{ ...session, prs: [sidebarPR()] },
				{
					...session,
					id: "proj-1-2",
					prs: [
						sidebarPR(),
						sidebarPR({ number: 8, state: "draft", url: "https://github.com/acme/project-one/pull/8" }),
						sidebarPR({ number: 9, state: "merged", url: "https://github.com/acme/project-one/pull/9" }),
					],
				},
			],
		};
		renderSidebar({ workspaces: [workspaceWithPullRequests] });

		await user.click(screen.getByLabelText("Project actions for Project One"));
		await user.click(await screen.findByRole("menuitem", { name: "Remove project" }));

		expect(await screen.findByRole("dialog", { name: "Remove project" })).toHaveTextContent(
			"2 open pull requests belong to this project. Removing it will hide those pull requests from Tallpine, but will not close them.",
		);
	});

	it("renames a session inline by double-clicking its name", async () => {
		const user = userEvent.setup();
		const workspaceWithSession = { ...workspace, sessions: [session] };
		renderSidebar({ workspaces: [workspaceWithSession] });

		await user.dblClick(screen.getByRole("button", { name: "Open fix login" }));
		expect(navigateMock).toHaveBeenCalledTimes(1);
		const input = screen.getByLabelText("Rename fix login");
		await user.clear(input);
		await user.type(input, "polish login{Enter}");

		await waitFor(() => expect(renameSessionMock).toHaveBeenCalledWith("proj-1-1", "polish login"));
		expect(navigateMock).toHaveBeenCalledTimes(1);
	});

	it("still opens a session after an unpaired single click", async () => {
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

		fireEvent.click(screen.getByRole("button", { name: "Open fix login" }), { detail: 1 });
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "proj-1-1" },
		});
	});

	it("starts the same inline rename from the session context menu", async () => {
		const user = userEvent.setup();
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

		fireEvent.contextMenu(screen.getByRole("button", { name: "Open fix login" }));
		const renameItem = await screen.findByRole("menuitem", { name: "Rename fix login" });
		const menu = renameItem.closest('[role="menu"]');
		if (!menu) throw new Error("Session context menu not found");
		expect(within(menu as HTMLElement).getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["Rename", "Archive session", "Copy session ID", "Copy session link"]);
		expect(renameItem).toHaveTextContent(/^Rename$/);
		expect(renameItem.querySelector("svg")).toBeInTheDocument();
		await user.click(renameItem);

		expect(screen.getByRole("textbox", { name: "Rename fix login" })).toHaveFocus();
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("caps the inline rename input at 100 characters", async () => {
		const user = userEvent.setup();
		const workspaceWithSession = { ...workspace, sessions: [session] };
		renderSidebar({ workspaces: [workspaceWithSession] });

		await user.dblClick(screen.getByText("fix login"));
		expect(screen.getByLabelText("Rename fix login")).toHaveAttribute("maxlength", "100");
	});

	it("renders rename as an unboxed inline label editor", async () => {
		const user = userEvent.setup();
		const lastUserMessageAt = "2026-06-29T23:55:00Z";
		mockParams.sessionId = session.id;
		renderSidebar({
			workspaces: [{ ...workspace, sessions: [{ ...session, lastUserMessageAt }] }],
		});

		await user.dblClick(screen.getByText("fix login"));
		const input = screen.getByLabelText("Rename fix login");
		const time = input.parentElement?.querySelector("time");

		expect(input).toHaveAttribute("data-session-inline-editor");
		expect(input).toHaveClass("border-0", "bg-transparent!", "p-0", "ring-0");
		expect(input).not.toHaveClass("rounded-xs", "border-accent", "px-1", "focus-visible:ring-1");
		expect(input.parentElement).toHaveAttribute("data-session-row");
		expect(input.parentElement?.className).toContain("group/nav-row");
		expect(input.parentElement).toHaveClass("text-foreground", "pr-1");
		expect(input.parentElement).not.toHaveClass("bg-interactive-active");
		const highlight = input.parentElement?.querySelector("[data-nav-row-highlight]");
		expect(highlight).toBeTruthy();
		expect(highlight?.className).toMatch(/bg-interactive-active/);
		expect(highlight?.className).toMatch(/opacity-100/);
		expect(time).toHaveAttribute("data-session-message-age", "");
		expect(time).toHaveAttribute("datetime", lastUserMessageAt);
		expect(time).toHaveClass("font-sans", "tabular-nums");
		expect(time).not.toHaveClass("font-mono");
	});

	it("offers F2 as a keyboard rename path", async () => {
		const user = userEvent.setup();
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

		const openSession = screen.getByLabelText("Open fix login");
		expect(openSession).toHaveAttribute("aria-keyshortcuts", "F2");
		openSession.focus();
		await user.keyboard("{F2}");

		expect(screen.getByLabelText("Rename fix login")).toHaveFocus();
	});

	it("retains a double-tap rename path for touch", () => {
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

		const label = screen.getByText("fix login");
		fireEvent.pointerUp(label, { pointerType: "touch" });
		fireEvent.pointerUp(label, { pointerType: "touch" });

		expect(screen.getByLabelText("Rename fix login")).toBeInTheDocument();
	});

	it("cancels the inline rename on Escape without calling the daemon", async () => {
		const user = userEvent.setup();
		const workspaceWithSession = { ...workspace, sessions: [session] };
		renderSidebar({ workspaces: [workspaceWithSession] });

		await user.dblClick(screen.getByText("fix login"));
		const input = screen.getByLabelText("Rename fix login");
		await user.clear(input);
		await user.type(input, "discard me{Escape}");

		expect(renameSessionMock).not.toHaveBeenCalled();
		expect(screen.getByLabelText("Open fix login")).toBeInTheDocument();
	});

	it("always shows action icons and reserves padding for them", () => {
		renderSidebar();

		const projectRow = screen.getByText("Project One").closest('button, [role="button"]');
		const actionCluster = screen.getByLabelText("Project actions for Project One").parentElement;

		if (!projectRow) throw new Error("Project row button not found");
		expect(projectRow).toHaveClass("pr-sidebar-project-actions");
		expect(actionCluster).toHaveAttribute("data-project-actions");
		expect(actionCluster).toHaveClass("right-1", "gap-0.5");
		expect(within(actionCluster as HTMLElement).getAllByRole("button")).toHaveLength(2);
		expect(screen.getByLabelText("Project actions for Project One")).not.toHaveClass("opacity-0");
	});

	it("optically aligns the project folder and label with its action icons", () => {
		renderSidebar();

		const projectRow = screen.getByText("Project One").closest('button, [role="button"]');
		expect(projectRow?.querySelector("[data-project-folder-visual]")).toHaveClass("translate-y-px");
		expect(projectRow?.querySelector("[data-project-label]")).toHaveClass("translate-y-px");
	});

	it("caps the project list at 10 until Show more is clicked", async () => {
		const user = userEvent.setup();
		const manyProjects = Array.from({ length: 14 }, (_, index) => ({
			...workspace,
			id: `proj-${index + 1}`,
			name: `Project ${index + 1}`,
			path: `/repo/project-${index + 1}`,
		}));
		renderSidebar({ workspaces: manyProjects });

		expect(screen.getByText("Project 10")).toBeInTheDocument();
		expect(screen.queryByText("Project 11")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Show 4 more projects" })).toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Show 4 more projects" }));

		expect(screen.getByText("Project 11")).toBeInTheDocument();
		expect(screen.getByText("Project 14")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Show fewer projects" })).toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Show fewer projects" }));

		expect(screen.queryByText("Project 11")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Show 4 more projects" })).toBeInTheDocument();
	});

	it("keeps the active project row at the top of the scroller", () => {
		mockParams.projectId = "proj-1";
		const other: WorkspaceSummary = {
			...workspace,
			id: "proj-2",
			name: "Project Two",
			path: "/repo/project-two",
		};

		renderSidebar({ workspaces: [workspace, other] });

		const activeRow = document.querySelector('[data-project-drag-row][data-project-id="proj-1"]');
		expect(activeRow).toHaveClass("sticky", "top-0", "z-20", "bg-sidebar");
	});

	it("caps each project's agent list at 6 until its Show more is clicked", async () => {
		const user = userEvent.setup();
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: Array.from({ length: 9 }, (_, index) => ({
						...session,
						id: `proj-1-${index + 1}`,
						title: `Agent ${index + 1}`,
						// Descending so sortedWorkerSessions keeps the fixture order.
						updatedAt: `2026-06-${30 - index}T00:00:00Z`,
					})),
				},
			],
		});

		const list = screen.getByTestId("session-list-proj-1");
		expect(within(list).getByText("Agent 6")).toBeInTheDocument();
		expect(screen.queryByText("Agent 7")).not.toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Show 3 more agents" }));

		expect(screen.getByText("Agent 7")).toBeInTheDocument();
		expect(screen.getByText("Agent 9")).toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Show fewer agents" }));

		expect(screen.queryByText("Agent 7")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Show 3 more agents" })).toBeInTheDocument();
	});

	it("lifts a project's agent cap when the open session sits past it", () => {
		mockParams.projectId = "proj-1";
		mockParams.sessionId = "proj-1-8";
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: Array.from({ length: 9 }, (_, index) => ({
						...session,
						id: `proj-1-${index + 1}`,
						title: `Agent ${index + 1}`,
						updatedAt: `2026-06-${30 - index}T00:00:00Z`,
					})),
				},
			],
		});

		expect(screen.getByText("Agent 8")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Show fewer agents" })).toBeInTheDocument();
	});

	it("keeps Scratchpad mounted, bounded and un-clipped by a long Projects list", () => {
		const manyProjects = Array.from({ length: 14 }, (_, index) => ({
			...workspace,
			id: `proj-${index + 1}`,
			name: `Project ${index + 1}`,
			path: `/repo/project-${index + 1}`,
		}));
		renderSidebar({
			workspaces: [
				...manyProjects,
				{
					id: STANDALONE_WORKSPACE_ID,
					name: "Scratchpad",
					kind: STANDALONE_PROJECT_KIND,
					path: "",
					sessions: [{ ...session, id: "adhoc-1", title: "adhoc one", workspaceId: STANDALONE_WORKSPACE_ID, workspaceName: "Scratchpad" }],
				},
			],
		});

		const section = document.querySelector<HTMLElement>("[data-scratchpad-section]")!;
		expect(section).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Scratchpad" })).toBeVisible();
		expect(section).toContainElement(screen.getByText("adhoc one"));
		// Both sections sit in auto-sized grid rows: each takes its content height,
		// spare space goes to whichever needs it, and a tie splits the column evenly.
		expect(section).not.toHaveClass("shrink-0");
		expect(section.style.maxHeight).toBe("");
		const projects = screen.getByTestId("sidebar-projects-scroller");
		expect(projects).toHaveClass("min-h-0", "flex-1", "overflow-y-auto");
		expect(projects.style.maxHeight).toBe("");
		expect(section.parentElement).toHaveClass("grid", "min-h-0", "auto-rows-[minmax(0,auto)]");
		expect(section.parentElement?.style.getPropertyValue("--sidebar-scratchpad-reserved-height")).toBe("");
	});

	it("fits the project list to content up to the full available height", async () => {
		const user = userEvent.setup();
		const manyProjects = Array.from({ length: 14 }, (_, index) => ({
			...workspace,
			id: `proj-${index + 1}`,
			name: `Project ${index + 1}`,
			path: `/repo/project-${index + 1}`,
		}));
		renderSidebar({ workspaces: manyProjects });

		const scroller = screen.getByTestId("sidebar-projects-scroller");
		expect(scroller).toHaveClass("overflow-y-auto");
		expect(scroller).toContainElement(screen.getByText("Project 1"));
		expect(scroller.style.height).toBe("");
		// Height comes from flex layout, not a measured CSS variable.
		expect(scroller.style.maxHeight).toBe("");
		expect(scroller).toHaveClass("min-h-0", "flex-1");

		// Show more stays directly beneath the project list and reveals the remainder.
		const showMore = screen.getByRole("button", { name: "Show 4 more projects" });
		expect(showMore.parentElement).toBe(scroller.parentElement?.parentElement);
		await user.click(showMore);
		expect(scroller.style.maxHeight).toBe("");
		expect(scroller).toHaveClass("overflow-y-auto");
	});

	it("collapses the project list from the Projects header", async () => {
		const user = userEvent.setup();
		renderSidebar();

		await user.click(screen.getByRole("button", { name: "Projects" }));
		expect(screen.getByRole("button", { name: "Projects" })).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByTestId("sidebar-projects-scroller")).not.toBeInTheDocument();
		expect(screen.queryByText("Project One")).not.toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Projects" }));
		expect(screen.getByRole("button", { name: "Projects" })).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByText("Project One")).toBeInTheDocument();
		expect(screen.getByTestId("sidebar-projects-scroller")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /Show (more|fewer) projects/ })).not.toBeInTheDocument();
	});

	it("keeps the project list capped behind Show more while the sidebar is toggled off", () => {
		const manyProjects = Array.from({ length: 14 }, (_, index) => ({
			...workspace,
			id: `proj-${index + 1}`,
			name: `Project ${index + 1}`,
			path: `/repo/project-${index + 1}`,
		}));
		renderSidebar({ workspaces: manyProjects, initialOpen: false });

		// The sidebar slides away (offcanvas); it has no icon rail, so its lists
		// must not lift their caps just because it is collapsed.
		expect(screen.queryByText("Project 11")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { hidden: true, name: /more projects/ })).toBeInTheDocument();
	});

	it("does not reopen a closed Projects section when the sidebar is toggled off and on", async () => {
		const user = userEvent.setup();
		renderSidebar();

		await user.click(screen.getByRole("button", { name: "Projects" }));
		expect(screen.getByRole("button", { name: "Projects" })).toHaveAttribute("aria-expanded", "false");
		await waitFor(() => expect(screen.queryByText(workspace.name)).not.toBeInTheDocument());

		await user.click(screen.getByRole("button", { name: "Toggle sessions panel" }));
		// While the sidebar is off, the closed section must not be forced open.
		expect(screen.queryByText(workspace.name)).not.toBeInTheDocument();
		await user.click(screen.getAllByRole("button", { hidden: true, name: "Expand sidebar" })[0]);

		expect(screen.getByRole("button", { name: "Projects" })).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByText(workspace.name)).not.toBeInTheDocument();
	});

	it("clamps width at minimum when dragged past the resize floor (no auto-collapse)", async () => {
		renderSidebar();

		const resizeHandle = screen.getByTestId("resize-handle");
		expect(resizeHandle).toBeInTheDocument();
		expect(document.querySelector('[data-slot="sidebar"][data-state="expanded"]')).toBeInTheDocument();
		expect(document.documentElement.style.getPropertyValue("--tallpine-sidebar-w")).toBe("");

		fireEvent.pointerDown(resizeHandle, { clientX: SIDEBAR_DEFAULT_WIDTH });
		// Drag well past minimum — sidebar should stay expanded and clamp at min.
		fireEvent.pointerMove(window, { clientX: SIDEBAR_MIN_WIDTH - 50 });
		fireEvent.pointerUp(window);

		// Sidebar stays expanded; dragging no longer collapses it.
		expect(document.querySelector('[data-slot="sidebar"][data-state="expanded"]')).toBeInTheDocument();
		expect(
			document
				.querySelector<HTMLElement>('[data-slot="sidebar-gap"]')
				?.style.getPropertyValue("--tallpine-sidebar-w"),
		).toBe(`${SIDEBAR_MIN_WIDTH}px`);
	});

	it("persists the latest clamped width on pointer-up before the queued frame", () => {
		renderSidebar();
		const resizeHandle = screen.getByTestId("resize-handle");
		const gap = document.querySelector<HTMLElement>('[data-slot="sidebar-gap"]')!;
		const initialWidth = gap.style.getPropertyValue("--tallpine-sidebar-w");
		fireEvent.pointerDown(resizeHandle, { clientX: SIDEBAR_DEFAULT_WIDTH });
		fireEvent.pointerMove(window, { clientX: SIDEBAR_MIN_WIDTH + 5 });
		expect(gap.style.getPropertyValue("--tallpine-sidebar-w")).toBe(initialWidth);
		fireEvent.pointerUp(window);
		expect(gap.style.getPropertyValue("--tallpine-sidebar-w")).toBe(`${SIDEBAR_MIN_WIDTH + 5}px`);
		expect(window.localStorage.getItem("ao-sidebar-w")).toBe(String(SIDEBAR_MIN_WIDTH + 5));
	});

	it("never drops the floor below SIDEBAR_MIN_WIDTH", () => {
		renderSidebar();
		const gap = document.querySelector<HTMLElement>('[data-slot="sidebar-gap"]')!;
		fireEvent.pointerDown(screen.getByTestId("resize-handle"), { clientX: SIDEBAR_DEFAULT_WIDTH });
		fireEvent.pointerMove(window, { clientX: 0 });
		fireEvent.pointerUp(window);
		expect(gap.style.getPropertyValue("--tallpine-sidebar-w")).toBe(`${SIDEBAR_MIN_WIDTH}px`);
	});

	it("paints the dot from its board section while activity drives the pulse", () => {
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [
						{
							...session,
							id: "proj-1-idle",
							title: "idle task",
							status: "idle",
							activity: { state: "idle", lastActivityAt: "2026-06-30T00:00:00Z" },
						},
						{
							...session,
							id: "proj-1-work",
							title: "working task",
							status: "working",
							activity: { state: "active", lastActivityAt: "2026-06-30T00:00:00Z" },
						},
						{
							...session,
							id: "proj-1-ci",
							title: "ci failed task",
							status: "working",
							scmStatus: "ci_failed",
							activity: { state: "active", lastActivityAt: "2026-06-30T00:00:00Z" },
							prs: [sidebarPR({ ci: "failing" })],
						},
						{
							...session,
							id: "proj-1-review",
							title: "review task",
							status: "working",
							scmStatus: "pr_open",
							activity: { state: "active", lastActivityAt: "2026-06-30T00:00:00Z" },
							prs: [sidebarPR()],
						},
						{
							...session,
							id: "proj-1-ready",
							title: "ready task",
							status: "working",
							scmStatus: "mergeable",
							activity: { state: "active", lastActivityAt: "2026-06-30T00:00:00Z" },
							prs: [sidebarPR({ mergeability: "mergeable" })],
						},
						{
							...session,
							id: "proj-1-merged",
							title: "merged task",
							status: "working",
							scmStatus: "merged",
							activity: { state: "active", lastActivityAt: "2026-06-30T00:00:00Z" },
							prs: [sidebarPR({ state: "merged" })],
						},
					],
				},
			],
		});
		const sessionDot = (title: string) =>
			screen.getByLabelText(`Open ${title}`).querySelector<HTMLElement>("[data-session-status]");

		// No pull request: the dot falls back to runtime status.
		expect(sessionDot("idle task")).toHaveClass("bg-status-idle");
		expect(sessionDot("idle task")).not.toHaveClass("animate-status-pulse");

		const workingDot = sessionDot("working task");
		expect(workingDot).toHaveClass("bg-status-working");
		expect(workingDot).toHaveClass("animate-status-pulse");

		// The board-section tone stays visible while the pulse says the agent is busy.
		expect(sessionDot("ci failed task")).toHaveClass("bg-status-exited", "animate-status-pulse");
		expect(sessionDot("review task")).toHaveClass("bg-status-in-review", "animate-status-pulse");
		expect(sessionDot("ready task")).toHaveClass("bg-status-ready", "animate-status-pulse");
		expect(sessionDot("merged task")).toHaveClass("bg-status-merged", "animate-status-pulse");
	});

	it("blinks blue when an idle-section session has working activity", () => {
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [
						{
							...session,
							id: "proj-1-idle-working",
							title: "idle task receiving work",
							status: "idle",
							activity: { state: "active", lastActivityAt: "2026-06-30T00:00:00Z" },
						},
					],
				},
			],
		});

		const dot = screen
			.getByLabelText("Open idle task receiving work")
			.querySelector<HTMLElement>("[data-session-status]");
		expect(dot).toHaveClass("bg-status-working", "animate-status-pulse");
	});

	it("holds the dot still for idle activity and keeps its PR tone", async () => {
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [
						{
							...session,
							id: "proj-1-idle-activity",
							title: "idle activity task",
							status: "idle",
							activity: { state: "idle", lastActivityAt: "2026-06-30T00:00:00Z" },
						},
						{
							...session,
							id: "proj-1-idle-draft",
							title: "idle draft task",
							status: "draft",
							scmStatus: "draft",
							activity: { state: "idle", lastActivityAt: "2026-06-30T00:00:00Z" },
							prs: [sidebarPR({ state: "draft" })],
						},
					],
				},
			],
		});


		const idleActivityDot = screen
			.getByLabelText("Open idle activity task")
			.querySelector<HTMLElement>("span.rounded-full");
		const idleDraftDot = screen.getByLabelText("Open idle draft task").querySelector<HTMLElement>("span.rounded-full");

		// An idle session with no pull request stays gray; a parked draft keeps
		// the in-review tone the board gives it, without any motion.
		expect(idleActivityDot).toHaveClass("bg-status-idle");
		expect(idleDraftDot).toHaveClass("bg-status-in-review");
		expect(idleActivityDot).not.toHaveClass("animate-status-pulse");
		expect(idleDraftDot).not.toHaveClass("animate-status-pulse");
	});

	it("shows sessions on load and hides them once collapsed", async () => {
		const user = userEvent.setup();
		const workspaceWithSessions = {
			...workspace,
			sessions: [session, { ...session, id: "proj-1-2", title: "second task" }],
		};
		renderSidebar({ workspaces: [workspaceWithSessions] });

		expect(screen.getByLabelText("Open fix login")).toBeInTheDocument();
		expect(screen.getByLabelText("Open second task")).toBeInTheDocument();

		// Collapse via folder icon
		const folder = screen.getByLabelText("Toggle Project One sessions");
		expect(folder).toBeTruthy();
		await user.click(folder);

		expect(screen.queryByLabelText("Open fix login")).not.toBeInTheDocument();
		expect(screen.queryByLabelText("Open second task")).not.toBeInTheDocument();
	});

	it("starts every project collapsed when the expanded-project store is empty", () => {
		renderSidebar({
			expandedProjectIds: [],
			workspaces: [{ ...workspace, sessions: [session] }],
		});

		expect(screen.queryByLabelText("Open fix login")).not.toBeInTheDocument();
		expect(screen.getByLabelText("Toggle Project One sessions")).toHaveAttribute(
			"aria-expanded",
			"false",
		);
	});

	it("reveals the active project when opening a worker-session deep link", async () => {
		const user = userEvent.setup();
		mockParams.projectId = workspace.id;
		mockParams.sessionId = session.id;
		renderSidebar({
			expandedProjectIds: [],
			workspaces: [{ ...workspace, sessions: [session] }],
		});

		expect(screen.getByLabelText("Open fix login")).toBeInTheDocument();
		expect(screen.getByLabelText("Toggle Project One sessions")).toHaveAttribute(
			"aria-expanded",
			"true",
		);

		await user.click(screen.getByLabelText("Toggle Project One sessions"));
		expect(screen.queryByLabelText("Open fix login")).not.toBeInTheDocument();
		expect(screen.getByLabelText("Toggle Project One sessions")).toHaveAttribute(
			"aria-expanded",
			"false",
		);
	});

	it("restores only the projects saved as expanded and persists toggles", async () => {
		const user = userEvent.setup();
		const secondWorkspace = {
			...workspace,
			id: "proj-2",
			name: "Project Two",
			sessions: [{ ...session, id: "proj-2-1", title: "second task" }],
		};
		renderSidebar({
			expandedProjectIds: [workspace.id],
			workspaces: [{ ...workspace, sessions: [session] }, secondWorkspace],
		});

		expect(screen.getByLabelText("Open fix login")).toBeInTheDocument();
		expect(screen.queryByLabelText("Open second task")).not.toBeInTheDocument();

		await user.click(screen.getByLabelText("Toggle Project One sessions"));
		expect(JSON.parse(window.localStorage.getItem("ao.sidebar.expanded-projects") ?? "null")).toEqual([]);
	});

	it("hides all sessions when project is collapsed via folder icon", async () => {
		const user = userEvent.setup();
		mockParams.projectId = "proj-1";
		mockParams.sessionId = "proj-1-2";
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [session, { ...session, id: "proj-1-2", title: "second task" }],
				},
			],
		});

		const projectRow = screen.getByText("Project One").closest('button, [role="button"]')!;
		// Project starts expanded — sessions visible
		expect(screen.getByLabelText("Open second task")).toBeInTheDocument();
		expect(screen.getByLabelText("Open fix login")).toBeInTheDocument();
		expect(projectRow).toHaveAttribute("aria-expanded", "true");

		// Collapse via folder icon
		const folder = screen.getByLabelText("Toggle Project One sessions");
		expect(folder).toBeTruthy();
		await user.click(folder);

		expect(projectRow).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByLabelText("Open second task")).not.toBeInTheDocument();
		expect(screen.queryByLabelText("Open fix login")).not.toBeInTheDocument();
	});

	it("keeps merged sessions in the list until they are terminated", async () => {
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [
						{ ...session, id: "merged-live", title: "merged live task", status: "merged", isTerminated: false },
						{ ...session, id: "merged-done", title: "merged terminated task", status: "merged", isTerminated: true },
					],
				},
			],
		});


		expect(screen.getByLabelText("Open merged live task")).toBeInTheDocument();
		expect(screen.queryByLabelText("Open merged terminated task")).not.toBeInTheDocument();
	});

	it("confirms before archiving a session and names the action archive, not delete", async () => {
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

		const row = screen.getByLabelText("Open fix login").closest<HTMLElement>("[data-session-row]")!;
		const archiveButton = within(row).getByLabelText("Archive session");
		expect(archiveButton.querySelector("svg")).toHaveClass("lucide-archive");

		fireEvent.click(archiveButton);
		expect(postMock).not.toHaveBeenCalled();

		const dialog = await screen.findByRole("dialog", {
			name: "Are you sure you want to archive this session?",
		});
		expect(dialog).toHaveTextContent("You can always restore it from the Archive section later.");
		fireEvent.click(within(dialog).getByRole("button", { name: "No" }));
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		expect(postMock).not.toHaveBeenCalled();

		await confirmArchiveFromRow(row);

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith(
				"/api/v1/sessions/{sessionId}/kill",
				expect.objectContaining({ params: { path: { sessionId: "proj-1-1" } } }),
			),
		);
	});

	it("shifts to the adjacent session when deleting the active session", async () => {
		mockParams.projectId = "proj-1";
		mockParams.sessionId = "proj-1-2";
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [
						{ ...session, id: "proj-1-1", title: "first task", updatedAt: "2026-06-30T00:00:00Z" },
						{ ...session, id: "proj-1-2", title: "second task", updatedAt: "2026-06-30T01:00:00Z" },
					],
				},
			],
		});

		const row = screen.getByLabelText("Open second task").closest<HTMLElement>("[data-session-row]")!;
		await confirmArchiveFromRow(row);

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith(
				"/api/v1/sessions/{sessionId}/kill",
				expect.objectContaining({ params: { path: { sessionId: "proj-1-2" } } }),
			),
		);
		await waitFor(() =>
			expect(navigateMock).toHaveBeenCalledWith({
				to: "/projects/$projectId/sessions/$sessionId",
				params: { projectId: "proj-1", sessionId: "proj-1-1" },
			}),
		);
	});

	it("shifts to the orchestrator when killing the only worker session in a project with an active orchestrator", async () => {
		mockParams.projectId = "proj-1";
		mockParams.sessionId = "proj-1-1";
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [
						{ ...session, id: "proj-1-1", title: "sole worker" },
						{
							...session,
							id: "orch-1",
							title: "Orchestrator",
							kind: "orchestrator",
							isTerminated: false,
						},
					],
				},
			],
		});

		const row = screen.getByLabelText("Open sole worker").closest<HTMLElement>("[data-session-row]")!;
		await confirmArchiveFromRow(row);

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith(
				"/api/v1/sessions/{sessionId}/kill",
				expect.objectContaining({ params: { path: { sessionId: "proj-1-1" } } }),
			),
		);

		await waitFor(() =>
			expect(navigateMock).toHaveBeenCalledWith({
				to: "/projects/$projectId/sessions/$sessionId",
				params: { projectId: "proj-1", sessionId: "orch-1" },
			}),
		);
	});

	it("shifts to the project board when killing the only worker session in a project without an orchestrator", async () => {
		mockParams.projectId = "proj-1";
		mockParams.sessionId = "proj-1-1";
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [{ ...session, id: "proj-1-1", title: "sole worker" }],
				},
			],
		});

		const row = screen.getByLabelText("Open sole worker").closest<HTMLElement>("[data-session-row]")!;
		await confirmArchiveFromRow(row);

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith(
				"/api/v1/sessions/{sessionId}/kill",
				expect.objectContaining({ params: { path: { sessionId: "proj-1-1" } } }),
			),
		);

		await waitFor(() =>
			expect(navigateMock).toHaveBeenCalledWith({
				to: "/projects/$projectId",
				params: { projectId: "proj-1" },
			}),
		);
	});

	it("does not navigate away when killing an inactive session", async () => {
		mockParams.projectId = "proj-1";
		mockParams.sessionId = "proj-1-1";
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [
						{ ...session, id: "proj-1-1", title: "active task", updatedAt: "2026-06-30T00:00:00Z" },
						{ ...session, id: "proj-1-2", title: "inactive task", updatedAt: "2026-06-30T01:00:00Z" },
					],
				},
			],
		});

		const row = screen.getByLabelText("Open inactive task").closest<HTMLElement>("[data-session-row]")!;
		await confirmArchiveFromRow(row);

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith(
				"/api/v1/sessions/{sessionId}/kill",
				expect.objectContaining({ params: { path: { sessionId: "proj-1-2" } } }),
			),
		);
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("shifts to the next session when killing an active pinned session", async () => {
		mockParams.projectId = "proj-1";
		mockParams.sessionId = "proj-1-2";
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [
						{ ...session, id: "proj-1-1", title: "first task", isPinned: false, updatedAt: "2026-06-30T00:00:00Z" },
						{ ...session, id: "proj-1-2", title: "pinned task", isPinned: true, pinnedAt: "2026-06-30T01:00:00Z", updatedAt: "2026-06-30T01:00:00Z" },
					],
				},
			],
		});

		const pinnedList = screen.getByTestId("pinned-session-list");
		const row = within(pinnedList).getByLabelText("Open pinned task").closest<HTMLElement>("[data-session-row]")!;
		await confirmArchiveFromRow(row);

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith(
				"/api/v1/sessions/{sessionId}/kill",
				expect.objectContaining({ params: { path: { sessionId: "proj-1-2" } } }),
			),
		);
		await waitFor(() =>
			expect(navigateMock).toHaveBeenCalledWith({
				to: "/projects/$projectId/sessions/$sessionId",
				params: { projectId: "proj-1", sessionId: "proj-1-1" },
			}),
		);
	});

	it("keeps pinned sessions with the same ID distinct across local and two hosts", () => {
		mockParams.hostId = "box-b";
		mockParams.projectId = "proj-1";
		mockParams.sessionId = "shared";
		const pinned = { ...session, id: "shared", title: "shared task", isPinned: true, pinnedAt: "2026-06-30T01:00:00Z" };
		renderSidebar({
			workspaces: [{ ...workspace, sessions: [pinned] }],
			remoteHosts: [
				{ hostId: "box-a", label: "Host A", url: "http://box-a:3011", status: "connected" },
				{ hostId: "box-b", label: "Host B", url: "http://box-b:3011", status: "connected" },
			],
			remoteWorkspaces: [
				{ ...workspace, hostId: "box-a", sessions: [{ ...pinned, hostId: "box-a" }] },
				{ ...workspace, hostId: "box-b", sessions: [{ ...pinned, hostId: "box-b" }] },
			],
		});
		const list = screen.getByTestId("pinned-session-list");
		const hostA = within(list).getByText("Host A").closest<HTMLElement>("[data-session-row]")!;
		const hostB = within(list).getByText("Host B").closest<HTMLElement>("[data-session-row]")!;
		const local = within(list).getByRole("button", { name: "Open shared task" });
		expect(within(hostA).getByRole("button", { name: "Open shared task · Host A" })).not.toHaveAttribute("aria-current");
		expect(within(hostB).getByRole("button", { name: "Open shared task · Host B" })).toHaveAttribute("aria-current", "page");
		fireEvent.click(within(hostA).getByRole("button", { name: "Open shared task · Host A" }));
		expect(navigateMock).toHaveBeenLastCalledWith({ to: "/host/$hostId/project/$projectId/session/$sessionId", params: { hostId: "box-a", projectId: "proj-1", sessionId: "shared" } });
		fireEvent.click(within(hostB).getByRole("button", { name: "Open shared task · Host B" }));
		expect(navigateMock).toHaveBeenLastCalledWith({ to: "/host/$hostId/project/$projectId/session/$sessionId", params: { hostId: "box-b", projectId: "proj-1", sessionId: "shared" } });
		fireEvent.click(local);
		expect(navigateMock).toHaveBeenLastCalledWith({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "proj-1", sessionId: "shared" } });
	});

	it("shifts to the adjacent session when killing an active pinned session with multiple remaining sessions", async () => {
		mockParams.projectId = "proj-1";
		mockParams.sessionId = "proj-1-1";
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [
						{ ...session, id: "proj-1-3", title: "newest task", updatedAt: "2026-06-30T02:00:00Z" },
						{ ...session, id: "proj-1-2", title: "middle task", updatedAt: "2026-06-30T01:00:00Z" },
						{ ...session, id: "proj-1-1", title: "oldest pinned task", isPinned: true, pinnedAt: "2026-06-30T00:00:00Z", updatedAt: "2026-06-30T00:00:00Z" },
					],
				},
			],
		});

		const pinnedList = screen.getByTestId("pinned-session-list");
		const row = within(pinnedList).getByLabelText("Open oldest pinned task").closest<HTMLElement>("[data-session-row]")!;
		await confirmArchiveFromRow(row);

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith(
				"/api/v1/sessions/{sessionId}/kill",
				expect.objectContaining({ params: { path: { sessionId: "proj-1-1" } } }),
			),
		);
		await waitFor(() =>
			expect(navigateMock).toHaveBeenCalledWith({
				to: "/projects/$projectId/sessions/$sessionId",
				params: { projectId: "proj-1", sessionId: "proj-1-2" },
			}),
		);
	});

	it("navigates optimistically when killing the active session", async () => {
		mockParams.projectId = "proj-1";
		mockParams.sessionId = "proj-1-1";
		let resolveKill: (() => void) | undefined;
		postMock.mockImplementation(
			() =>
				new Promise((resolve) => {
					resolveKill = () => resolve({ data: { ok: true }, error: undefined, response: new Response(null, { status: 200 }) });
				}),
		);
		renderSidebar({
			workspaces: [
				{
					...workspace,
					sessions: [
						{ ...session, id: "proj-1-1", title: "first task", updatedAt: "2026-06-30T00:00:00Z" },
					],
				},
			],
		});

		const row = screen.getByLabelText("Open first task").closest<HTMLElement>("[data-session-row]")!;
		await confirmArchiveFromRow(row);

		// Navigation occurs optimistically on click rather than waiting for daemon round-trip.
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId",
			params: { projectId: "proj-1" },
		});

		resolveKill?.();
	});

	it("commits a project drop", () => {
		renderSidebar({
			workspaces: [
				{ ...workspace, id: "alpha", name: "Alpha" },
				{ ...workspace, id: "bravo", name: "Bravo" },
			],
		});

		const bravoRow = document.querySelector('[data-project-drag-row][data-project-id="bravo"]')!;
		const alphaTarget = document.querySelector('li[data-project-drop-target][data-project-id="alpha"]')!;
		fireDrag("dragStart", bravoRow, {});
		// jsdom rows measure as zero-height, so clientY 0 lands in the top half — drop before Alpha.
		fireDrag("dragOver", alphaTarget, { clientY: 0 });
		fireDrag("drop", alphaTarget, {});

		expect(Array.from(document.querySelectorAll("[data-project-label]"), (node) => node.textContent)).toEqual(["Bravo", "Alpha"]);
		expect(JSON.parse(window.localStorage.getItem("ao.sidebar.project-order") ?? "null")).toEqual(["bravo", "alpha"]);
	});

	it("keeps the ad hoc group out of the project list and its drag ordering", () => {
		renderSidebar({
			workspaces: [
				{ ...workspace, id: "alpha", name: "Alpha" },
				{ ...workspace, id: "bravo", name: "Bravo" },
				{
					id: STANDALONE_WORKSPACE_ID,
					name: "Scratchpad",
					kind: STANDALONE_PROJECT_KIND,
					path: "",
					sessions: [],
				},
			],
		});
		const labels = () => Array.from(document.querySelectorAll("[data-project-label]"), (node) => node.textContent);

		// The ad hoc group is its own section, never a row in the project list.
		expect(labels()).toEqual(["Alpha", "Bravo"]);
		expect(document.querySelector(`li[data-project-id="${STANDALONE_WORKSPACE_ID}"]`)).toBeNull();
		expect(screen.getByRole("button", { name: "Scratchpad" })).toBeInTheDocument();

		const bravoRow = document.querySelector('[data-project-drag-row][data-project-id="bravo"]')!;
		const alphaTarget = document.querySelector('li[data-project-drop-target][data-project-id="alpha"]')!;
		fireDrag("dragStart", bravoRow, {});
		// jsdom rows measure as zero-height, so clientY 0 lands in the top half — drop before Alpha.
		fireDrag("dragOver", alphaTarget, { clientY: 0 });
		fireDrag("drop", alphaTarget, {});
		expect(labels()).toEqual(["Bravo", "Alpha"]);
	});

	it("commits a session drop within its project", () => {
		renderSidebar({
			workspaces: [{
				...workspace,
				sessions: [
					{ ...session, id: "first", title: "First", updatedAt: "2026-06-30T01:00:00Z" },
					{ ...session, id: "second", title: "Second", updatedAt: "2026-06-30T00:00:00Z" },
				],
			}],
		});

		act(() => dragEnds.get("sidebar-sessions-proj-1")?.({ active: { id: "second" }, over: { id: "first" } }));

		expect(Array.from(document.querySelectorAll('[data-testid="session-list-proj-1"] button[aria-label^="Open "]'), (node) => node.getAttribute("aria-label"))).toEqual([
			"Open Second",
			"Open First",
		]);
	});

	it.each(["light", "dark"] as const)("uses a visible project drop indicator in the %s theme", (theme) => {
		document.documentElement.classList.toggle("dark", theme === "dark");
		try {
			renderSidebar({
				workspaces: [
					{ ...workspace, id: "alpha", name: "Alpha" },
					{ ...workspace, id: "bravo", name: "Bravo" },
				],
			});

			const bravoRow = document.querySelector('[data-project-drag-row][data-project-id="bravo"]')!;
			const alphaTarget = document.querySelector('li[data-project-drop-target][data-project-id="alpha"]')!;
			fireDrag("dragStart", bravoRow, {});
			fireDrag("dragOver", alphaTarget, { clientY: 0 });

			const indicator = document.querySelector("[data-project-drop-line]");
			expect(indicator).not.toBeNull();
			expect(indicator).toHaveClass("bg-foreground");
			expect(indicator).not.toHaveClass("bg-white");
		} finally {
			document.documentElement.classList.remove("dark");
		}
	});
});
