import { type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render as rtlRender, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { vi } from "vitest";
import "./SessionView.test.mocks.chat";
import "./SessionView.test.mocks.center";
import "./SessionView.test.mocks.files";
import "./SessionView.test.mocks.inspector";
import { SessionView } from "./SessionView";
import { SessionTopbarProvider } from "./SessionTopbarPortal";
import { TooltipProvider } from "./ui/tooltip";
import type { SessionInterfaceTransitionStatus } from "../hooks/useSessionInterfaceTransition";
import { inspectorIsOpen, useUiStore } from "../stores/ui-store";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";

const navigateMock = vi.hoisted(() => vi.fn());

const openShellTerminalMock = vi.hoisted(() => vi.fn());

const adoptedShellHandles = vi.hoisted(() => new Map<string, string>());

const closeShellTerminalMock = vi.hoisted(() => vi.fn());

const cloudReviewGetMock = vi.hoisted(() => vi.fn());

const cloudCpClientMock = vi.hoisted(() => ({
	resumeSession: vi.fn(async () => ({ session: {} })),
	requestWorkspaceCheckout: vi.fn(async () => undefined),
	listChatEvents: vi.fn(),
	getSession: vi.fn(),
	getSessionReviewState: cloudReviewGetMock,
	retrySessionStartup: vi.fn(),
	// The presence heartbeat beats once the test document has focus.
	touchSessionPresence: vi.fn(async () => undefined),
}));

export const cloudResumeMock = cloudCpClientMock.resumeSession;

export const cloudRetryStartupMock = cloudCpClientMock.retrySessionStartup;

export const cloudGetSessionMock = cloudCpClientMock.getSession;

export const getCloudSessionMock = cloudCpClientMock.getSession;

export const listSessionEventsMock = cloudCpClientMock.listChatEvents;

const autoAttachSessionTerminal = vi.hoisted(() => ({ current: true }));

const subscribeSessionEventsMock = vi.hoisted(() => vi.fn(async (_options: {
	onEvent: (event: { type: string; createdAt: string; sessionId: string; sequence: number; payload: unknown }) => void;
}) => undefined));

const nativeFullScreenMock = vi.hoisted(() => vi.fn(() => false));

const interfaceTransitionMock = vi.hoisted(() => ({
	start: vi.fn(),
	refreshStatus: vi.fn(),
	resetStartError: vi.fn(),
	cancel: vi.fn(),
	acknowledgeNotice: vi.fn(),
}));

const interfaceTransitionState = vi.hoisted(() => ({
	starting: false,
	startingPolicy: undefined as "drain" | "interrupt" | undefined,
	startingTarget: undefined as "chat" | "tui" | undefined,
	settling: false,
	startError: undefined as string | undefined,
	status: undefined as SessionInterfaceTransitionStatus | undefined,
}));

// Unset by default so the suite follows the per-session status; tests that
// exercise the daemon's Chat harness list set it explicitly.
const settingsState = vi.hoisted(() => ({
	chatHarnesses: undefined as string[] | undefined,
}));

const reviewGetMock = vi.hoisted(() => vi.fn());

// The skills notice has its own GET; it must not consume reviewGetMock's queued once-values.
const skillsGetMock = vi.hoisted(() => vi.fn(async (..._args: unknown[]) => ({ data: { offers: [], offersEnabled: true, pendingCommit: [] }, error: undefined })));

const chatViewPostMock = vi.hoisted(() => vi.fn());





const cloudSessionQueryState = vi.hoisted(() => ({
	data: undefined as WorkspaceSession | undefined,
	isLoading: false,
	isError: false,
}));

const cloudSessionLookup = vi.hoisted(() => vi.fn());

const cloudSessionPoll = vi.hoisted(() => vi.fn());

const cloudGateState = vi.hoisted(() => ({ cloudEnabled: true }));

const workspaceSessionLookup = vi.hoisted(() => vi.fn());

const resumeAgentPostMock = vi.hoisted(() => vi.fn());


export async function chooseSessionAction(name: string) {
	const user = userEvent.setup();
	await user.click(await screen.findByRole("button", { name }));
}

const routeBlockerState = vi.hoisted(() => ({
	options: undefined as
		| {
				disabled: boolean;
				enableBeforeUnload: boolean;
				shouldBlockFn: () => boolean | Promise<boolean>;
			}
		| undefined,
}));


vi.mock("@tanstack/react-router", () => ({
	useNavigate: () => navigateMock,
	useBlocker: (options: NonNullable<typeof routeBlockerState.options>) => {
		routeBlockerState.options = options;
	},
}));


vi.mock("../lib/platform", () => ({
	// Exercise the macOS shell layout without changing the existing Ctrl-based
	// shortcut assertions in this suite.
	hidesShellTopbar: () => true,
	isMacPlatform: () => false,
	isLinuxPlatform: () => false,
}));

vi.mock("../hooks/useWindowFullScreen", () => ({
	useWindowFullScreen: () => nativeFullScreenMock(),
}));

vi.mock("../hooks/useSessionInterfaceTransition", async (importOriginal) => ({
	...await importOriginal<typeof import("../hooks/useSessionInterfaceTransition")>(),
	interfaceTransitionIsActive: (transition?: { phase?: string }) =>
		Boolean(
			transition &&
				[
					"requested",
					"preflighting",
					"draining",
					"source_stopping",
					"source_stopped",
					"target_starting",
					"activating",
				].includes(transition.phase ?? ""),
		),
	interfaceTransitionIsCancellable: (transition?: { phase?: string }) =>
		Boolean(
			transition && ["requested", "preflighting", "draining"].includes(transition.phase ?? ""),
		),
	interfaceTransitionHasUnacknowledgedNotice: (transition?: {
		phase?: string;
		noticeAcknowledgedAt?: string;
	}) =>
		Boolean(
			transition &&
				!transition.noticeAcknowledgedAt &&
				(transition.phase === "failed" || transition.phase === "recovery_required"),
		),
	useSessionInterfaceTransitionStatus: () => ({
		transition: interfaceTransitionState.status?.transition,
		isLoading: false,
		statusError: undefined,
	}),
	useSessionInterfaceTransition: () => ({
		status: interfaceTransitionState.status,
		transition: interfaceTransitionState.status?.transition,
		isLoading: false,
		statusError: undefined,
		start: interfaceTransitionMock.start,
		refreshStatus: interfaceTransitionMock.refreshStatus,
		starting: interfaceTransitionState.starting,
		startingPolicy: interfaceTransitionState.startingPolicy,
		startingTarget: interfaceTransitionState.startingTarget,
		settling: interfaceTransitionState.settling,
		startError: interfaceTransitionState.startError,
		resetStartError: interfaceTransitionMock.resetStartError,
		cancel: interfaceTransitionMock.cancel,
		cancelling: false,
		cancelError: undefined,
		acknowledgeNotice: interfaceTransitionMock.acknowledgeNotice,
		acknowledgingNotice: false,
		acknowledgeNoticeError: undefined,
	}),
}));


vi.mock("../lib/api-client", () => ({
	apiClient: {
		GET: vi.fn((path: string, ...args: unknown[]) =>
			path === "/api/v1/projects/{id}/skills" ? skillsGetMock(path, ...args) : reviewGetMock(path, ...args)),
		POST: vi.fn((path: string, ...args: unknown[]) =>
			path.endsWith("/resume-agent")
				? resumeAgentPostMock(path, ...args)
				: chatViewPostMock(path, ...args)),
	},
	apiErrorCode: (error: { code?: string }) => error.code,
	apiErrorMessage: (_error: unknown, fallback: string) => fallback,
}));


const { workspaces, workspaceQueryState, shellTerminalsState } = vi.hoisted(() => {
	const worker = {
		id: "sess-1",
		workspaceId: "proj-1",
		workspaceName: "my-app",
		title: "do the thing",
		provider: "claude-code",
		kind: "worker",
		branch: "ao/sess-1",
		status: "working",
		updatedAt: "2026-06-10T00:00:00Z",
		prs: [],
	} satisfies WorkspaceSession;
	const secondWorker = {
		...worker,
		id: "sess-2",
		title: "do the other thing",
		branch: "ao/sess-2",
	} satisfies WorkspaceSession;
	const orchestrator = {
		...worker,
		id: "sess-orch",
		kind: "orchestrator",
		title: "orchestrate",
	} satisfies WorkspaceSession;
	const crossProjectWorker = {
		...worker,
		id: "sess-cross-project",
		workspaceId: "proj-2",
		workspaceName: "other-app",
		title: "cross-project task",
		branch: "ao/cross-project",
	} satisfies WorkspaceSession;
	const workspaces: WorkspaceSummary[] = [
		{ id: "proj-1", name: "my-app", path: "/p", type: "main", sessions: [worker, secondWorker, orchestrator] },
		{ id: "proj-2", name: "other-app", path: "/q", type: "main", sessions: [crossProjectWorker] },
	];
	const workspaceQueryState: { data: WorkspaceSummary[] | undefined; isLoading: boolean; directLoading?: boolean } = {
		data: workspaces,
		isLoading: false,
	};
	const shellTerminalsState: {
		loaded: boolean;
		data: Array<{
			handleId: string;
			projectId?: string;
			sessionId?: string;
			title: string;
			workingDir: string;
			createdAt: string;
		}>;
	} = {
		loaded: true,
		data: [],
	};
	return { workspaces, workspaceQueryState, shellTerminalsState };
});


// The terminal and inspector body pull in xterm/SSE machinery irrelevant to
// the split under test. (ShellTopbar is shell-owned on Win/Linux; when the
// platform hides the shell topbar, SessionView mounts it in-panel.)
vi.mock("./ShellTopbar", () => ({
	ShellTopbar: ({
		sessionAction,
		compactActions,
	}: {
		sessionAction?: ReactNode;
		compactActions?: boolean;
	}) => (
		<div data-compact-actions={compactActions ? "true" : "false"} data-testid="mock-session-topbar">
			{sessionAction}
		</div>
	),
}));

const sessionPlanState = vi.hoisted(() => ({ plan: undefined as unknown }));

vi.mock("../hooks/useSessionPlan", () => ({ useSessionPlan: () => sessionPlanState.plan }));

vi.mock("../hooks/useUnreadNotificationCount", () => ({ useUnreadNotificationCount: () => 0 }));

vi.mock("./NotificationCenter", () => ({
	NotificationCenter: () => <button type="button">Notifications</button>,
}));

vi.mock("../hooks/useSettings", () => ({
	useSettings: () => ({
		settings: settingsState.chatHarnesses ? { chatHarnesses: settingsState.chatHarnesses } : undefined,
		isLoading: false,
		error: undefined,
	}),
}));

vi.mock("../lib/shell-context", () => ({
	useShell: () => ({ daemonStatus: { state: "ready" } }),
}));

vi.mock("../hooks/useWorkspaceQuery", () => ({
	// A session whose directory lists no further files.
	fetchSessionView: vi.fn(async () => undefined),
	forgetSessionArtifactFiles: vi.fn(),
	workspaceQueryKeyForHost: (hostId?: string) => hostId ? ["remote-workspaces", hostId] : ["workspaces"],
	toCloudWorkspaceSession: vi.fn(),
	refetchCloudProjectsForUnknownSessions: vi.fn(),
	useCloudSessionQuery: (orgId: string | undefined, sessionId: string, enabled: boolean, poll?: boolean) => {
		cloudSessionLookup(orgId, sessionId, enabled);
		cloudSessionPoll(poll);
		return cloudSessionQueryState;
	},
	cloudSessionsQueryKey: ["cloud-sessions"],
	useRemoteSessionProjectKind: () => undefined,
	workspaceQueryKey: ["workspaces"],
	useWorkspaceQuery: () => ({
		data: workspaceQueryState.data,
		isLoading: workspaceQueryState.isLoading,
	}),
	useWorkspaceSession: (sessionId: string, hostId?: string, localLookupEnabled?: boolean) => {
		workspaceSessionLookup(sessionId, hostId, localLookupEnabled);
		return ({
		data: workspaceQueryState.data
			?.flatMap((workspace) => workspace.sessions)
			.find((session) => session.id === sessionId),
		isLoading: workspaceQueryState.directLoading ?? workspaceQueryState.isLoading,
		});
	},
}));

// Standalone shell terminals are orthogonal to the split under test, and their
// real hooks would need a QueryClientProvider this suite deliberately omits.
vi.mock("../hooks/useShellTerminals", () => ({
	adoptedShellHandle: (handleId: string) => adoptedShellHandles.get(handleId),
	useShellTerminals: () => ({ data: shellTerminalsState.loaded ? shellTerminalsState.data : undefined, isLoading: !shellTerminalsState.loaded }),
	useOpenShellTerminal: () => ({ open: openShellTerminalMock, isPending: false }),
	useCloseShellTerminal: () => ({ mutate: closeShellTerminalMock }),
	useRenameShellTerminal: () => ({ mutate: vi.fn() }),
}));


export type TestShell = (typeof shellTerminalsState.data)[number];


// Mirrors useOpenShellTerminal().open(): each call shows a pending tab at once;
// finish() resolves its create, replacing the tab's row with its shell and
// recording what it became, as the real hook does.
export function mockShellCreation() {
	const opens: TestShell[] = [];
	openShellTerminalMock.mockImplementation(
		(input: { projectId?: string; sessionId?: string }) => {
			const pending = {
				handleId: `pending-shell:${opens.length + 1}`,
				projectId: input.projectId,
				sessionId: input.sessionId,
				workingDir: "",
				title: `Terminal ${opens.length + 1}`,
				createdAt: "2026-08-31T00:00:00Z",
				optimistic: true as const,
			};
			opens.push(pending);
			shellTerminalsState.data = [...shellTerminalsState.data, pending];
			return pending;
		},
	);
	return (index: number, shell: TestShell, view: { rerender: (ui: ReactNode) => void }, sessionId = "sess-1") => {
		const pending = opens[index]!;
		shellTerminalsState.data = shellTerminalsState.data.map((candidate) =>
			candidate.handleId === pending.handleId ? shell : candidate,
		);
		adoptedShellHandles.set(pending.handleId, shell.handleId);
		view.rerender(<SessionView sessionId={sessionId} />);
	};
}


export function workerSession(sessionId: string): WorkspaceSession {
	const session = workspaces[0].sessions.find((item) => item.id === sessionId);
	if (!session) throw new Error(`missing test session ${sessionId}`);
	return session;
}


export function testInterfaceTransition(
	id: string,
	phase: NonNullable<NonNullable<typeof interfaceTransitionState.status>["transition"]>["phase"],
) {
	return {
		id,
		sessionId: "sess-1",
		sourceMode: "chat" as const,
		targetMode: "tui" as const,
		policy: "interrupt" as const,
		historyPolicy: "strict" as const,
		phase,
		createdAt: "2026-08-26T09:00:00.000Z",
		updatedAt: "2026-08-26T09:00:01.000Z",
	};
}


export function inspectorOpen(sessionId: string): boolean {
	return inspectorIsOpen(useUiStore.getState().inspectorSessions, sessionId);
}


export function browserUnseen(sessionId: string): boolean {
	return Boolean(useUiStore.getState().inspectorSessions[sessionId]?.browserUnseen);
}


export function inspectorButton(): HTMLElement {
	const button = screen.getByText("pop browser").closest("button");
	if (!button) throw new Error("missing inspector button");
	return button;
}


export async function unsafeChatLeaveDialog(): Promise<HTMLElement> {
	return screen.findByRole("dialog", { name: "Discard unsafe Chat draft state?" });
}


export async function confirmUnsafeChatLeave(): Promise<HTMLElement> {
	const dialog = await unsafeChatLeaveDialog();
	fireEvent.click(within(dialog).getByRole("button", { name: "Leave chat" }));
	return dialog;
}


export function render(ui: ReactNode) {
	const client = new QueryClient({
		defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
	});
	return {
		...rtlRender(ui, {
			wrapper: ({ children }) => (
				<QueryClientProvider client={client}>
					<TooltipProvider>
						<SessionTopbarProvider>{children}</SessionTopbarProvider>
					</TooltipProvider>
				</QueryClientProvider>
			),
		}),
		client,
	};
}


export { navigateMock, openShellTerminalMock, adoptedShellHandles, closeShellTerminalMock, cloudReviewGetMock, cloudCpClientMock, autoAttachSessionTerminal, subscribeSessionEventsMock, nativeFullScreenMock, interfaceTransitionMock, interfaceTransitionState, settingsState, reviewGetMock, skillsGetMock, chatViewPostMock, cloudSessionQueryState, cloudSessionLookup, cloudSessionPoll, cloudGateState, workspaceSessionLookup, resumeAgentPostMock, routeBlockerState, workspaces, workspaceQueryState, shellTerminalsState, sessionPlanState };
export { chatSurfaceRenders, chatSurfaceTransitionRenders, chatSurfaceWorkState } from "./SessionView.test.mocks.chat";
export { artifactFeedbackConsumes, browserDestroy, browserViewOptions, browserViewState } from "./SessionView.test.mocks.files";
export { inspectorVisibilityRenders } from "./SessionView.test.mocks.inspector";
