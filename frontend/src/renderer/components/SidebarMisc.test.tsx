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
	sessionRowRenders,
	projectItemRenders,
	workspace,
	session,
	importValidation,
	renderSidebar,
} from "./Sidebar.test.setup";

import { SidebarProvider } from "@/components/ui/sidebar";
import { TooltipProvider } from "@/components/ui/tooltip";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
	act,
	render,
	screen,
	waitFor,
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
import { Sidebar, resolveNextNavigationAfterSessionKill } from "./Sidebar";
import { type WorkspaceSession, type WorkspaceSummary } from "../types/workspace";
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

it("keeps new projects at the top across refreshes and restores the order after restart", () => {
	const alpha = { ...workspace, id: "alpha", name: "Alpha", sessions: [] };
	const beta = { ...workspace, id: "beta", name: "Beta", sessions: [] };
	const newest = { ...workspace, id: "zulu", name: "Zulu", sessions: [] };
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const noop = vi.fn().mockResolvedValue(undefined);
	const tree = (workspaces: WorkspaceSummary[]) => (
		<QueryClientProvider client={client}><TooltipProvider><SidebarProvider>
			<Sidebar workspaces={workspaces} onCloneProject={noop} onCreateProject={noop} onInitializeProject={noop} onRemoveProject={noop} onCreateRemoteProject={noop} onInitializeRemoteProject={noop} />
		</SidebarProvider></TooltipProvider></QueryClientProvider>
	);
	const view = render(tree([alpha, beta]));
	const rows = () => Array.from(document.querySelectorAll("[data-project-drop-target][data-project-id]"));
	view.rerender(tree([newest, alpha, beta]));
	const newRow = rows()[0];
	expect(rows().map((row) => row.getAttribute("data-project-id"))).toEqual(["zulu", "alpha", "beta"]);
	view.rerender(tree([alpha, beta, newest]));
	expect(rows().map((row) => row.getAttribute("data-project-id"))).toEqual(["zulu", "alpha", "beta"]);
	expect(rows()[0]).toBe(newRow);
	expect(JSON.parse(window.localStorage.getItem("ao.sidebar.project-order") ?? "null")).toEqual(["zulu", "alpha", "beta"]);
	view.unmount();
	const restarted = render(tree([]));
	restarted.rerender(tree([alpha, beta, newest]));
	expect(rows().map((row) => row.getAttribute("data-project-id"))).toEqual(["zulu", "alpha", "beta"]);
});


it.each(["invalid JSON", '{"unexpected":true}', '[4,null]'])("ignores invalid saved project order: %s", (saved) => {
	window.localStorage.setItem("ao.sidebar.project-order", saved);
	renderSidebar();
	expect(document.querySelector(`[data-project-id="${workspace.id}"]`)).toBeInTheDocument();
});

describe("resolveNextNavigationAfterSessionKill", () => {
	const ws: WorkspaceSummary = {
		...workspace,
		sessions: [
			{ ...session, id: "s-1", title: "Task 1", updatedAt: "2026-06-30T00:00:00Z" },
			{ ...session, id: "s-2", title: "Task 2", updatedAt: "2026-06-30T01:00:00Z" },
			{ ...session, id: "s-3", title: "Task 3", updatedAt: "2026-06-30T02:00:00Z" },
		],
	};

	it("selects previous session when killing middle session in default order", () => {
		// sortedWorkerSessions orders newest first: [s-3, s-2, s-1]
		const route = resolveNextNavigationAfterSessionKill(ws, "s-2");
		expect(route).toEqual({ target: "session", sessionId: "s-3" });
	});

	it("selects first remaining session when killing the first session", () => {
		const route = resolveNextNavigationAfterSessionKill(ws, "s-3");
		expect(route).toEqual({ target: "session", sessionId: "s-2" });
	});

	it("respects custom display order when provided", () => {
		const customOrder: WorkspaceSession[] = [
			{ ...session, id: "s-1", title: "Task 1" },
			{ ...session, id: "s-2", title: "Task 2" },
			{ ...session, id: "s-3", title: "Task 3" },
		];
		const route = resolveNextNavigationAfterSessionKill(ws, "s-2", customOrder);
		expect(route).toEqual({ target: "session", sessionId: "s-1" });
	});

	it("falls back to active orchestrator if no worker sessions remain", () => {
		const wsWithOrch: WorkspaceSummary = {
			...workspace,
			sessions: [
				{ ...session, id: "s-1", title: "Task 1" },
				{ ...session, id: "orch-1", kind: "orchestrator", isTerminated: false },
			],
		};
		const route = resolveNextNavigationAfterSessionKill(wsWithOrch, "s-1");
		expect(route).toEqual({ target: "session", sessionId: "orch-1" });
	});

	it("falls back to project board if orchestrator is terminated", () => {
		const wsWithDeadOrch: WorkspaceSummary = {
			...workspace,
			sessions: [
				{ ...session, id: "s-1", title: "Task 1" },
				{ ...session, id: "orch-1", kind: "orchestrator", isTerminated: true },
			],
		};
		const route = resolveNextNavigationAfterSessionKill(wsWithDeadOrch, "s-1");
		expect(route).toEqual({ target: "project" });
	});

	it("falls back to project board if workspace is undefined", () => {
		const route = resolveNextNavigationAfterSessionKill(undefined, "s-1");
		expect(route).toEqual({ target: "project" });
	});
});

describe("Sidebar session switch rendering", () => {
	it("re-renders only the rows whose active state changed when switching across projects", () => {
		const noop = () => vi.fn().mockResolvedValue(undefined);
		const p1 = { ...workspace, sessions: [{ ...session, id: "p1-a", title: "one a" }, { ...session, id: "p1-b", title: "one b" }] };
		const p2: WorkspaceSummary = {
			...workspace,
			id: "proj-2",
			name: "Project Two",
			path: "/repo/project-two",
			sessions: [
				{ ...session, id: "p2-a", workspaceId: "proj-2", workspaceName: "Project Two", title: "two a" },
				{ ...session, id: "p2-b", workspaceId: "proj-2", workspaceName: "Project Two", title: "two b" },
			],
		};
		let workspaces = [p1, p2];
		window.localStorage.setItem("ao.sidebar.expanded-projects", JSON.stringify([p1.id, p2.id]));
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const handlers = {
			onCloneProject: noop(),
			onCreateProject: noop(),
			onInitializeProject: noop(),
			onRemoveProject: noop(),
			onCreateRemoteProject: noop(),
			onInitializeRemoteProject: noop(),
		};
		const tree = () => (
			<QueryClientProvider client={client}>
				<TooltipProvider>
					<SidebarProvider>
						<Sidebar workspaces={workspaces} {...handlers} />
					</SidebarProvider>
				</TooltipProvider>
			</QueryClientProvider>
		);
		mockParams.projectId = p1.id;
		mockParams.sessionId = "p1-a";
		const view = render(tree());
		expect(screen.getByLabelText("Open two b")).toBeInTheDocument();

		sessionRowRenders.clear();
		mockParams.projectId = p2.id;
		mockParams.sessionId = "p2-a";
		view.rerender(tree());

		expect([...sessionRowRenders.keys()].sort()).toEqual(["p1-a", "p2-a"]);

		// Opening a session refreshes its project (its viewed state changes). Only
		// the changed row may re-render, not every sortable row in that project.
		const refreshed = { ...p2, sessions: [{ ...p2.sessions[0]!, updatedAt: "2026-07-01T00:00:00Z" }, p2.sessions[1]!] };
		workspaces = [p1, refreshed];
		sessionRowRenders.clear();
		view.rerender(tree());

		expect([...sessionRowRenders.keys()]).toEqual(["p2-a"]);
	});
});

describe("Sidebar project rendering", () => {
	it("does not re-render project rows when the shell hands it a new remove handler", () => {
		const noop = () => vi.fn().mockResolvedValue(undefined);
		const p2: WorkspaceSummary = { ...workspace, id: "proj-2", name: "Project Two", path: "/repo/project-two", sessions: [] };
		const workspaces = [workspace, p2];
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		// The shell rebuilds onRemoveProject whenever its workspace list changes.
		const tree = (onRemoveProject: (projectId: string) => Promise<void>) => (
			<QueryClientProvider client={client}>
				<TooltipProvider>
					<SidebarProvider>
						<Sidebar
							workspaces={workspaces}
							onCloneProject={noop()}
							onCreateProject={noop()}
							onInitializeProject={noop()}
							onRemoveProject={onRemoveProject}
							onCreateRemoteProject={noop()}
							onInitializeRemoteProject={noop()}
						/>
					</SidebarProvider>
				</TooltipProvider>
			</QueryClientProvider>
		);
		const view = render(tree(noop()));
		expect(projectItemRenders.count).toBeGreaterThan(0);

		projectItemRenders.count = 0;
		view.rerender(tree(noop()));

		expect(projectItemRenders.count).toBe(0);
	});

	it("does not re-render a project when an agent hook only moves its session's activity time", () => {
		const noop = () => vi.fn().mockResolvedValue(undefined);
		const busy: WorkspaceSession = { ...session, id: "busy", title: "busy", createdAt: "2026-01-01T00:00:00Z", activity: { state: "active", lastActivityAt: "2026-01-01T00:00:00Z" } };
		let workspaces: WorkspaceSummary[] = [{ ...workspace, sessions: [busy] }];
		window.localStorage.setItem("ao.sidebar.expanded-projects", JSON.stringify([workspace.id]));
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const handlers = { onCloneProject: noop(), onCreateProject: noop(), onInitializeProject: noop(), onRemoveProject: noop(), onCreateRemoteProject: noop(), onInitializeRemoteProject: noop() };
		const tree = () => (
			<QueryClientProvider client={client}>
				<TooltipProvider>
					<SidebarProvider>
						<Sidebar workspaces={workspaces} {...handlers} />
					</SidebarProvider>
				</TooltipProvider>
			</QueryClientProvider>
		);
		const view = render(tree());
		expect(screen.getByLabelText("Open busy")).toBeInTheDocument();

		projectItemRenders.count = 0;
		sessionRowRenders.clear();
		workspaces = [{ ...workspace, sessions: [{ ...busy, updatedAt: "2026-01-01T00:05:00Z", activity: { state: "active", lastActivityAt: "2026-01-01T00:05:00Z" } }] }];
		view.rerender(tree());
		expect(projectItemRenders.count).toBe(0);
		expect(sessionRowRenders.size).toBe(0);

		// A change the sidebar shows still renders.
		workspaces = [{ ...workspace, sessions: [{ ...busy, title: "renamed" }] }];
		view.rerender(tree());
		expect(screen.getByLabelText("Open renamed")).toBeInTheDocument();
	});
});

describe("Sidebar active-session reveal", () => {
	const noop = () => vi.fn().mockResolvedValue(undefined);
	const other: WorkspaceSession = { ...session, id: "proj-1-2", title: "second task" };

	function mount(expandedProjectIds: string[]) {
		window.localStorage.setItem("ao.sidebar.expanded-projects", JSON.stringify(expandedProjectIds));
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const tree = () => (
			<QueryClientProvider client={client}>
				<TooltipProvider>
					<SidebarProvider>
						<Sidebar
							workspaces={[{ ...workspace, sessions: [session, other] }]}
							onCloneProject={noop()}
							onCreateProject={noop()}
							onInitializeProject={noop()}
							onRemoveProject={noop()}
							onCreateRemoteProject={noop()}
							onInitializeRemoteProject={noop()}
						/>
					</SidebarProvider>
				</TooltipProvider>
			</QueryClientProvider>
		);
		const view = render(tree());
		return {
			go: (sessionId: string | undefined) => {
				mockParams.projectId = sessionId ? workspace.id : undefined;
				mockParams.sessionId = sessionId;
				view.rerender(tree());
			},
		};
	}

	it("expands the collapsed project and scrolls the active row into view when the session changes", async () => {
		const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
		const { go } = mount([]);
		expect(screen.queryByLabelText("Open second task")).not.toBeInTheDocument();

		go(other.id);

		const row = await screen.findByLabelText("Open second task");
		expect(screen.getByLabelText("Toggle Project One sessions")).toHaveAttribute("aria-expanded", "true");
		expect(JSON.parse(window.localStorage.getItem("ao.sidebar.expanded-projects") ?? "null")).toEqual([workspace.id]);
		await waitFor(() => expect(scroll).toHaveBeenCalledWith({ block: "nearest" }));
		expect(scroll.mock.contexts.at(-1)).toBe(row);
	});

	it("reopens a closed Projects section to reveal the active session", async () => {
		const user = userEvent.setup();
		const { go } = mount([workspace.id]);
		await user.click(screen.getByRole("button", { name: "Projects" }));
		expect(screen.getByRole("button", { name: "Projects" })).toHaveAttribute("aria-expanded", "false");

		go(session.id);

		await waitFor(() => expect(screen.getByRole("button", { name: "Projects" })).toHaveAttribute("aria-expanded", "true"));
		expect(await screen.findByLabelText("Open fix login")).toHaveAttribute("aria-current", "page");
	});

	it("does not re-expand a project the user collapses while staying on the same session", async () => {
		const user = userEvent.setup();
		const { go } = mount([workspace.id]);
		go(session.id);
		await screen.findByLabelText("Open fix login");

		await user.click(screen.getByLabelText("Toggle Project One sessions"));
		expect(screen.queryByLabelText("Open fix login")).not.toBeInTheDocument();

		go(session.id);
		await act(async () => {
			await new Promise((resolve) => setTimeout(resolve, 50));
		});
		expect(screen.getByLabelText("Toggle Project One sessions")).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByLabelText("Open fix login")).not.toBeInTheDocument();
	});
});

