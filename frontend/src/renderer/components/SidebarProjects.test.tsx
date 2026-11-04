import {
	settingsLayer,
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
	exitedOrchestrator,
	CreateProjectHandler,
	CloneProjectHandler,
	InitializeProjectHandler,
	RemoveProjectHandler,
	repoStatus,
	importValidation,
	renderSidebar,
	chooseOption,
	codedError,
	openCreateProjectDialog,
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
import {
	STANDALONE_PROJECT_KIND,
	STANDALONE_WORKSPACE_ID,
	type WorkspaceSession,
	type WorkspaceSummary,
} from "../types/workspace";
import { agentReadiness } from "../test/agent-readiness-fixtures";
import { sessionInterfaceTransitionStatus } from "../test/interface-transition-fixtures";
import { useUiStore } from "../stores/ui-store";
import { useSidebarOrderStore } from "../stores/sidebar-order-store";
import { sessionInterfaceTransitionQueryKey } from "../hooks/useSessionInterfaceTransition";

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
	it("creates local or remote projects from the header button", async () => {
		const user = userEvent.setup();
		renderSidebar({
			workspaces: [],
			remoteHosts: [
				{ hostId: "box-a", label: "Host A", url: "http://box-a:3011", status: "connected" },
				{ hostId: "box-b", label: "Host B", url: "http://box-b:3011", status: "connected" },
			],
		});

		await user.click(screen.getByRole("button", { name: "New project" }));
		const chooser = await screen.findByRole("dialog");
		expect(screen.queryByRole("button", { name: "Add project on Host B" })).not.toBeInTheDocument();
		await user.click(within(chooser).getByRole("combobox", { name: "Computer" }));
		await user.click(screen.getByRole("option", { name: "Host B" }));
		expect(screen.getByRole("dialog")).toBe(chooser);
		expect(within(chooser).getByRole("combobox", { name: "Computer" })).toHaveTextContent("Host B");
		await user.click(within(chooser).getByRole("combobox", { name: "Computer" }));
		await user.click(screen.getByRole("option", { name: "This computer" }));
		expect(screen.getByRole("dialog")).toBe(chooser);
	});

	it("lists remote projects beside local and Cloud projects with host-qualified sessions", () => {
		mockParams.hostId = "box-b";
		mockParams.projectId = "proj-1";
		mockParams.sessionId = "proj-1-1";
		const remoteSession = (hostId: string, title: string): WorkspaceSession => ({
			...session, hostId, title,
		});
		renderSidebar({
			workspaces: [{ ...workspace, sessions: [session] }],
			remoteHosts: [
				{ hostId: "box-a", label: "Host A", url: "http://box-a:3101", status: "connected" },
				{ hostId: "box-b", label: "Host B", url: "http://box-b:3101", status: "connected" },
			],
			remoteWorkspaces: [
				{ ...workspace, hostId: "box-a", sessions: [remoteSession("box-a", "Task on A")] },
				{ ...workspace, hostId: "box-b", sessions: [remoteSession("box-b", "Task on B")] },
			],
		});

		const projects = screen.getByTestId("sidebar-projects-scroller");
		expect(within(projects).getByText("Host A")).toBeInTheDocument();
		expect(within(projects).getByText("Host B")).toBeInTheDocument();
		expect(screen.queryByText("Remote hosts")).not.toBeInTheDocument();
		const remoteSessions = within(projects).getAllByTestId("remote-session-row");
		expect(remoteSessions).toHaveLength(2);
		expect(remoteSessions[0]).not.toHaveAttribute("aria-current");
		expect(remoteSessions[1]).toHaveAttribute("aria-current", "page");
		fireEvent.click(remoteSessions[0]);
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/host/$hostId/project/$projectId/session/$sessionId",
			params: { hostId: "box-a", projectId: "proj-1", sessionId: "proj-1-1" },
		});
	});



	it("hides cloud account controls when the daemon reports the cloud offering off", () => {
		cloudGateState.cloudEnabled = false;
		cloudSessionState.configured = true;
		cloudSessionState.status = "authenticated";
		cloudSessionState.session = { user: { email: "user@example.com" } };
		renderSidebar();

		expect(screen.queryByLabelText("Signed in as user@example.com")).not.toBeInTheDocument();
	});


	it("has no page rows: Automations, Artifacts and Backlog live on the icon rail", () => {
		mockParams.projectId = "proj-1";
		renderSidebar();

		expect(screen.queryByRole("button", { name: "Automations" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Artifacts" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /^Backlog/ })).not.toBeInTheDocument();
	});

	it("shows the Inbox list instead of the project tree on /inbox", () => {
		mockLocation.pathname = "/inbox";
		renderSidebar();
		expect(screen.getByTestId("inbox-list")).toBeInTheDocument();
		expect(screen.queryByTestId("sessions-panel-new")).toBeNull();
	});

	it("keeps the project tree off the Inbox list on other routes", () => {
		mockLocation.pathname = "/sessions";
		renderSidebar();
		expect(screen.queryByTestId("inbox-list")).toBeNull();
	});

	it("shows no Sessions title or + New on the settings layer, only the panel toggle", () => {
		settingsLayer.current = { cueBusy: false, close: vi.fn() };
		try {
			renderSidebar();

			expect(screen.getByTestId("settings-nav")).toBeInTheDocument();
			expect(screen.queryByRole("heading", { name: "Sessions" })).toBeNull();
			expect(screen.queryByTestId("sessions-panel-new")).toBeNull();
			expect(screen.getAllByRole("button", { name: "Toggle sessions panel" })).toHaveLength(1);
		} finally {
			settingsLayer.current = null;
		}
	});

	it("has one panel toggle while open: the header's", () => {
		renderSidebar();

		expect(screen.getAllByRole("button", { name: "Toggle sessions panel" })).toHaveLength(1);
		expect(screen.queryByRole("button", { name: "Collapse sidebar" })).toBeNull();
	});

	it("suppresses focus chrome without removing keyboard focusability", () => {
		renderSidebar();

		expect(document.querySelector('[data-slot="sidebar-container"]')).toHaveClass("sidebar-focusless");
		expect(screen.getByTestId("awake-button")).toHaveAttribute("tabindex", "0");
	});

	it("keeps the Settings footer flush with the bottom edge", () => {
		renderSidebar();

		const footer = document.querySelector('[data-sidebar="footer"]');
		expect(footer).toHaveClass("border-t", "border-border-strong", "!py-2");
		expect(footer?.className).not.toContain("--size-center-panel-bottom-inset");
		expect(footer?.className).not.toContain("--size-center-panel-inset-mac");
	});

	it("keeps the footer controls keyboard-accessible only while the panel is open", () => {
		renderSidebar();
		expect(screen.getByTestId("awake-button")).toHaveAttribute("tabindex", "0");
	});

	it("takes the footer controls out of the tab order while the panel is closed", () => {
		renderSidebar({ initialOpen: false });
		expect(screen.getByTestId("awake-button")).toHaveAttribute("tabindex", "-1");
		// The panel content is off-screen while closed: inert + aria-hidden, but the
		// edge strip that reopens it stays live.
		const inertRegion = screen.getByTestId("awake-button").closest("[inert]");
		expect(inertRegion).toHaveAttribute("aria-hidden", "true");
		expect(inertRegion?.closest('[data-slot="sidebar-container"]')).not.toBeNull();
		expect(document.querySelector('[data-sidebar="rail"]')?.closest("[inert]")).toBeNull();
	});

	it("keeps sidebar scrolling functional without a layout gutter", () => {
		renderSidebar();

		const content = document.querySelector('[data-sidebar="content"]');
		expect(content).toHaveClass("overflow-y-auto", "scrollbar-none");
		expect(content).not.toHaveClass("project-sidebar-scrollbar");
		expect(content).not.toContainElement(screen.getByText("Projects"));
	});

	it("opens project settings instead of spawning when no orchestrator agent is configured", async () => {
		const user = userEvent.setup();
		renderSidebar({ workspaces: [{ ...workspace, orchestratorAgent: undefined }] });

		await user.click(screen.getByRole("button", { name: "Spawn Project One orchestrator" }));

		expect(useUiStore.getState().settingsModal).toEqual({ scope: "project", projectId: "proj-1" });
		expect(navigateMock).not.toHaveBeenCalled();
		expect(spawnMock).not.toHaveBeenCalled();
	});

	// The sidebar owns its own openOrchestrator, separate from
	// useProjectOrchestratorAction. Clicking Orchestrator on an orchestrator whose
	// agent exited must resume it, not navigate to the dead terminal.
	it("resumes an exited orchestrator instead of opening its dead terminal", async () => {
		const user = userEvent.setup();
		renderSidebar({ workspaces: [{ ...workspace, sessions: [exitedOrchestrator] }] });

		await user.click(screen.getByRole("button", { name: "Open Project One orchestrator" }));

		await waitFor(() => expect(resumeOrchestratorMock).toHaveBeenCalledWith("proj-1-orch"));
		expect(spawnMock).not.toHaveBeenCalled();
	});

	it("opens an exited orchestrator at once and reports a failed resume", async () => {
		const user = userEvent.setup();
		const error = new Error("resume request failed");
		useUiStore.getState().clearGlobalToast();
		vi.spyOn(console, "error").mockImplementation(() => {});
		resumeOrchestratorMock.mockRejectedValueOnce(error);
		renderSidebar({ workspaces: [{ ...workspace, sessions: [exitedOrchestrator] }] });

		await user.click(screen.getByRole("button", { name: "Open Project One orchestrator" }));

		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "proj-1-orch" },
		});
		await waitFor(() => expect(console.error).toHaveBeenCalledWith("Failed to resume orchestrator:", error));
		expect(useUiStore.getState().globalToast).toMatchObject({
			title: "Resume agent",
			body: "resume request failed",
			tone: "error",
		});
	});

	it("opens an exited orchestrator without resuming during an interface transition", async () => {
		const user = userEvent.setup();
		renderSidebar({
			workspaces: [{ ...workspace, sessions: [exitedOrchestrator] }],
			seed: (client) => {
				client.setQueryData(
					sessionInterfaceTransitionQueryKey(exitedOrchestrator.id),
					sessionInterfaceTransitionStatus(exitedOrchestrator.id),
				);
			},
		});

		await user.click(screen.getByRole("button", { name: "Open Project One orchestrator" }));

		expect(resumeOrchestratorMock).not.toHaveBeenCalled();
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "proj-1-orch" },
		});
	});

	it("does not spawn from the sidebar while the orchestrator is provisioning", async () => {
		const user = userEvent.setup();
		useUiStore.getState().setProjectProvisioning("proj-1", true);
		try {
			renderSidebar();

			const spawnButton = screen.getByRole("button", { name: "Spawn Project One orchestrator" });
			expect(spawnButton).toBeDisabled();
			await user.click(spawnButton);

			expect(spawnMock).not.toHaveBeenCalled();
			expect(navigateMock).not.toHaveBeenCalled();
		} finally {
			useUiStore.getState().setProjectProvisioning("proj-1", false);
		}
	});

	it("shows a ConfirmDialog and calls onRemoveProject when confirmed", async () => {
		mockParams.projectId = "proj-1";
		const user = userEvent.setup();
		const onRemoveProject = renderSidebar();

		await user.click(screen.getByLabelText("Project actions for Project One"));
		await user.click(await screen.findByRole("menuitem", { name: "Remove project" }));

		// The ConfirmDialog renders via Radix Portal — find it by role
		const dialog = await screen.findByRole("dialog", { name: "Remove project" });
		expect(dialog).toBeInTheDocument();
		expect(dialog).toHaveTextContent("Project One");

		await user.click(screen.getByRole("button", { name: "Remove" }));
		await waitFor(() => expect(onRemoveProject).toHaveBeenCalledTimes(1));
		expect(navigateMock).toHaveBeenCalledWith({ to: "/" });
	});

	it("keeps focus when removing a project that is not focused", async () => {
		mockParams.projectId = "proj-2";
		const user = userEvent.setup();
		const onRemoveProject = renderSidebar();
		await user.click(screen.getByLabelText("Project actions for Project One"));
		await user.click(await screen.findByRole("menuitem", { name: "Remove project" }));
		await user.click(await screen.findByRole("button", { name: "Remove" }));
		await waitFor(() => expect(onRemoveProject).toHaveBeenCalledTimes(1));
		expect(navigateMock).not.toHaveBeenCalledWith({ to: "/" });
	});

	it("dismisses project removal immediately and shows progress outside the modal", async () => {
		mockParams.projectId = "proj-1";
		let finishRemoval!: () => void;
		const onRemoveProject = vi.fn(
			() =>
				new Promise<void>((resolve) => {
					finishRemoval = resolve;
				}),
		) as RemoveProjectHandler;
		const user = userEvent.setup();
		renderSidebar({ onRemoveProject });

		await user.click(screen.getByLabelText("Project actions for Project One"));
		await user.click(await screen.findByRole("menuitem", { name: "Remove project" }));
		await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Remove" }));

		expect(screen.queryByRole("dialog", { name: "Remove project" })).not.toBeInTheDocument();
		expect(navigateMock).toHaveBeenCalledWith({ to: "/" });
		expect(screen.getByRole("status")).toHaveTextContent("Removing Project One");

		finishRemoval();
		await waitFor(() => expect(screen.queryByRole("status")).not.toBeInTheDocument());
	});

	it("does not remove the project when cancellation is clicked in the ConfirmDialog", async () => {
		const user = userEvent.setup();
		const onRemoveProject = renderSidebar();

		await user.click(screen.getByLabelText("Project actions for Project One"));
		await user.click(await screen.findByRole("menuitem", { name: "Remove project" }));

		await screen.findByRole("dialog", { name: "Remove project" });
		await user.click(screen.getByRole("button", { name: "Cancel" }));

		// Dialog should close and the handler must not have fired
		await waitFor(() => expect(screen.queryByRole("dialog", { name: "Remove project" })).not.toBeInTheDocument());
		expect(onRemoveProject).not.toHaveBeenCalled();
	});

	it("keeps the removal dialog dismissed and surfaces failures in the sidebar", async () => {
		mockParams.projectId = "proj-1";
		const user = userEvent.setup();
		const onRemoveProject = vi
			.fn()
			.mockRejectedValueOnce(new Error("Failed to remove project")) as RemoveProjectHandler;
		renderSidebar({ onRemoveProject });

		await user.click(screen.getByLabelText("Project actions for Project One"));
		await user.click(await screen.findByRole("menuitem", { name: "Remove project" }));
		await screen.findByRole("dialog", { name: "Remove project" });
		await user.click(screen.getByRole("button", { name: "Remove" }));

		expect(await screen.findByText("Failed to remove project")).toBeInTheDocument();
		expect(screen.queryByRole("dialog", { name: "Remove project" })).not.toBeInTheDocument();
		expect(navigateMock).toHaveBeenCalledWith({ to: "/" });
	});

	it("requests a new task for the project from the kebab menu", async () => {
		const user = userEvent.setup();
		renderSidebar();
		const before = useUiStore.getState().newTaskRequest?.nonce ?? 0;

		await user.click(screen.getByLabelText("Project actions for Project One"));
		await user.click(await screen.findByRole("menuitem", { name: /New task/ }));

		const request = useUiStore.getState().newTaskRequest;
		expect(request?.projectId).toBe("proj-1");
		expect(request?.nonce ?? 0).toBeGreaterThan(before);
	});

	it("opens the standalone board from the Scratchpad archived sessions action", () => {
		renderSidebar({
			workspaces: [
				{
					id: STANDALONE_WORKSPACE_ID,
					name: "Scratchpad",
					kind: STANDALONE_PROJECT_KIND,
					path: "",
					sessions: [
						{
							...session,
							id: "adhoc-archived",
							title: "archived",
							workspaceId: STANDALONE_WORKSPACE_ID,
							workspaceName: "Scratchpad",
							isTerminated: true,
							status: "terminated",
						},
					],
				},
			],
		});

		const archiveAction = screen.getByRole("button", { name: "Archived sessions" }).closest("[data-scratchpad-archive-action]");
		expect(archiveAction).toHaveClass("opacity-0", "scale-[0.8]");
		fireEvent.click(screen.getByRole("button", { name: "Archived sessions" }));

		expect(navigateMock).toHaveBeenCalledWith({ to: "/sessions" });
	});

	it("opens a new ad hoc agent directly from the ad hoc row action", async () => {
		const user = userEvent.setup();
		renderSidebar({
			workspaces: [
				{
					id: STANDALONE_WORKSPACE_ID,
					name: "Scratchpad",
					kind: STANDALONE_PROJECT_KIND,
					path: "",
					sessions: [],
				},
			],
		});
		const before = useUiStore.getState().newTaskRequest?.nonce ?? 0;

		expect(screen.queryByLabelText("Project actions for Scratchpad")).not.toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Open a new agent" }));

		const request = useUiStore.getState().newTaskRequest;
		expect(request?.projectId).toBe(STANDALONE_WORKSPACE_ID);
		expect(request?.nonce ?? 0).toBeGreaterThan(before);
	});

	it("lists ad hoc agents in their own Scratchpad section, not under a project row", () => {
		renderSidebar({
			workspaces: [
				workspace,
				{
					id: STANDALONE_WORKSPACE_ID,
					name: "Scratchpad",
					kind: STANDALONE_PROJECT_KIND,
					path: "",
					sessions: [
						{ ...session, id: "adhoc-1", title: "baby", workspaceId: STANDALONE_WORKSPACE_ID, workspaceName: "Scratchpad" },
					],
				},
			],
		});

		// Section header, not a project row.
		expect(screen.getByRole("button", { name: "Scratchpad" })).toBeInTheDocument();
		expect(document.querySelector(`li[data-project-id="${STANDALONE_WORKSPACE_ID}"]`)).toBeNull();
		const section = document.querySelector("[data-scratchpad-section]")!;
		expect(section).toContainElement(screen.getByText("baby"));
		expect(section).toHaveClass("mb-2", "min-h-0");
		expect(screen.queryByRole("button", { name: /show (more|fewer) agents/i })).not.toBeInTheDocument();
		expect(screen.getByTestId("sidebar-projects-scroller")).not.toContainElement(screen.getByText("baby"));
	});

	it("optically centers the Project and Scratchpad header add icons", () => {
		renderSidebar({
			workspaces: [
				workspace,
				{
					id: STANDALONE_WORKSPACE_ID,
					name: "Scratchpad",
					kind: STANDALONE_PROJECT_KIND,
					path: "",
					sessions: [],
				},
			],
		});

		for (const label of ["New project", "Open a new agent"]) {
			// Shared 24px row-action button; the icon is centered by the grid, no optical nudge.
			expect(screen.getByRole("button", { name: label })).toHaveClass("size-6", "place-items-center");
		}
	});

	it("opens an ad hoc agent on its own session route", async () => {
		const user = userEvent.setup();
		renderSidebar({
			workspaces: [
				{
					id: STANDALONE_WORKSPACE_ID,
					name: "Scratchpad",
					kind: STANDALONE_PROJECT_KIND,
					path: "",
					sessions: [
						{ ...session, id: "adhoc-1", title: "baby", workspaceId: STANDALONE_WORKSPACE_ID, workspaceName: "Scratchpad" },
					],
				},
			],
		});

		await user.click(screen.getByText("baby"));

		expect(navigateMock).toHaveBeenCalledWith({ to: "/sessions/$sessionId", params: { sessionId: "adhoc-1" } });
	});

	it("collapses the Scratchpad section from its header", async () => {
		const user = userEvent.setup();
		renderSidebar({
			workspaces: [
				{
					id: STANDALONE_WORKSPACE_ID,
					name: "Scratchpad",
					kind: STANDALONE_PROJECT_KIND,
					path: "",
					sessions: [
						{ ...session, id: "adhoc-1", title: "baby", workspaceId: STANDALONE_WORKSPACE_ID, workspaceName: "Scratchpad" },
					],
				},
			],
		});

		await user.click(screen.getByRole("button", { name: "Scratchpad" }));
		expect(screen.queryByText("baby")).not.toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Scratchpad" }));
		expect(screen.getByText("baby")).toBeInTheDocument();
	});

	it("caps the ad hoc agent list at 10 and lets the section grow to half the available height", async () => {
		const user = userEvent.setup();
		renderSidebar({
			workspaces: [
				{
					id: STANDALONE_WORKSPACE_ID,
					name: "Scratchpad",
					kind: STANDALONE_PROJECT_KIND,
					path: "",
					sessions: Array.from({ length: 13 }, (_, index) => ({
						...session,
						id: `adhoc-${index + 1}`,
						title: `Agent ${index + 1}`,
						workspaceId: STANDALONE_WORKSPACE_ID,
						workspaceName: "Scratchpad",
						// Descending so sortedWorkerSessions keeps the fixture order.
						updatedAt: `2026-06-${30 - index}T00:00:00Z`,
					})),
				},
			],
		});

		expect(screen.getByText("Agent 10")).toBeInTheDocument();
		expect(screen.queryByText("Agent 11")).not.toBeInTheDocument();

		const scroller = screen.getByTestId("sidebar-scratchpad-scroller");
		expect(scroller).toHaveClass("overflow-y-auto");
		expect(scroller.style.height).toBe("");
		expect(scroller.style.maxHeight).toBe("");
		expect(document.querySelector<HTMLElement>("[data-scratchpad-section]")!.style.maxHeight).toBe("");

		await user.click(screen.getByRole("button", { name: "Show 3 more agents" }));

		expect(screen.getByText("Agent 11")).toBeInTheDocument();
		expect(screen.getByText("Agent 13")).toBeInTheDocument();
		expect(scroller.style.maxHeight).toBe("");
		expect(screen.getByRole("button", { name: "Show fewer agents" })).toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Show fewer agents" }));

		expect(screen.queryByText("Agent 11")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Show 3 more agents" })).toBeInTheDocument();
	});

	it("shows scroll-edge fades on project and Scratchpad sections as they scroll", () => {
		renderSidebar({
			workspaces: [
				...Array.from({ length: 2 }, (_, index) => ({
					...workspace,
					id: `proj-${index + 1}`,
					name: `Project ${index + 1}`,
				})),
				{
					id: STANDALONE_WORKSPACE_ID,
					name: "Scratchpad",
					kind: STANDALONE_PROJECT_KIND,
					path: "",
					sessions: [{ ...session, id: "adhoc-1", title: "baby", workspaceId: STANDALONE_WORKSPACE_ID, workspaceName: "Scratchpad" }],
				},
			],
		});

		for (const testId of ["sidebar-projects-scroller", "sidebar-scratchpad-scroller"]) {
			const scroller = screen.getByTestId(testId);
			Object.defineProperties(scroller, {
				scrollHeight: { configurable: true, value: 200 },
				clientHeight: { configurable: true, value: 100 },
				scrollTop: { configurable: true, writable: true, value: 0 },
			});

			fireEvent.scroll(scroller);
			expect(scroller.parentElement?.querySelector(".sidebar-section-scroll-fade--bottom")).toBeInTheDocument();
			expect(scroller.parentElement?.querySelector(".sidebar-section-scroll-fade--top")).not.toBeInTheDocument();

			scroller.scrollTop = 50;
			fireEvent.scroll(scroller);
			expect(scroller.parentElement?.querySelector(".sidebar-section-scroll-fade--top")).toBeInTheDocument();
			expect(scroller.parentElement?.querySelector(".sidebar-section-scroll-fade--bottom")).toBeInTheDocument();

			scroller.scrollTop = 100;
			fireEvent.scroll(scroller);
			expect(scroller.parentElement?.querySelector(".sidebar-section-scroll-fade--top")).toBeInTheDocument();
			expect(scroller.parentElement?.querySelector(".sidebar-section-scroll-fade--bottom")).not.toBeInTheDocument();
		}
	});

	it("offers ad hoc agent creation from the project add flow before the ad hoc row exists", async () => {
		const user = userEvent.setup();
		renderSidebar();
		const before = useUiStore.getState().newTaskRequest?.nonce ?? 0;

		await user.click(screen.getByLabelText("New project"));
		await user.click(await screen.findByRole("button", { name: "Start a standalone agent" }));

		const request = useUiStore.getState().newTaskRequest;
		expect(request?.projectId).toBe(STANDALONE_WORKSPACE_ID);
		expect(request?.nonce ?? 0).toBeGreaterThan(before);
	});

	it("opens the create-project flow when the no-project shortcut signal arrives", async () => {
		renderSidebar();

		act(() => {
			useUiStore.getState().requestCreateProject();
		});

		expect(await screen.findByRole("dialog", { name: "Add a project" })).toBeInTheDocument();
	});

	it("keeps the create-project shortcut available when there are no projects", async () => {
		renderSidebar({ workspaces: [] });

		act(() => {
			useUiStore.getState().requestCreateProject();
		});

		expect(await screen.findByRole("dialog", { name: "Add a project" })).toBeInTheDocument();
	});

	it("reveals orchestrator and kebab buttons on the project row (no dashboard button)", () => {
		renderSidebar();

		expect(screen.queryByLabelText("Open Project One dashboard")).not.toBeInTheDocument();
		expect(screen.getByLabelText("Spawn Project One orchestrator")).toBeInTheDocument();
		expect(screen.getByLabelText("Project actions for Project One")).toBeInTheDocument();
	});

	it("keeps project row actions in the keyboard tab order without a duplicate disclosure stop", () => {
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

		const disclosure = screen.getByLabelText("Toggle Project One sessions");
		expect(disclosure.tagName).toBe("BUTTON");
		// The overlay folder button duplicates the project row's tab stop.
		expect(disclosure).toHaveProperty("tabIndex", -1);
		expect(screen.getByLabelText("Spawn Project One orchestrator")).toHaveProperty("tabIndex", 0);
		expect(screen.getByLabelText("Project actions for Project One")).toHaveProperty("tabIndex", 0);
		expect(screen.getByLabelText("Pin session")).toHaveProperty("tabIndex", 0);
		expect(screen.queryByRole("button", { name: "Rename fix login" })).not.toBeInTheDocument();
		expect(screen.getByLabelText("Archive session")).toHaveProperty("tabIndex", 0);
	});

	it("fades the message age out in favor of the overlaid hover actions", () => {
		const lastUserMessageAt = "2026-06-29T23:55:00Z";
		renderSidebar({
			workspaces: [{ ...workspace, sessions: [{ ...session, lastUserMessageAt }] }],
		});

		const openSession = screen.getByLabelText("Open fix login");
		const label = within(openSession).getByText("fix login");
		const actions = screen.getByLabelText("Pin session").closest("[data-session-actions]");
		const actionButtons = screen.getByLabelText("Pin session").parentElement;
		const time = actions?.querySelector("time");

		expect(openSession).toHaveClass("pr-[36px]");
		expect(openSession).toHaveClass(
			"group-hover/session-row:pr-sidebar-project-actions",
			"group-has-[:focus-visible]/session-row:pr-sidebar-project-actions",
		);
		expect(label).toHaveClass("min-w-0", "flex-1", "truncate");
		expect(actions).toHaveAttribute("data-session-actions");
		expect(actionButtons).toHaveClass(
			"absolute",
			"right-1",
			"gap-0.5",
			"opacity-0",
			"scale-[0.8]",
			"duration-normal",
			"group-has-[:focus-visible]/session-row:pointer-events-auto",
			"group-has-[:focus-visible]/session-row:scale-100",
			"group-has-[:focus-visible]/session-row:opacity-100",
		);
		expect(actionButtons).toHaveAttribute("data-session-action-buttons", "");
		expect(time).toHaveAttribute("datetime", lastUserMessageAt);
		expect(time).toHaveClass(
			"absolute",
			"right-2",
			"opacity-100",
			"group-has-[:focus-visible]/session-row:opacity-0",
		);
		expect(openSession).toHaveClass("pl-1.5");
		expect(openSession.closest("li")).toHaveClass("pl-0.5");
	});

	it("keeps session status and actions stable when an action receives keyboard focus", async () => {
		const user = userEvent.setup();
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

		const openSession = screen.getByLabelText("Open fix login");
		const row = openSession.closest<HTMLElement>("[data-session-row]");
		const status = openSession.querySelector("[data-session-status]");

		if (!row) throw new Error("Session row not found");
		expect(status).toBeInTheDocument();
		openSession.focus();
		await user.tab();

		expect(screen.getByLabelText("Pin session")).toHaveFocus();
		expect(row).toContainElement(openSession);
		expect(row).toContainElement(status as HTMLElement);
		expect(row).toContainElement(screen.getByLabelText("Pin session"));
	});

	it("toggles project sessions from the folder icon without selecting the project first", async () => {
		const user = userEvent.setup();
		const other: WorkspaceSummary = {
			id: "proj-2",
			name: "Project Two",
			path: "/repo/project-two",
			orchestratorAgent: "claude-code",
			sessions: [{ ...session, id: "proj-2-1", workspaceId: "proj-2", workspaceName: "Project Two", title: "other task" }],
		};
		renderSidebar({
			workspaces: [{ ...workspace, sessions: [session] }, other],
		});

		expect(screen.getByText("fix login")).toBeInTheDocument();
		expect(screen.getByText("other task")).toBeInTheDocument();

		const folder = screen.getByLabelText("Toggle Project Two sessions");
		expect(folder).toBeTruthy();
		await user.click(folder);

		expect(screen.queryByText("other task")).not.toBeInTheDocument();
		expect(screen.getByText("fix login")).toBeInTheDocument();
		expect(navigateMock).not.toHaveBeenCalled();
	});


	it("uses deliberate interaction recency for both worker order and age", () => {
		const at = "2026-07-05T00:00:00Z";
		const directed = {
			...session, id: "directed", title: "orchestrator directed",
			lastUserMessageAt: "2026-07-01T00:00:00Z", lastInteractionAt: at,
		};
		const human = {
			...session, id: "human", title: "human directed",
			lastUserMessageAt: "2026-07-03T00:00:00Z",
		};
		renderSidebar({ workspaces: [{ ...workspace, sessions: [human, directed] }] });
		const directedButton = screen.getByLabelText("Open orchestrator directed");
		const humanButton = screen.getByLabelText("Open human directed");
		expect(directedButton.compareDocumentPosition(humanButton) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		expect(document.querySelector(`time[datetime="${at}"]`)).toHaveAttribute("datetime", at);
	});

	it("lists worker sessions by the user's last message, matching the row's message age", () => {
		// Agent activity bumps updatedAt, but must not reorder rows past the shown age.
		const busyAgent: WorkspaceSession = {
			...session,
			id: "proj-1-busy",
			title: "busy agent",
			createdAt: "2026-06-29T00:00:00Z",
			lastUserMessageAt: "2026-07-01T00:00:00Z",
			updatedAt: "2026-07-05T00:00:00Z",
		};
		const recentlyMessaged: WorkspaceSession = {
			...session,
			id: "proj-1-messaged",
			title: "recently messaged",
			createdAt: "2026-06-29T00:00:00Z",
			lastUserMessageAt: "2026-07-03T00:00:00Z",
			updatedAt: "2026-07-03T00:00:00Z",
		};
		const neverMessaged: WorkspaceSession = {
			...session,
			id: "proj-1-never-messaged",
			title: "never messaged",
			createdAt: "2026-07-02T00:00:00Z",
			updatedAt: "2026-07-06T00:00:00Z",
		};
		const invalidMessage: WorkspaceSession = {
			...session,
			id: "proj-1-invalid-message",
			title: "invalid message",
			createdAt: "2026-06-28T00:00:00Z",
			lastUserMessageAt: "not-a-timestamp",
			updatedAt: "2026-07-07T00:00:00Z",
		};
		const tieOlderUpdate: WorkspaceSession = {
			...session,
			id: "proj-1-tie-older",
			title: "tie older update",
			createdAt: "2026-06-27T00:00:00Z",
			updatedAt: "2026-06-27T00:00:00Z",
		};
		const tieNewerUpdate: WorkspaceSession = {
			...session,
			id: "proj-1-tie-newer",
			title: "tie newer update",
			createdAt: "2026-06-27T00:00:00Z",
			updatedAt: "2026-06-28T00:00:00Z",
		};
		renderSidebar({
			workspaces: [{
				...workspace,
				sessions: [busyAgent, tieOlderUpdate, recentlyMessaged, invalidMessage, neverMessaged, tieNewerUpdate],
			}],
		});

		const sessionButtons = Array.from(document.querySelectorAll<HTMLButtonElement>('[data-session-row] button[aria-label^="Open "]'));
		expect(sessionButtons.map((button) => button.getAttribute("aria-label"))).toEqual([
			"Open recently messaged",
			"Open never messaged",
			"Open busy agent",
			"Open invalid message",
			"Open tie newer update",
			"Open tie older update",
		]);
	});

	it("navigates to the project board when a project with worker sessions is clicked", async () => {
		const user = userEvent.setup();
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

		// Click the project name text — it's inside SidebarMenuButton and bubbles up to onProjectClick.
		await user.click(screen.getByText("Project One"));

		expect(navigateMock).toHaveBeenCalledWith({ to: "/projects/$projectId", params: { projectId: "proj-1" } });
	});

	it("opens the board, not the orchestrator, when an empty project row is clicked", async () => {
		renderSidebar({ workspaces: [{ ...workspace, sessions: [] }] });
		await userEvent.click(screen.getByText("Project One"));
		expect(navigateMock).toHaveBeenCalledWith({ to: "/projects/$projectId", params: { projectId: "proj-1" } });
		expect(spawnMock).not.toHaveBeenCalled();
	});

	it.each([false, true])("opens an empty project's orchestrator from its button (existing: %s)", async (existing) => {
		const orchestrator: WorkspaceSession = { ...session, id: "proj-1-orc", kind: "orchestrator", title: "Orchestrator" };
		spawnMock.mockResolvedValue(orchestrator.id);
		renderSidebar({ workspaces: [{ ...workspace, sessions: existing ? [orchestrator] : [] }] });
		await userEvent.click(screen.getByRole("button", { name: existing ? "Open Project One orchestrator" : "Spawn Project One orchestrator" }));
		await waitFor(() => expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: orchestrator.id },
		}));
		expect(spawnMock).toHaveBeenCalledTimes(existing ? 0 : 1);
	});

	it("returns to the project board from an orchestrator session without collapsing", async () => {
		const user = userEvent.setup();
		const orchestrator: WorkspaceSession = {
			...session,
			id: "proj-1-orc",
			title: "Orchestrator",
			kind: "orchestrator",
		};
		mockParams.projectId = "proj-1";
		mockParams.sessionId = "proj-1-orc";
		renderSidebar({
			workspaces: [{ ...workspace, sessions: [orchestrator, session] }],
		});

		expect(screen.getByLabelText("Open fix login")).toBeInTheDocument();

		await user.click(screen.getByText("Project One"));

		expect(navigateMock).toHaveBeenCalledWith({ to: "/projects/$projectId", params: { projectId: "proj-1" } });
		expect(screen.getByLabelText("Open fix login")).toBeInTheDocument();
		expect(screen.getByText("Project One").closest("button")).toHaveAttribute("aria-expanded", "true");
	});

	it("collapses an expanded project when its board is already active", async () => {
		const user = userEvent.setup();
		mockParams.projectId = "proj-1";
		mockParams.sessionId = undefined;
		renderSidebar({
			workspaces: [{ ...workspace, sessions: [session] }],
		});

		expect(screen.getByLabelText("Open fix login")).toBeInTheDocument();

		await user.click(screen.getByText("Project One"));

		expect(navigateMock).not.toHaveBeenCalled();
		expect(screen.queryByLabelText("Open fix login")).not.toBeInTheDocument();
		expect(screen.getByText("Project One").closest("button")).toHaveAttribute("aria-expanded", "false");
	});

	it("expands a collapsed project when opening its orchestrator", async () => {
		const user = userEvent.setup();
		const orchestrator: WorkspaceSession = {
			...session,
			id: "proj-1-orc",
			title: "Orchestrator",
			kind: "orchestrator",
		};
		renderSidebar({
			workspaces: [{ ...workspace, sessions: [orchestrator, session] }],
		});

		await user.click(screen.getByLabelText("Toggle Project One sessions"));
		expect(screen.queryByLabelText("Open fix login")).not.toBeInTheDocument();
		expect(screen.getByText("Project One").closest("button")).toHaveAttribute("aria-expanded", "false");

		await user.click(screen.getByRole("button", { name: "Open Project One orchestrator" }));

		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "proj-1-orc" },
		});
		expect(screen.getByLabelText("Open fix login")).toBeInTheDocument();
		expect(screen.getByText("Project One").closest("button")).toHaveAttribute("aria-expanded", "true");
	});

	it("defaults worker and orchestrator agents when creating a project", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		window.tallpine!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/new-project");
		renderSidebar({ onCreateProject });

		await user.click(screen.getByLabelText("New project"));
		expect(screen.getByRole("dialog", { name: "Add a project" })).toBeInTheDocument();
		expect(window.tallpine!.app.chooseDirectory).not.toHaveBeenCalled();
		await user.click(screen.getByRole("button", { name: /^Open a local folder$/i }));

		expect(await screen.findByRole("dialog", { name: "Set up project" })).toBeInTheDocument();
		expect(window.tallpine!.app.chooseDirectory).toHaveBeenCalledWith("Choose a project repository");
		const dialog = screen.getByRole("dialog", { name: "Set up project" });
		expect(dialog).toHaveClass("left-1/2", "top-1/2", "-translate-x-1/2", "-translate-y-1/2");
		await user.click(screen.getByRole("button", { name: "Create and start" }));

		await waitFor(() =>
			expect(onCreateProject).toHaveBeenCalledWith(
				expect.objectContaining({
					path: "/repo/new-project",
					workerAgent: "claude-code",
					orchestratorAgent: "claude-code",
				}),
			),
		);
	});

	it("opens an already registered project before agent setup", async () => {
		const user = userEvent.setup();
		useUiStore.getState().clearGlobalToast();
		window.tallpine!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/project-one/");
		renderSidebar();

		await user.click(screen.getByLabelText("New project"));
		await user.click(screen.getByRole("button", { name: /^Open a local folder$/i }));

		await waitFor(() => expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId",
			params: { projectId: "proj-1" },
		}));
		expect(screen.queryByRole("dialog", { name: "Set up project" })).not.toBeInTheDocument();
		expect(postMock).not.toHaveBeenCalledWith("/api/v1/imports/validate", expect.anything());
		expect(useUiStore.getState().globalToasts).toHaveLength(1);
		expect(useUiStore.getState().globalToast).toMatchObject({
			title: "Project already added",
			body: "Opened the registered project for this folder.",
		});
	});

	it("clones a Git URL into the selected folder before starting agents", async () => {
		const user = userEvent.setup();
		const onCloneProject = vi.fn().mockResolvedValue(undefined) as CloneProjectHandler;
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		window.tallpine!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo");
		renderSidebar({ onCloneProject, onCreateProject });

		await user.click(screen.getByLabelText("New project"));
		await user.click(screen.getByRole("button", { name: "Clone a repo" }));
		expect(await screen.findByRole("dialog", { name: "Clone a Git repository" })).toBeInTheDocument();

		await user.type(
			await screen.findByRole("textbox", { name: "Repository URL" }),
			"git@github.com:acme/web-app.git",
		);
		await user.click(screen.getByRole("button", { name: "Choose where to clone the repository" }));
		expect(window.tallpine!.app.chooseDirectory).toHaveBeenCalledWith({ title: "Choose where to clone the repository", defaultPath: "~/tallpine/projects" });
		await waitFor(() => expect(screen.getByRole("button", { name: "Continue" })).toBeEnabled());
		await user.click(screen.getByRole("button", { name: "Continue" }));

		expect(await screen.findByRole("dialog", { name: "Set up project" })).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Create and start" }));
		await waitFor(() => expect(onCreateProject).toHaveBeenCalledWith(expect.objectContaining({
			path: "/repo/web-app",
			clonePreparationId: "prep-web-app",
			workerAgent: "claude-code",
			orchestratorAgent: "claude-code",
		})));
		expect(onCloneProject).not.toHaveBeenCalled();
	});

	it("creates the selected local repository after backing out of a clone", async () => {
		const user = userEvent.setup();
		const onCloneProject = vi.fn().mockResolvedValue(undefined) as CloneProjectHandler;
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		window.tallpine!.app.chooseDirectory = vi
			.fn()
			.mockResolvedValueOnce("/repo")
			.mockResolvedValueOnce("/repo/local-project");
		window.tallpine!.app.scanImportFolder = vi.fn().mockResolvedValue({
			path: "/repo/local-project",
			repos: [
				{
					name: "local-project",
					path: "/repo/local-project",
					relativePath: ".",
					branch: "main",
					remote: "origin",
					hasRemote: true,
					status: "ok",
				},
			],
		});
		renderSidebar({ onCloneProject, onCreateProject });

		await user.click(screen.getByLabelText("New project"));
		await user.click(screen.getByRole("button", { name: "Clone a repo" }));
		await user.type(
			await screen.findByRole("textbox", { name: "Repository URL" }),
			"git@github.com:acme/web-app.git",
		);
		await user.click(screen.getByRole("button", { name: "Choose where to clone the repository" }));
		await waitFor(() => expect(screen.getByRole("button", { name: "Continue" })).toBeEnabled());
		await user.click(await screen.findByRole("button", { name: "Continue" }));

		await user.click(await screen.findByRole("button", { name: "Back to clone details" }));
		await user.click(await screen.findByRole("button", { name: "Back to code source" }));
		await user.click(await screen.findByRole("button", { name: /^Open a local folder$/i }));

		expect(await screen.findByRole("dialog", { name: "Set up project" })).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Create and start" }));

		await waitFor(() =>
			expect(onCreateProject).toHaveBeenCalledWith(
				expect.objectContaining({
					path: "/repo/local-project",
					workerAgent: "claude-code",
					orchestratorAgent: "claude-code",
				}),
			),
		);
		expect(onCloneProject).not.toHaveBeenCalled();
	});

	it("opens the agent sheet after project validation", async () => {
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		const onInitializeProject = vi.fn().mockResolvedValue(undefined) as InitializeProjectHandler;
		renderSidebar({ onCreateProject, onInitializeProject });
		const user = await openCreateProjectDialog("/repo/new-project", { path: "/repo/new-project", repos: [] });

		expect(await screen.findByRole("dialog", { name: "Set up project" })).toBeInTheDocument();
		expect(onInitializeProject).not.toHaveBeenCalled();
		await user.click(screen.getByRole("button", { name: "Create and start" }));
		await waitFor(() => expect(onCreateProject).toHaveBeenCalledTimes(1));
	});

	it("opens agent setup for a validated project folder nested inside a parent repo", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		const onInitializeProject = vi.fn().mockResolvedValue(undefined) as InitializeProjectHandler;
		window.tallpine!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/parent/universe");
		window.tallpine!.app.scanImportFolder = vi.fn().mockResolvedValue({
			path: "/repo/parent/universe",
			repos: [],
			setupWarning:
				"Selected folder is inside an existing Git repository at /repo/parent. Tallpine will initialize this folder as a separate repository.",
		});
		renderSidebar({ onCreateProject, onInitializeProject });

		await user.click(screen.getByLabelText("New project"));
		await user.click(screen.getByRole("button", { name: /^Open a local folder$/i }));

		expect(await screen.findByRole("dialog", { name: "Set up project" })).toBeInTheDocument();
		expect(onInitializeProject).not.toHaveBeenCalled();
		expect(onCreateProject).not.toHaveBeenCalled();

		await user.click(screen.getByRole("button", { name: "Create and start" }));
		await waitFor(() => expect(onCreateProject).toHaveBeenCalledTimes(1));
	});

	it("shows repository initialization recovery for git repos with no commits", async () => {
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		const onInitializeProject = vi.fn().mockResolvedValue(undefined) as InitializeProjectHandler;
		renderSidebar({ onCreateProject, onInitializeProject });
		const user = await openCreateProjectDialog("/repo/unborn", {
			path: "/repo/unborn",
			repos: [
				{
					name: "unborn",
					path: "/repo/unborn",
					relativePath: ".",
					branch: "HEAD",
					remote: "",
					hasRemote: false,
					status: "error",
					reason: "Repository must have at least one commit.",
				},
			],
		});
		expect(await screen.findByRole("dialog", { name: "Set up project" })).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Create and start" }));
		await waitFor(() => expect(onCreateProject).toHaveBeenCalledTimes(1));
	});

	it("does not initialize Git when the project creation is cancelled", async () => {
		const onCreateProject = vi
			.fn()
			.mockRejectedValueOnce(
				codedError("This folder is not a Git repository.", "NOT_A_GIT_REPO"),
			) as unknown as CreateProjectHandler;
		const onInitializeProject = vi.fn().mockResolvedValue(undefined) as InitializeProjectHandler;
		renderSidebar({ onCreateProject, onInitializeProject });
		const user = await openCreateProjectDialog("/repo/new-project", { path: "/repo/new-project", repos: [] });
	await user.click(screen.getByRole("button", { name: "Close project agents dialog" }));
		expect(onInitializeProject).not.toHaveBeenCalled();
		expect(screen.queryByRole("dialog", { name: "Set up project" })).not.toBeInTheDocument();
	});

	it("does not initialize Git a second time after validation", async () => {
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		const onInitializeProject = vi.fn().mockRejectedValue(new Error("git init failed")) as InitializeProjectHandler;
		renderSidebar({ onCreateProject, onInitializeProject });
		const user = await openCreateProjectDialog("/repo/new-project", { path: "/repo/new-project", repos: [] });
		await user.click(screen.getByRole("button", { name: "Create and start" }));
		await waitFor(() => expect(onCreateProject).toHaveBeenCalledTimes(1));
		expect(onInitializeProject).not.toHaveBeenCalled();
	});

	it("can create a workspace project from the project add flow", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		window.tallpine!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/workspace");
		renderSidebar({ onCreateProject });

		await user.click(screen.getByLabelText("New project"));
		await user.click(screen.getByRole("button", { name: /^Open a workspace$/i }));

		expect(window.tallpine!.app.chooseDirectory).toHaveBeenCalledWith("Choose a workspace folder");
		await screen.findByRole("dialog", { name: "Import workspace" });
		await user.click(screen.getByRole("button", { name: "Continue" }));
		await chooseOption(screen.getByRole("combobox", { name: "Worker agent" }), "Codex");
		await chooseOption(screen.getByRole("combobox", { name: "Orchestrator agent" }), "Claude Code");
		await user.click(screen.getByRole("button", { name: "Create workspace and start" }));

		await waitFor(() =>
			expect(onCreateProject).toHaveBeenCalledWith({
				path: "/repo/workspace",
				workerAgent: "codex",
				orchestratorAgent: "claude-code",
				asWorkspace: true,
			}),
		);
	});

	it("does not run single-repo Git setup recovery for workspace imports", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi
			.fn()
			.mockRejectedValueOnce(
				codedError("This folder is not a Git repository.", "NOT_A_GIT_REPO"),
			) as unknown as CreateProjectHandler;
		const onInitializeProject = vi.fn().mockResolvedValue(undefined) as InitializeProjectHandler;
		window.tallpine!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/workspace");
		window.tallpine!.app.checkAncestorRepo = vi.fn().mockResolvedValue(undefined);
		renderSidebar({ onCreateProject, onInitializeProject });

		await user.click(screen.getByLabelText("New project"));
		await user.click(screen.getByRole("button", { name: /^Open a workspace$/i }));
		await screen.findByRole("dialog", { name: "Import workspace" });
		await user.click(screen.getByRole("button", { name: "Continue" }));
		await chooseOption(screen.getByRole("combobox", { name: "Orchestrator agent" }), "Claude Code");
		await user.click(screen.getByRole("button", { name: "Create workspace and start" }));

		await waitFor(() => expect(onCreateProject).toHaveBeenCalledTimes(1));
		expect(onInitializeProject).not.toHaveBeenCalled();
		await waitFor(() => expect(useUiStore.getState().globalToast?.body).toBe("This folder is not a Git repository."));
		expect(screen.queryByText(/Import failed · workspace not registered/i)).not.toBeInTheDocument();
		expect(window.tallpine!.app.checkAncestorRepo).toHaveBeenCalledWith("/repo/workspace");
		expect(window.tallpine!.app.scanImportFolder).toHaveBeenCalledTimes(2);
	});

	it("shows detected repository validation when workspace import fails", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockRejectedValue(new Error("workspace not registered")) as CreateProjectHandler;
		window.tallpine!.app.chooseDirectory = vi.fn().mockResolvedValue("/Users/test/dev/acme");
		window.tallpine!.app.checkAncestorRepo = vi.fn().mockResolvedValue(undefined);
		postMock.mockImplementation(async (path: string, options?: { body?: { importKind?: string; path?: string } }) => {
			if (path === "/api/v1/imports/validate") {
				return {
					data: importValidation(options?.body?.path ?? "/Users/test/dev/acme", {
						childRepos: [repoStatus("/Users/test/dev/acme/api", { isRepo: true, hasCommit: true, hasOrigin: true })],
					}),
					error: undefined,
				};
			}
			return { data: undefined, error: undefined };
		});
		window.tallpine!.app.scanImportFolder = vi.fn().mockResolvedValueOnce({
			path: "/Users/test/dev/acme",
			repos: [],
		}).mockResolvedValueOnce({
			path: "/Users/test/dev/acme",
			repos: [
				{
					name: "web",
					path: "/Users/test/dev/acme/web",
					relativePath: "web",
					branch: "HEAD",
					remote: "",
					hasRemote: false,
					status: "error",
					reason: "Repository name is reserved by Tallpine.",
				},
				{
					name: "api",
					path: "/Users/test/dev/acme/api",
					relativePath: "api",
					branch: "main",
					remote: "git@github.com:acme/api.git",
					hasRemote: true,
					status: "ok",
				},
			],
		});
		renderSidebar({ onCreateProject });

		await user.click(screen.getByLabelText("New project"));
		await user.click(screen.getByRole("button", { name: /^Open a workspace$/i }));
		await screen.findByRole("dialog", { name: "Import workspace" });
		await user.click(screen.getByRole("button", { name: "Continue" }));
		await chooseOption(screen.getByRole("combobox", { name: "Orchestrator agent" }), "Claude Code");
		await user.click(screen.getByRole("button", { name: "Create workspace and start" }));

		await waitFor(() => expect(useUiStore.getState().globalToast?.body).toBe("workspace not registered"));
		expect(screen.queryByText(/Import failed · workspace not registered/i)).not.toBeInTheDocument();
		expect(screen.queryByText("workspace not registered")).not.toBeInTheDocument();
		expect(window.tallpine!.app.checkAncestorRepo).toHaveBeenCalledWith("/Users/test/dev/acme");
		expect(window.tallpine!.app.scanImportFolder).toHaveBeenCalledTimes(2);
	});

	it("blocks workspace import when no child repository is initialized", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockRejectedValue(new Error("workspace not registered")) as CreateProjectHandler;
		window.tallpine!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/workspace");
		window.tallpine!.app.checkAncestorRepo = vi.fn().mockResolvedValue(undefined);
		postMock.mockImplementation(async (path: string, options?: { body?: { importKind?: string; path?: string } }) => {
			if (path === "/api/v1/imports/validate") {
				return {
					data: importValidation(options?.body?.path ?? "/repo/workspace", {
						isValid: false,
						blockingErrors: ["WORKSPACE_CHILD_REPO_REQUIRED"],
						nextStep: "error",
					}),
					error: undefined,
				};
			}
			return { data: undefined, error: undefined };
		});
		renderSidebar({ onCreateProject });

		await user.click(screen.getByLabelText("New project"));
		await user.click(screen.getByRole("button", { name: /^Open a workspace$/i }));
		expect(screen.getByText("Importing a workspace requires at least one direct child Git repository that already has a commit and an origin remote. You can import this folder as a project instead.")).toBeInTheDocument();
		expect(screen.queryByText("No repositories detected in this folder.")).not.toBeInTheDocument();
		expect(screen.queryByText("/repo/workspace")).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Continue" })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Import as project" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Cancel" })).not.toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Go Back" }));
		expect(screen.getByRole("dialog", { name: "Add a project" })).toBeInTheDocument();
		expect(onCreateProject).not.toHaveBeenCalled();
	});

	it("imports a workspace without initialized repositories that need fixes", async () => {
		const user = userEvent.setup();
		window.tallpine!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/workspace");
		window.tallpine!.app.checkAncestorRepo = vi.fn().mockResolvedValue(undefined);
		postMock.mockImplementation(async (path: string, options?: { body?: { importKind?: string; path?: string } }) => {
			if (path === "/api/v1/imports/validate") {
				return {
					data: importValidation(options?.body?.path ?? "/repo/workspace", {
						childRepos: [
							repoStatus("/repo/workspace/api", { isRepo: true, hasCommit: true, hasOrigin: true }),
							repoStatus("/repo/workspace/unborn", { isRepo: true, requiredActions: ["git_commit", "set_remote"] }),
							repoStatus("/repo/workspace/no-remote", { isRepo: true, hasCommit: true, requiredActions: ["set_remote"] }),
						],
						nextStep: "prepare_git",
					}),
					error: undefined,
				};
			}
			return { data: undefined, error: undefined };
		});
		window.tallpine!.app.scanImportFolder = vi.fn().mockResolvedValue({
			path: "/repo/workspace",
			repos: [
				{ name: "api", path: "/repo/workspace/api", relativePath: "api", branch: "main", remote: "origin", hasRemote: true, status: "ok" },
				{ name: "unborn", path: "/repo/workspace/unborn", relativePath: "unborn", branch: "", remote: "", hasRemote: false, status: "ok", needsGitInit: true },
				{ name: "no-remote", path: "/repo/workspace/no-remote", relativePath: "no-remote", branch: "main", remote: "", hasRemote: false, status: "ok", needsGitInit: true },
			],
		});
		renderSidebar({ onCreateProject: vi.fn().mockResolvedValue(undefined) as CreateProjectHandler });

		await user.click(screen.getByLabelText("New project"));
		await user.click(screen.getByRole("button", { name: /^Open a workspace$/i }));
		await screen.findByRole("dialog", { name: "Import workspace" });

		expect(screen.getByText("unborn")).toBeInTheDocument();
		expect(screen.queryByText("Set an origin remote for the child repositories marked below before importing this workspace.")).not.toBeInTheDocument();
		expect(screen.getByText("The 2 repositories marked below have no commit or origin remote yet. Tallpine will import this workspace without them, and agents will not work in them.")).toBeInTheDocument();
		expect(screen.getByText("Git setup needed · Not included")).toBeInTheDocument();
		expect(screen.getByText("No remote · Not included")).toBeInTheDocument();
		expect(screen.queryByRole("dialog", { name: "Prepare project" })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Continue" })).toBeEnabled();
		expect(screen.queryByRole("button", { name: /Set up|Hide setup/i })).not.toBeInTheDocument();
		expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
	});

	it("blocks workspace repositories until their remotes are configured", async () => {
		const user = userEvent.setup();
		window.tallpine!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/workspace");
		window.tallpine!.app.checkAncestorRepo = vi.fn().mockResolvedValue(undefined);
		postMock.mockImplementation(async (path: string, options?: { body?: { importKind?: string; path?: string } }) => {
			if (path === "/api/v1/imports/validate") {
				return {
					data: importValidation(options?.body?.path ?? "/repo/workspace", {
						childRepos: [repoStatus("/repo/workspace/temp", { isRepo: true, hasCommit: true, requiredActions: ["set_remote"] })],
						nextStep: "prepare_git",
					}),
					error: undefined,
				};
			}
			return { data: undefined, error: undefined };
		});
		window.tallpine!.app.scanImportFolder = vi.fn().mockResolvedValue({
			path: "/repo/workspace",
			repos: [{ name: "temp", path: "/repo/workspace/temp", relativePath: "temp", branch: "main", remote: "", hasRemote: false, isRepo: true, hasCommit: true, status: "ok", needsGitInit: false }],
		});
		renderSidebar({ onCreateProject: vi.fn().mockResolvedValue(undefined) as CreateProjectHandler });

		await user.click(screen.getByLabelText("New project"));
		await user.click(screen.getByRole("button", { name: /^Open a workspace$/i }));
		expect(screen.getByRole("dialog", { name: "Import workspace" })).toBeInTheDocument();
		expect(screen.getByText("temp")).toBeInTheDocument();
		expect(screen.getByText("Set an origin remote for the child repositories marked below before importing this workspace.")).toBeInTheDocument();
		expect(screen.queryByRole("textbox", { name: "Origin remote URL" })).not.toBeInTheDocument();
		expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /Set up|Hide setup/i })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Continue" })).toBeDisabled();
	});

	it("offers project import when all workspace children are plain folders", async () => {
		const user = userEvent.setup();
		window.tallpine!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/workspace");
		window.tallpine!.app.checkAncestorRepo = vi.fn().mockResolvedValue(undefined);
		postMock.mockResolvedValue({
				data: importValidation("/repo/workspace", {
					isValid: false,
					blockingErrors: ["WORKSPACE_CHILD_REPO_REQUIRED"],
					root: repoStatus("/repo/workspace", { needsGitInit: true, requiredActions: ["git_init", "git_commit", "set_remote"] }),
					nextStep: "error",
				}),
				error: undefined,
		});
		window.tallpine!.app.scanImportFolder = vi.fn().mockResolvedValue({
			path: "/repo/workspace",
			repos: [
				{ name: "app", path: "/repo/workspace/app", relativePath: "app", branch: "", remote: "", hasRemote: false, isRepo: false, hasCommit: false, status: "ok", needsGitInit: true },
				{ name: "docs", path: "/repo/workspace/docs", relativePath: "docs", branch: "", remote: "", hasRemote: false, isRepo: false, hasCommit: false, status: "ok", needsGitInit: true },
			],
		});
		renderSidebar({ onCreateProject: vi.fn().mockResolvedValue(undefined) as CreateProjectHandler });

		await user.click(screen.getByLabelText("New project"));
		await user.click(screen.getByRole("button", { name: /^Open a workspace$/i }));
		expect(screen.queryByRole("button", { name: "Continue" })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Import as project" })).toBeInTheDocument();
		expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
	});

	it("does not rescan folders for non-validation create failures", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockRejectedValue(new Error("Tallpine daemon is not ready.")) as CreateProjectHandler;
		window.tallpine!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/workspace");
		window.tallpine!.app.checkAncestorRepo = vi.fn().mockResolvedValue(undefined);
		renderSidebar({ onCreateProject });

		await user.click(screen.getByLabelText("New project"));
		await user.click(screen.getByRole("button", { name: /^Open a workspace$/i }));
		await screen.findByRole("dialog", { name: "Import workspace" });
		await user.click(screen.getByRole("button", { name: "Continue" }));
		await chooseOption(screen.getByRole("combobox", { name: "Orchestrator agent" }), "Claude Code");
		await user.click(screen.getByRole("button", { name: "Create workspace and start" }));

		await waitFor(() => expect(useUiStore.getState().globalToast).toMatchObject({
			title: "Project setup failed",
			body: "Tallpine daemon is not ready.",
		}));
		expect(screen.getByRole("dialog", { name: "Add a project" })).toBeInTheDocument();
		// The initial folder validation is required by the import step. The
		// non-validation create failure must not trigger a second scan.
		expect(window.tallpine!.app.checkAncestorRepo).toHaveBeenCalledWith("/repo/workspace");
		expect(window.tallpine!.app.scanImportFolder).toHaveBeenCalledTimes(1);
	});
});
