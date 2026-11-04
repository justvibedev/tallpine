// Resets every SessionView.test.setup mock and store to its starting state;
// each SessionView*.test.tsx calls it from beforeEach.
import {
	adoptedShellHandles,
	artifactFeedbackConsumes,
	autoAttachSessionTerminal,
	browserDestroy,
	browserViewOptions,
	browserViewState,
	chatSurfaceRenders,
	chatSurfaceTransitionRenders,
	chatSurfaceWorkState,
	chatViewPostMock,
	closeShellTerminalMock,
	cloudGateState,
	cloudGetSessionMock,
	cloudResumeMock,
	cloudRetryStartupMock,
	cloudReviewGetMock,
	cloudSessionQueryState,
	getCloudSessionMock,
	inspectorVisibilityRenders,
	interfaceTransitionMock,
	interfaceTransitionState,
	listSessionEventsMock,
	nativeFullScreenMock,
	navigateMock,
	openShellTerminalMock,
	resumeAgentPostMock,
	reviewGetMock,
	routeBlockerState,
	settingsState,
	shellTerminalsState,
	subscribeSessionEventsMock,
	workspaceQueryState,
	workspaces,
} from "./SessionView.test.setup";
import { useUiStore } from "../stores/ui-store";
import { setChatDraftBoundary } from "../lib/chat-draft-boundary";

export function resetSessionViewState() {
	cloudGateState.cloudEnabled = true;
	for (const sessionId of ["sess-1", "sess-2", "sess-orch", "sess-cross-project"]) {
		setChatDraftBoundary(sessionId, "composer", undefined);
		setChatDraftBoundary(sessionId, "inline-edit", undefined);
	}
	routeBlockerState.options = undefined;
	inspectorVisibilityRenders.length = 0;
	chatSurfaceRenders.length = 0;
	artifactFeedbackConsumes.length = 0;
	chatSurfaceTransitionRenders.length = 0;
	nativeFullScreenMock.mockReturnValue(false);
	window.localStorage.clear();
	for (const session of workspaces.flatMap((workspace) => workspace.sessions)) {
		delete session.activity;
		delete session.previewUrl;
		delete session.previewRevision;
		delete session.isTerminated;
		session.status = "working";
		session.provider = "claude-code";
		delete session.mode;
		session.prs = [];
	}
	workspaceQueryState.data = workspaces;
	workspaceQueryState.isLoading = false;
	workspaceQueryState.directLoading = undefined;
	useUiStore.setState({
		activeShellTerminalHandleId: null,
		workspaceFileOpenRequest: null,
		// Opening a session leaves the inspector closed (covered by the
		// "keeps the inspector closed" test); most tests here exercise the
		// open rail, so start the workers the way a user left them: open.
		inspectorSessions: {
			"sess-1": { isOpen: true, view: "summary" },
			"sess-2": { isOpen: true, view: "summary" },
			"sess-cross-project": { isOpen: true, view: "summary" },
		},
		isSidebarOpen: true,
		visibleTerminalKindBySession: {},
	});
	browserDestroy.mockReset();
	browserViewOptions.current = undefined;
	browserViewState.url = "";
	browserViewState.tabCount = 1;
	browserViewState.agentBrowserActive = false;
	browserViewState.agentBrowserActivity = null;
	shellTerminalsState.data = [];
	shellTerminalsState.loaded = true;
	navigateMock.mockReset();
	openShellTerminalMock.mockReset();
	adoptedShellHandles.clear();
	openShellTerminalMock.mockImplementation((input: { projectId?: string; sessionId?: string }) => ({
		handleId: "pending-shell:test",
		projectId: input.projectId,
		sessionId: input.sessionId,
		workingDir: "",
		title: "Terminal 1",
		createdAt: "2026-08-31T00:00:00Z",
		optimistic: true,
	}));
	closeShellTerminalMock.mockReset();
	cloudResumeMock.mockReset();
	cloudResumeMock.mockResolvedValue({ session: {} });
	cloudRetryStartupMock.mockReset();
	cloudRetryStartupMock.mockResolvedValue({ session: {} });
	cloudReviewGetMock.mockReset();
	cloudReviewGetMock.mockResolvedValue({ sessionId: "sess-2", reviews: [], runs: [] });
	cloudGetSessionMock.mockReset();
	subscribeSessionEventsMock.mockClear();
	listSessionEventsMock.mockReset();
	listSessionEventsMock.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
	getCloudSessionMock.mockReset();
	autoAttachSessionTerminal.current = true;
	cloudGetSessionMock.mockResolvedValue({ session: { activityState: "idle", status: "idle" } });
	interfaceTransitionMock.start.mockReset();
	interfaceTransitionMock.refreshStatus.mockReset();
	interfaceTransitionMock.refreshStatus.mockImplementation(
		async () => interfaceTransitionState.status,
	);
	interfaceTransitionMock.resetStartError.mockReset();
	interfaceTransitionMock.cancel.mockReset();
	interfaceTransitionMock.acknowledgeNotice.mockReset();
	interfaceTransitionState.starting = false;
	interfaceTransitionState.startingPolicy = undefined;
	interfaceTransitionState.startingTarget = undefined;
	interfaceTransitionState.settling = false;
	interfaceTransitionState.startError = undefined;
	interfaceTransitionState.status = undefined;
	cloudSessionQueryState.data = undefined;
	cloudSessionQueryState.isLoading = false;
	cloudSessionQueryState.isError = false;
	settingsState.chatHarnesses = undefined;
	chatSurfaceWorkState.controllerBusy = false;
	chatSurfaceWorkState.hasRunningTurn = false;
	chatSurfaceWorkState.queuedTurnCount = 0;
	reviewGetMock.mockReset();
	resumeAgentPostMock.mockReset();
	resumeAgentPostMock.mockResolvedValue({ data: {}, error: undefined });
	chatViewPostMock.mockReset().mockResolvedValue({ error: undefined });
	reviewGetMock.mockImplementation(async (path: string) => {
		if (path === "/api/v1/sessions/{sessionId}/workspace/manifest") {
			return {
				data: {
					sessionId: "sess-1",
					files: [],
					truncated: false,
					sections: { staged: [], unstaged: [], untracked: [], committed: [] },
					commits: [],
					summary: { files: 0, additions: 0, deletions: 0 },
				},
				error: undefined,
			};
		}
		return { data: { reviewerHandleId: "", reviews: [], runs: [] }, error: undefined };
	});
}
