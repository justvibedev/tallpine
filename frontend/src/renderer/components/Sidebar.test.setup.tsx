import { SidebarProvider } from "@/components/ui/sidebar";
import { TooltipProvider } from "@/components/ui/tooltip";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
	createEvent,
	fireEvent,
	render,
	screen,
	within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { vi } from "vitest";
import type { components } from "../../api/schema";
import { Sidebar } from "./Sidebar";
import { type WorkspaceSession, type WorkspaceSummary } from "../types/workspace";
import { agentReadinessQueryKey } from "../hooks/useAgentReadinessQuery";
import { agentReadiness } from "../test/agent-readiness-fixtures";
import type { RemoteHost } from "../hooks/useRemoteHosts";

// Disable motion animations so AnimatePresence unmounts children immediately
// (no exit-animation timer keeps them alive after conditional removal).
// The health badge polls the daemon through React Query; it is covered in HealthBadge.test.tsx.
vi.mock("./HealthBadge", () => ({ HealthBadge: () => null }));

// A settings page layer is opened by the shell; stand it in so the panel can be rendered in settings mode.
const settingsLayer = vi.hoisted(() => ({ current: null as null | { cueBusy: boolean; close: () => void } }));

vi.mock("./SettingsDialog", async (importOriginal) => ({
	...(await importOriginal<typeof import("./SettingsDialog")>()),
	useSettingsPage: () => settingsLayer.current,
}));

vi.mock("./inbox/InboxList", () => ({ InboxList: () => <div data-testid="inbox-list" /> }));

vi.mock("./sidebar/SettingsSidebarNav", () => ({ SettingsSidebarNav: () => <div data-testid="settings-nav" /> }));


vi.mock("motion/react", async (importOriginal) => {
	const actual = await importOriginal<typeof import("motion/react")>();
	return {
		...actual,
		AnimatePresence: ({ children }: { children: React.ReactNode }) => children,
	};
});

export type DragOverTestEvent = {
	active: {
		id: string;
		rect: { current: { initial: null; translated: null } };
	};
	activatorEvent: null;
	delta: { x: number; y: number };
	over: { id: string; rect: { height: number; top: number } } | null;
};


const {
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
} = vi.hoisted(
	() => ({
		cloudGateState: { cloudEnabled: true, localEnabled: true, client: "" },
		cloudSessionState: {
			configured: false,
			session: null as null | { user: { email: string } },
			status: "unauthenticated" as "authenticated" | "loading" | "unauthenticated",
			signIn: vi.fn(),
			signOut: vi.fn().mockResolvedValue(undefined),
		},
		dragEnds: new Map<string, (event: { active: { id: string }; over: { id: string } | null }) => void>(),
		dragOvers: new Map<string, (event: DragOverTestEvent) => void>(),
		dragStarts: new Map<string, (event: { active: { id: string } }) => void>(),
		getMock: vi.fn(),
		postMock: vi.fn(),
		navigateMock: vi.fn(),
		mockLocation: { pathname: "/" },
		mockParams: { hostId: undefined as string | undefined, projectId: undefined as string | undefined, sessionId: undefined as string | undefined },
		renameSessionMock: vi.fn().mockResolvedValue(undefined),
		spawnMock: vi.fn(),
		resumeOrchestratorMock: vi.fn(),
		commandPaletteEnabled: { current: true },
	}),
);


vi.mock("@dnd-kit/core", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@dnd-kit/core")>();
	return {
		...actual,
		DndContext: ({ children, id, onDragEnd, onDragOver, onDragStart }: {
			children: React.ReactNode;
			id?: string;
			onDragEnd?: (event: { active: { id: string }; over: { id: string } | null }) => void;
			onDragOver?: (event: DragOverTestEvent) => void;
			onDragStart?: (event: { active: { id: string } }) => void;
		}) => {
			if (id && onDragEnd) dragEnds.set(id, onDragEnd);
			if (id && onDragOver) dragOvers.set(id, onDragOver);
			if (id && onDragStart) dragStarts.set(id, onDragStart);
			return <div data-dnd-context={id}>{children}</div>;
		},
		DragOverlay: ({ children }: { children: React.ReactNode }) => children,
	};
});


vi.mock("../lib/rename-session", () => ({ renameSession: renameSessionMock }));

vi.mock("../lib/spawn-orchestrator", () => ({
	spawnOrchestrator: spawnMock,
	resumeOrchestrator: resumeOrchestratorMock,
}));

// Counts session-row renders by id; every SessionRow calls this hook once.
const sessionRowRenders = vi.hoisted(() => new Map<string, number>());

vi.mock("../hooks/useSessionRename", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../hooks/useSessionRename")>();
	return {
		...actual,
		useSessionRename: (...args: Parameters<typeof actual.useSessionRename>) => {
			const id = args[0]?.id;
			if (id) sessionRowRenders.set(id, (sessionRowRenders.get(id) ?? 0) + 1);
			return actual.useSessionRename(...args);
		},
	};
});

// Counts project-row renders; every ProjectItem calls this hook once.
const projectItemRenders = vi.hoisted(() => ({ count: 0 }));

vi.mock("../hooks/useCanResumeAgent", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../hooks/useCanResumeAgent")>();
	return {
		...actual,
		useCanResumeAgent: (...args: Parameters<typeof actual.useCanResumeAgent>) => {
			projectItemRenders.count += 1;
			return actual.useCanResumeAgent(...args);
		},
	};
});

// Local (dev-only) cloud sign-in is off in these tests; mock it like its cloud
// siblings so the sign-in row never fires a real settings fetch.
vi.mock("../hooks/useCommandPaletteEnabled", () => ({
	useCommandPaletteEnabled: () => commandPaletteEnabled.current,
}));


vi.mock("../lib/platform", () => ({
	isLinuxPlatform: () => false,
	isMacPlatform: () => true,
	isWindowsPlatform: () => false,
}));


vi.mock("@tanstack/react-router", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@tanstack/react-router")>();
	return {
		...actual,
		useNavigate: () => navigateMock,
		useParams: () => ({ ...mockParams }),
		useRouterState: ({ select }: { select: (state: { location: { pathname: string } }) => unknown }) =>
			select({ location: { pathname: mockLocation.pathname } }),
	};
});


vi.mock("../lib/bridge", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../lib/bridge")>();
	return {
		tallpineBridge: {
			...actual.tallpineBridge,
		},
	};
});


vi.mock("../hooks/useSettings", () => ({
	useSettings: () => ({ settings: { trackerIntakeEnabled: true }, isLoading: false, error: undefined }),
}));


vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, POST: postMock },
	hasTrustedApiBaseUrl: () => false,
	apiErrorMessage: (error: unknown) => {
		if (error instanceof Error) return error.message;
		if (typeof error === "object" && error !== null && "message" in error && typeof error.message === "string") {
			return error.message;
		}
		return "Request failed";
	},
}));


export const workspace: WorkspaceSummary = {
	id: "proj-1",
	name: "Project One",
	path: "/repo/project-one",
	orchestratorAgent: "claude-code",
	sessions: [],
};


export const session: WorkspaceSession = {
	id: "proj-1-1",
	workspaceId: "proj-1",
	workspaceName: "Project One",
	title: "fix login",
	provider: "claude-code",
	kind: "worker",
	branch: "session/proj-1-1",
	status: "working",
	updatedAt: "2026-06-30T00:00:00Z",
	prs: [],
};


// The row archive is confirmed, not instant: open the shared modal and accept.
export async function confirmArchiveFromRow(row: HTMLElement) {
	fireEvent.click(within(row).getByLabelText("Archive session"));
	const dialog = await screen.findByRole("dialog");
	fireEvent.click(within(dialog).getByRole("button", { name: "Confirm, archive session" }));
}


export const exitedOrchestrator: WorkspaceSession = {
	...session,
	id: "proj-1-orch",
	kind: "orchestrator",
	status: "exited",
	activity: { state: "exited", lastActivityAt: "2026-06-30T00:00:00Z" },
	isTerminated: false,
};


export function sidebarPR(overrides: Partial<WorkspaceSession["prs"][number]> = {}): WorkspaceSession["prs"][number] {
	return {
		url: "https://github.com/acme/project-one/pull/7",
		number: 7,
		state: "open",
		ci: "unknown",
		review: "none",
		mergeability: "unknown",
		reviewComments: false,
		updatedAt: "2026-06-30T00:00:00Z",
		...overrides,
	};
}


export type CreateProjectInput = {
	path: string;
	workerAgent: string;
	orchestratorAgent: string;
	trackerIntake?: unknown;
	asWorkspace?: boolean;
};

export type CreateProjectHandler = (input: CreateProjectInput) => Promise<void>;

export type CloneProjectHandler = (input: {
	remoteUrl: string;
	destinationParent: string;
	workerAgent: string;
	orchestratorAgent: string;
	trackerIntake?: unknown;
	signal?: AbortSignal;
}) => Promise<void>;

export type InitializeProjectHandler = (path: string) => Promise<void>;

export type RemoveProjectHandler = (projectId: string) => Promise<void>;

export type ImportValidationResult = components["schemas"]["ImportValidationResult"];

export type RepoGitStatus = components["schemas"]["RepoGitStatus"];


export function repoStatus(repoPath: string, overrides: Partial<RepoGitStatus> = {}): RepoGitStatus {
	return {
		repoPath,
		isRepo: false,
		hasCommit: false,
		hasOrigin: false,
		isEmptyFolder: false,
		needsGitInit: false,
		requiredActions: [],
		blockingErrors: [],
		...overrides,
	};
}


export function importValidation(path: string, overrides: Partial<ImportValidationResult> = {}): ImportValidationResult {
	const importKind = overrides.importKind ?? "workspace";
	return {
		importKind,
		isValid: true,
		blockingErrors: [],
		root: repoStatus(path, importKind === "project" ? { isRepo: true, hasCommit: true, hasOrigin: true } : {}),
		childRepos: [],
		nextStep: "continue",
		...overrides,
	};
}


export function renderSidebar({
	onCloneProject = vi.fn().mockResolvedValue(undefined) as CloneProjectHandler,
	onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler,
	onInitializeProject = vi.fn().mockResolvedValue(undefined) as InitializeProjectHandler,
	onRemoveProject = vi.fn().mockResolvedValue(undefined) as RemoveProjectHandler,
	seedAgents = true,
	workspaces = [workspace],
	remoteHosts = [],
	remoteWorkspaces = [],
	onCreateRemoteProject = vi.fn().mockResolvedValue(undefined),
	onInitializeRemoteProject = vi.fn().mockResolvedValue(undefined),
	onRemoveRemoteProject = vi.fn().mockResolvedValue(undefined),
	initialOpen = true,
	topbarOffset = "toolbar",
	expandedProjectIds,
	seed,
}: {
	onCloneProject?: CloneProjectHandler;
	onCreateProject?: CreateProjectHandler;
	onInitializeProject?: InitializeProjectHandler;
	onRemoveProject?: RemoveProjectHandler;
	seedAgents?: boolean;
	workspaces?: WorkspaceSummary[];
	remoteHosts?: RemoteHost[];
	remoteWorkspaces?: WorkspaceSummary[];
	onCreateRemoteProject?: (hostId: string, input: CreateProjectInput) => Promise<void>;
	onInitializeRemoteProject?: (hostId: string, path: string) => Promise<void>;
	onRemoveRemoteProject?: (hostId: string, projectId: string) => Promise<void>;
	initialOpen?: boolean;
	topbarOffset?: "toolbar" | "titlebar" | "trafficLights" | "session";
	expandedProjectIds?: string[];
	seed?: (client: QueryClient) => void;
} = {}) {
	// Most legacy sidebar tests exercise session rows and assume their fixture
	// project was previously open. Tests for the empty-store behavior opt out.
	window.localStorage.setItem(
		"ao.sidebar.expanded-projects",
		JSON.stringify(expandedProjectIds ?? workspaces.map(({ id }) => id)),
	);
	const queryClient = new QueryClient({
		defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
	});
	if (seedAgents) {
		queryClient.setQueryData(agentReadinessQueryKey, {
			agents: [agentReadiness("claude-code", "Claude Code"), agentReadiness("codex", "Codex")],
		});
	}
	seed?.(queryClient);
	const tree = () => (
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<SidebarProvider defaultOpen={initialOpen}>
					<Sidebar
						topbarOffset={topbarOffset}
						onCloneProject={onCloneProject}
						onCreateProject={onCreateProject}
						onInitializeProject={onInitializeProject}
						onRemoveProject={onRemoveProject}
						workspaces={workspaces}
						remoteHosts={remoteHosts}
						remoteWorkspaces={remoteWorkspaces}
						onCreateRemoteProject={onCreateRemoteProject}
						onInitializeRemoteProject={onInitializeRemoteProject}
						onRemoveRemoteProject={onRemoveRemoteProject}
					/>
				</SidebarProvider>
			</TooltipProvider>
		</QueryClientProvider>
	);
	const view = render(tree());
	// Re-renders the same tree, so a test can change the mocked route params and watch the sidebar follow.
	lastRerender = () => view.rerender(tree());
	return onRemoveProject;
}

let lastRerender: () => void = () => {};
export const rerenderSidebar = () => lastRerender();


export function mockAgentReadinessResponse(response: {
	data: { agents: ReturnType<typeof agentReadiness>[] };
	error: undefined;
} | Promise<{
	data: { agents: ReturnType<typeof agentReadiness>[] };
	error: undefined;
}>) {
	const fallback = getMock.getMockImplementation();
	getMock.mockImplementation((path: string) =>
		path === "/api/v1/agents/readiness" ? Promise.resolve(response) : fallback?.(path),
	);
}

/** Projects restore their persisted disclosure state. */


export async function chooseOption(trigger: HTMLElement, optionName: string) {
	await userEvent.click(trigger);
	await userEvent.click(await screen.findByRole("option", { name: optionName }));
}


export function codedError(message: string, code: "NOT_A_GIT_REPO" | "PROJECT_UNBORN") {
	const error = new Error(message) as Error & { code: string };
	error.code = code;
	return error;
}


export async function openCreateProjectDialog(
	path = "/repo/new-project",
	scan: {
		path: string;
		repos: Array<{
			name: string;
			path: string;
			relativePath: string;
			branch: string;
			remote: string;
			hasRemote: boolean;
			status?: "ok" | "error";
			reason?: string;
			needsGitInit?: boolean;
		}>;
	} = {
		path,
		repos: [
			{ name: "project", path, relativePath: ".", branch: "main", remote: "origin", hasRemote: true, status: "ok" },
		],
	},
) {
	const user = userEvent.setup();
	window.tallpine!.app.chooseDirectory = vi.fn().mockResolvedValue(path);
	window.tallpine!.app.scanImportFolder = vi.fn().mockResolvedValue(scan);
	await user.click(screen.getByLabelText("New project"));
	await user.click(screen.getByRole("button", { name: /^Open a local folder$/i }));
	await screen.findByRole("dialog", { name: "Set up project" });
	await chooseOption(screen.getByRole("combobox", { name: "Worker agent" }), "Codex");
	await chooseOption(screen.getByRole("combobox", { name: "Orchestrator agent" }), "Claude Code");
	return user;
}


export function fireDrag(
	type: "dragStart" | "dragOver" | "drop",
	element: Element,
	props: { clientY?: number },
) {
	const dataTransfer = { setData: () => {}, getData: () => "", setDragImage: () => {}, dropEffect: "", effectAllowed: "" };
	const event = createEvent[type](element);
	Object.defineProperty(event, "dataTransfer", { value: dataTransfer, configurable: true });
	if (props.clientY !== undefined) {
		Object.defineProperty(event, "clientY", { value: props.clientY, configurable: true });
	}
	fireEvent(element, event);
}


export { settingsLayer, cloudGateState, cloudSessionState, dragEnds, dragOvers, dragStarts, getMock, navigateMock, mockLocation, mockParams, postMock, renameSessionMock, spawnMock, resumeOrchestratorMock, commandPaletteEnabled, sessionRowRenders, projectItemRenders };
