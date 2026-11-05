import { AppBrowserLinkContext } from "../components/AppLink";
import { useSessionBrowserLink } from "../hooks/useSessionBrowserLink";
import { createFileRoute, useMatchRoute, useNavigate, useParams, useRouter } from "@tanstack/react-router";
import { isCancelledError, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { type CSSProperties, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { CommandPalette } from "../components/CommandPalette";
import { DaemonFailureBanner } from "../components/DaemonFailureBanner";
import { daemonStatusKeepsStartupScreen } from "../lib/daemon-failure";
import { DaemonStartupLoader } from "../components/DaemonStartupLoader";
import { NotificationRuntime } from "../components/NotificationCenter";
import { HarnessUpdateNotice } from "../components/HarnessUpdateNotice";
import { TrayRuntime } from "../components/TrayRuntime";
import { GlobalNewTaskDialog } from "../components/GlobalNewTaskDialog";
import { GlobalBacklogCapture } from "../components/backlog/GlobalBacklogCapture";
import { GlobalLaneOrchestrator } from "../components/GlobalLaneOrchestrator";
import { GlobalToast } from "../components/GlobalToast";
import { SkillsOfferDialogHost } from "../components/SkillsOfferDialog";
import { SettingsProvider } from "../components/SettingsDialog";
import { KeyboardShortcutsDialog } from "../components/KeyboardShortcutsDialog";
import { QuitConfirmDialog } from "../components/QuitConfirmDialog";
import { openShortcutSettings } from "../lib/open-shortcut-settings";
import { ShellTopbar } from "../components/ShellTopbar";
import { SessionTopbarProvider } from "../components/SessionTopbarPortal";
import { OrchestratorReplacementDialog } from "../components/OrchestratorReplacementDialog";
import { Sidebar } from "../components/Sidebar";
import { NarrowPanelBackdrop } from "../components/shell-nav/NarrowPanelBackdrop";
import { ShellNavRail } from "../components/shell-nav/ShellNavRail";
import { useNarrowShell } from "../components/shell-nav/useNarrowShell";
import { useRemoteHosts } from "../hooks/useRemoteHosts";
import { SidebarProvider } from "../components/ui/sidebar";
import { useIsMobile } from "@/hooks/use-mobile";
import { TitlebarNav } from "../components/TitlebarNav";
import { WindowTitlebar } from "../components/WindowTitlebar";
import { preloadXtermTerminal } from "../components/terminal/xterm-lazy";
import { TerminalCacheProvider } from "../components/terminal/TerminalCacheProvider";
import { scheduleIdle } from "../lib/idle";
import { useDaemonStatus } from "../hooks/useDaemonStatus";
import { useOpenShellTerminal } from "../hooks/useShellTerminals";
import { useSkillsOfferOnProjectAdd } from "../hooks/useSkillsOfferOnProjectAdd";
import {
	useRemoteWorkspaces,
	useWorkspacePolling,
	useWorkspaceQuery,
	workspaceQueryOptions,
} from "../hooks/useWorkspaceQuery";
import { hasTrustedApiBaseUrl } from "../lib/api-client";
import { refreshDaemonStatus } from "../lib/daemon-status";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";
import { ShellProvider } from "../lib/shell-context";
import { applyDocumentTheme, applyDocumentThemeStyle } from "../lib/theme";
import { handleModifierLinkClick } from "../lib/external-link-policy";
import { recordProjectOpened } from "../lib/project-history";
import { cn } from "../lib/utils";
import { sidebarIsVisible, sidebarOccupiesLayout, useUiStore } from "../stores/ui-store";
import { sessionIsActive } from "../types/workspace";
import { useSessionSwitchSnap } from "../hooks/useSessionSwitchSnap";
import { projectNavigateTarget } from "../lib/navigate-to-session";
import { sessionUiKey } from "../lib/hosts";
import { preloadOrchestratorStartingChat, preloadSessionView } from "../shell/lazyChunks";
import { errorMessage } from "../shell/projectConfig";
import { FolderDropOverlay } from "../shell/FolderDropOverlay";
import { MacTitlebarDragStrip } from "../shell/MacTitlebarDragStrip";
import { isMac, isWindows, isLinux, framedAppTopbar, shellTopbarHiddenByPlatform } from "../shell/platform";
import { ShellCenter } from "../shell/ShellCenter";
import { useSidebarWidthPx } from "../shell/useSidebarWidthPx";
import { useTrafficLightDrag } from "../shell/useTrafficLightDrag";
import { useFolderDrop } from "../shell/useFolderDrop";
import { useScopedRouteSession } from "../shell/useScopedRouteSession";
import { useSessionNavigation } from "../shell/useSessionNavigation";
import { useProjectProvisioning } from "../shell/useProjectProvisioning";
import { useLocalProjectActions } from "../shell/useLocalProjectActions";
import { useProjectLifecycleActions } from "../shell/useProjectLifecycleActions";
import { useWorkspaceStartup } from "../shell/useWorkspaceStartup";
import { useShellAppearance } from "../shell/useShellAppearance";
import { useShellShortcuts } from "../shell/useShellShortcuts";
import { useQuitConfirm } from "../shell/useQuitConfirm";
import { useShellNativeEvents } from "../shell/useShellNativeEvents";
import { useRetentionDefaultNotice } from "../shell/useRetentionDefaultNotice";


const SHELL_LOADER_STALE_MS = 2_000;

export const Route = createFileRoute("/_shell")({
	// Prefetch the workspace list for the whole shell (parent loaders run before
	// children); pairs with the router's defaultPreload: "intent" so a hovered
	// nav target is warm before the click.
	loader: async ({ context }) => {
		await refreshDaemonStatus().catch(() => undefined);
		if (!usesPreviewWorkspaceData && !hasTrustedApiBaseUrl()) return;
		// Fresh on navigation, but deduped: a hover preload followed by its click,
		// or a quick run of navigations, shares one /projects + /sessions fetch
		// instead of one each. Mutations and CDC events invalidate regardless.
		// A refetch of the same key (e.g. a session's refreshWorkspaces while this
		// navigation loads) cancels this fetch. The newer fetch warms the same
		// cache, so a cancellation must not fail the whole shell match.
		return context.queryClient
			.fetchQuery({ ...workspaceQueryOptions, staleTime: SHELL_LOADER_STALE_MS })
			.catch((error: unknown) => {
				if (isCancelledError(error)) return undefined;
				throw error;
			});
	},
	component: ShellLayoutWithSettings,
});

function ShellLayoutWithSettings() {
	return (
		<SettingsProvider>
			<ShellLayout />
		</SettingsProvider>
	);
}

// Persistent app shell: the Sidebar + shared state survive route changes; only
// the <Outlet> content (board / session / settings / …) swaps. Lifted out of
// the old single <App>, with selection now owned by the router (route params)
// instead of Zustand. The daemon-status effect runs here exactly once.
function ShellLayout() {
	// Reports how many agents this install has available, once per launch.
	useSessionSwitchSnap();
	const { t } = useTranslation();
	const navigate = useNavigate();
	const router = useRouter();
	const matchRoute = useMatchRoute();
	const queryClient = useQueryClient();
	useWorkspacePolling();
	const workspaceQuery = useWorkspaceQuery();
	const workspaces = workspaceQuery.data ?? [];
	const workspaceIds = useMemo(() => workspaceQuery.data?.map((workspace) => workspace.id), [workspaceQuery.data]);
	useSkillsOfferOnProjectAdd(workspaceIds);
	const { hosts: remoteHosts, refresh: refreshRemoteHosts } = useRemoteHosts();
	const { data: remoteWorkspaces, failedHostIds: remoteFailedHostIds } = useRemoteWorkspaces();
	const failedRemoteHostKey = remoteFailedHostIds.join("\0");
	useEffect(() => {
		if (failedRemoteHostKey) void refreshRemoteHosts();
	}, [failedRemoteHostKey, refreshRemoteHosts]);
	// Global shortcut listeners need the latest workspace list, but recreating
	// those subscriptions for every streamed activity update is avoidable.
	const workspacesRef = useRef(workspaces);
	workspacesRef.current = workspaces;
	const remoteWorkspacesRef = useRef(remoteWorkspaces);
	remoteWorkspacesRef.current = remoteWorkspaces;
	const daemonStatus = useDaemonStatus(queryClient);
	const daemonIsReady = daemonStatus.state === "ready";
	// Warm the lazy chunks the first session open needs once the app is idle, so
	// moving them off the startup path does not add a wait on first use.
	useEffect(() => {
		if (!daemonIsReady) return;
		return scheduleIdle(() => {
			preloadXtermTerminal();
			preloadOrchestratorStartingChat();
			preloadSessionView();
		});
	}, [daemonIsReady]);
	const [workspaceStartupState, setWorkspaceStartupState] = useState<"loading" | "ready" | "error">("loading");
	const workspaceStartupBaselineRef = useRef(0);
	const sidebarDragStripRef = useRef<HTMLDivElement>(null);
	useNarrowShell();
	const isMobileShell = useIsMobile();
	const themePreference = useUiStore((state) => state.themePreference);
	const resolvedTheme = useUiStore((state) => state.resolvedTheme);
	const themeStyle = useUiStore((state) => state.themeStyle);
	const windowTranslucency = useUiStore((state) => state.windowTranslucency);
	const isSidebarOpen = useUiStore(sidebarIsVisible);
	const toggleSidebar = useUiStore((state) => state.toggleSidebar);
	const isRailExpanded = useUiStore((state) => state.isRailExpanded);
	const settingsOpen = useUiStore((state) => state.settingsModal?.scope === "global");
	const sidebarHasLayout = useUiStore(sidebarOccupiesLayout);
		const { sidebarWidthPx } = useSidebarWidthPx({ sidebarHasLayout });
	const syncSystemTheme = useUiStore((state) => state.syncSystemTheme);
	const requestNewTask = useUiStore((state) => state.requestNewTask);
	const openProjectSettings = useUiStore((state) => state.openProjectSettings);
	const requestCreateProjectFromPath = useUiStore((state) => state.requestCreateProjectFromPath);
	const requestNewShellTerminal = useUiStore((state) => state.requestNewShellTerminal);
	const newShellTerminalNonce = useUiStore((state) => state.newShellTerminalNonce);
	const setActiveShellTerminal = useUiStore((state) => state.setActiveShellTerminal);
	const openShellTerminal = useOpenShellTerminal();
		const { isFullScreen, trafficLightDragActive } = useTrafficLightDrag();
	// Seeded to the current value so a mount never opens a terminal unasked.
	const handledShellNonceRef = useRef(newShellTerminalNonce);
	const [isKeyboardShortcutsOpen, setIsKeyboardShortcutsOpen] = useState(false);
	const customizeKeyboardShortcuts = useCallback(() => openShortcutSettings(() => setIsKeyboardShortcutsOpen(false)), []);
	const routeParams = useParams({ strict: false }) as { hostId?: string; projectId?: string; sessionId?: string };
	const routeParamsRef = useRef(routeParams);
	routeParamsRef.current = routeParams;
	const remoteHostsEnabled = useUiStore((state) => state.remoteHosts);
	useEffect(() => {
		if (!remoteHostsEnabled && routeParams.hostId) void navigate({ to: "/", replace: true });
	}, [navigate, remoteHostsEnabled, routeParams.hostId]);
	const linkSession = routeParams.hostId ? undefined : workspaces.flatMap((workspace) => workspace.sessions).find((session) => session.id === routeParams.sessionId);
	const openBrowserLink = useSessionBrowserLink(linkSession);
	const canOpenBrowserLink = linkSession?.kind === "worker" && sessionIsActive(linkSession);
	useEffect(() => {
		document.addEventListener("click", handleModifierLinkClick);
		return () => document.removeEventListener("click", handleModifierLinkClick);
	}, []);
		const { isDragActive } = useFolderDrop({ requestCreateProjectFromPath });
		const { scopedProjectId } = useScopedRouteSession({ queryClient, workspaces, remoteWorkspaces, routeParams });
	// The root route is the intentionally minimal home surface, regardless of
	// whether projects have already been registered.
	const isHomeRoute = Boolean(matchRoute({ to: "/" }));
	useEffect(() => {
		if (!routeParams.hostId && routeParams.projectId) recordProjectOpened(routeParams.projectId);
	}, [routeParams.hostId, routeParams.projectId]);
	const isTerminalsRoute = Boolean(matchRoute({ to: "/terminals" }));
	const isSettingsRoute =
		Boolean(matchRoute({ to: "/settings", fuzzy: true })) ||
		Boolean(matchRoute({ to: "/projects/$projectId/settings", fuzzy: true }));
	// Welcome/settings always self-frame. Platforms that hide the shell-owned
	// topbar (macOS) use the same full-height inset; session actions mount
	// inside SessionView.
	// Home keeps the shell's topbar hidden, but still renders inside the shared
	// rounded center panel. Settings owns its complete frame and remains
	// self-framed.
	const selfFramedCenterPanel = isSettingsRoute;
	const hideShellTopbar = isHomeRoute || selfFramedCenterPanel || shellTopbarHiddenByPlatform;
	const restartingProjectIds = useUiStore((state) => state.restartingProjectIds);
	const setProjectRestarting = useUiStore((state) => state.setProjectRestarting);
	const orchestratorReplacementErrors = useUiStore((state) => state.orchestratorReplacementErrors);
	const setOrchestratorReplacementError = useUiStore((state) => state.setOrchestratorReplacementError);
	const setOrchestratorStartupError = useUiStore((state) => state.setOrchestratorStartupError);
	const setProjectProvisioning = useUiStore((state) => state.setProjectProvisioning);
	const openingOrchestrator = useUiStore((state) =>
		state.projectCreationPending || Boolean(
			routeParams.projectId && state.provisioningProjectIds.has(sessionUiKey(routeParams.projectId, routeParams.hostId)),
		),
	);
	const startingWorkspace = workspaces.find((workspace) => workspace.id === routeParams.projectId);
	const startingOrchestratorSession = openingOrchestrator
		? startingWorkspace?.sessions.find((session) => session.kind === "orchestrator")
		: undefined;
	const startingSteps = startingOrchestratorSession?.provisionSteps;
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const replacementErrorProjectId = Object.keys(orchestratorReplacementErrors)[0] ?? null;
const replacementErrorCount = Object.keys(orchestratorReplacementErrors).length;
	const isStartupLoading =
		!usesPreviewWorkspaceData &&
		daemonStatusKeepsStartupScreen(daemonStatus) &&
		(daemonStatus.state !== "ready" || workspaceStartupState === "loading" || (!workspaceQuery.isSuccess && !workspaceQuery.isError));
		const { sidebarSessionOrder, openNavSession, navigateSession } = useSessionNavigation({ t, navigate, workspacesRef, remoteWorkspacesRef, routeParams });

		const { updateWorkspaces, provisionOrchestrator, completeProjectCreation } = useProjectProvisioning({ navigate, queryClient, routeParamsRef, setOrchestratorStartupError, setProjectProvisioning });

		const { createProject, cloneProject, initializeProjectRepository } = useLocalProjectActions({ navigate, queryClient, workspacesRef, setOrchestratorStartupError, setProjectProvisioning, showGlobalToast, provisionOrchestrator, completeProjectCreation });
		const { createRemoteProject, initializeRemoteProject, validateImport, removeProject, removeRemoteProject, openRemoteProjectOrchestrator, restartOrchestrator } = useProjectLifecycleActions({ t, navigate, queryClient, workspaces, remoteHosts, remoteWorkspaces, openProjectSettings, routeParams, setProjectRestarting, setOrchestratorReplacementError, setOrchestratorStartupError, setProjectProvisioning, showGlobalToast, updateWorkspaces });

	useEffect(() => {
		applyDocumentTheme(resolvedTheme);
	}, [resolvedTheme]);

	useEffect(() => {
		applyDocumentThemeStyle(themeStyle);
	}, [themeStyle]);

		useWorkspaceStartup({ queryClient, workspaceQuery, daemonStatus, workspaceStartupState, setWorkspaceStartupState, workspaceStartupBaselineRef });

		useShellAppearance({ themePreference, resolvedTheme, windowTranslucency, syncSystemTheme });

		useShellShortcuts({ router, toggleSidebar, requestNewTask, setIsKeyboardShortcutsOpen, routeParams, scopedProjectId, sidebarSessionOrder, openNavSession, navigateSession });

		const { isQuitConfirmOpen, quitKeepsRunning, resolveQuitConfirm } = useQuitConfirm();

		useRetentionDefaultNotice(navigate);

		useShellNativeEvents({ navigate, requestCreateProjectFromPath, requestNewShellTerminal, newShellTerminalNonce, setActiveShellTerminal, openShellTerminal, handledShellNonceRef, routeParams, scopedProjectId, isTerminalsRoute, navigateSession });

	const openProject = useCallback(
		(projectId: string) => void navigate({ to: "/projects/$projectId", params: { projectId } }),
		[navigate],
	);
	const shellContextValue = useMemo(
		() => ({
			daemonStatus,
			workspaceStartupState,
			cloneProject,
			createProject,
			initializeProjectRepository,
			openRemoteProjectSettings: (hostId: string, projectId: string) => openProjectSettings(projectId, hostId),
			openProject,
			validateImport,
		}),
		[
			cloneProject,
			createProject,
			daemonStatus,
			initializeProjectRepository,
			openProjectSettings,
			openProject,
			validateImport,
			workspaceStartupState,
		],
	);

	// Keep the shell chrome and its first route behind the same readiness gate.
	// Rendering the sidebar with an empty query while the home outlet shows its
	// loader creates a visible two-stage launch and can make the home page flash
	// before the project list arrives.
	// ⌘Q can arrive at any time, including during startup or a daemon
	// reconnect, so the quit confirm renders outside the readiness gate.
	const quitConfirmDialog = (
		<QuitConfirmDialog open={isQuitConfirmOpen} keepsRunning={quitKeepsRunning} onResolve={resolveQuitConfirm} />
	);
	if (isStartupLoading) {
		return (
			<>
				<DaemonStartupLoader status={daemonStatus} />
				{quitConfirmDialog}
			</>
		);
	}

	return (
		<ShellProvider
			value={shellContextValue}
		>
			<AppBrowserLinkContext.Provider value={canOpenBrowserLink ? openBrowserLink : undefined}>
			<SessionTopbarProvider>
				<NotificationRuntime />
				<HarnessUpdateNotice enabled={daemonStatus.state === "ready"} />
				<TrayRuntime />
				<FolderDropOverlay active={isDragActive} />
				<GlobalNewTaskDialog />
				<GlobalBacklogCapture />
				<GlobalLaneOrchestrator />
				<GlobalToast />
				<SkillsOfferDialogHost />
				{quitConfirmDialog}
				<KeyboardShortcutsDialog
					open={isKeyboardShortcutsOpen}
					onOpenChange={setIsKeyboardShortcutsOpen}
					onCustomize={customizeKeyboardShortcuts}
				/>
				<TerminalCacheProvider
					daemonReady={daemonStatus.state === "ready"}
					theme={resolvedTheme}
				>

			{/* Shell chrome: Win/Linux hang the sidebar under a topbar. macOS uses a
          titlebar strip above the off-canvas sidebar. Session and board actions
          render inside the center panel when the shell topbar is hidden. */}
			<div
				className={cn(
					// `app-shell-root` is the platform-independent hook the native-composition
					// cascade needs to clear this opaque `bg-sidebar` while the live browser
					// page shows through. Windows/Linux were already covered by their
					// platform classes; macOS adds none, so without this the shell painted
					// over the live page whenever an overlay raised it.
					"app-shell-root flex h-screen min-h-0 flex-col bg-sidebar text-foreground",
					isWindows && "platform-windows",
					isLinux && "platform-linux",
					isFullScreen && "native-fullscreen",
				)}
			>
				{/* Windows-only custom title bar (sidebar toggle + File/Edit/View/…
            menu); paints the chrome the frameless window drops. Renders null on
            macOS/Linux. */}
				<WindowTitlebar />
				{/* App routes render their topbar inside the framed panel, matching the board chrome across platforms while leaving OS titlebars native. */}
				{!framedAppTopbar && !hideShellTopbar && !routeParams.sessionId && !openingOrchestrator && !settingsOpen ? <ShellTopbar /> : null}
				{/* Controlled by the ui-store so TitlebarNav / Topbar toggles (which
			    call the store directly) stay in sync. Direct dragging scopes its
			    width override to the sidebar's layout consumers. */}
				<SidebarProvider
					// The icon rail keeps its width whether or not the Sessions panel is open; it hides with the mobile overlay.
					className="min-h-0 flex-1 flex-col overflow-x-hidden [--tallpine-rail-w:var(--size-sidebar-icon)] data-[rail-expanded=true]:[--tallpine-rail-w:var(--size-rail-expanded)] max-md:[--tallpine-rail-w:0px] max-md:data-[rail-expanded=true]:[--tallpine-rail-w:0px]"
					data-rail-expanded={isRailExpanded}
					keyboardShortcut={false}
					onOpenChange={(open) => {
						if (open !== isSidebarOpen) toggleSidebar();
					}}
					open={!isStartupLoading && isSidebarOpen}
					style={
						{
							"--sidebar-width": "var(--size-sidebar-default)",
							"--sidebar-width-icon": "var(--size-sidebar-icon)",
						} as CSSProperties
					}
				>
					<div
						className="flex min-h-0 w-full flex-1 overflow-x-hidden"
						data-testid="shell-content-row"
					>
						{/* macOS + Linux reserve a titlebar band for the fixed TitlebarNav
			      cluster above a full-height sidebar; Windows hangs the sidebar
			      below its custom titlebar. */}
					<Sidebar
						underTopbar={isMac || isWindows || isLinux}
						topbarOffset={isWindows ? "titlebar" : hideShellTopbar ? "trafficLights" : "toolbar"}
						onCloneProject={cloneProject}
						onCreateProject={createProject}
						onInitializeProject={initializeProjectRepository}
						onRemoveProject={removeProject}
						resizeAuxiliaryTargetRef={sidebarDragStripRef}
						workspaceError={workspaceQuery.isError ? errorMessage(workspaceQuery.error) : undefined}
						workspaces={workspaces}
						remoteHosts={remoteHosts}
						onCreateRemoteProject={createRemoteProject}
						onInitializeRemoteProject={initializeRemoteProject}
						onOpenRemoteProject={(hostId, projectId) => { void navigate(projectNavigateTarget(projectId, hostId)); }}
						onOpenRemoteOrchestrator={(hostId, projectId) => { void openRemoteProjectOrchestrator(hostId, projectId); }}
						onConfigureRemoteProject={(hostId, projectId) => openProjectSettings(projectId, hostId)}
						onRemoveRemoteProject={removeRemoteProject}
						onRetryRemoteHosts={() => {
							void refreshRemoteHosts();
							void queryClient.invalidateQueries({ queryKey: ["remote-workspaces"] });
						}}
						remoteWorkspaces={remoteWorkspaces}
						remoteFailedHostIds={remoteFailedHostIds}
					/>
					{/* After the sidebar in DOM order so it paints over the panel while that slides away. */}
					<ShellNavRail
						underTopbar={isMac || isWindows || isLinux}
						topbarOffset={isWindows ? "titlebar" : hideShellTopbar ? "trafficLights" : "toolbar"}
						workspaces={workspaces}
					/>
					<NarrowPanelBackdrop />
					<main className={cn("flex min-w-0 flex-1 flex-col overflow-x-hidden", !sidebarHasLayout && isMobileShell && "sidebar-hidden")}>
						<div className="relative min-h-0 flex-1 overflow-x-hidden">
							{/* Board/session routes render inside the same inset box the welcome board and settings paint for themselves, so every screen sits within the app's outer boundary. */}
							<ShellCenter
								hideShellTopbar={hideShellTopbar}
								isSessionRoute={Boolean(routeParams.sessionId)}
								selfFramedCenterPanel={selfFramedCenterPanel}
								startingOrchestrator={openingOrchestrator}
								startingSteps={startingSteps}
							/>
						</div>
						</main>
					</div>
					<DaemonFailureBanner status={daemonStatus} />
					{/* When ShellTopbar is hidden, keep a macOS window-drag strip over
              the traffic-light band only. The fixed TitlebarNav renders after
              this strip so its no-drag buttons remain clickable. */}
					{hideShellTopbar && isMac ? (
						<MacTitlebarDragStrip
							isFullScreen={isFullScreen}
							sidebarDragStripRef={sidebarDragStripRef}
							sidebarHasLayout={sidebarHasLayout}
							sidebarWidthPx={sidebarWidthPx}
							trafficLightDragActive={trafficLightDragActive}
						/>
					) : null}
					{/* Fixed macOS titlebar cluster beside the traffic lights — rendered
              once here so the toggle/history buttons never move when the
              sidebar collapses or expands. History arrows stay visible but
              locked on the empty start page. MUST come after the drag strip
              (ShellTopbar or the welcome substitute) in the DOM: Electron
              builds the window-drag region in document order (drag rects add,
              no-drag rects subtract), so the cluster's no-drag holes only
              survive if they're processed after the drag strips they overlap.
              Rendered first, real clicks get swallowed by window-drag even
              though DOM hit-testing looks correct. */}
					<TitlebarNav
						historyLocked={isHomeRoute}
						isFullScreen={isFullScreen}
					/>
				</SidebarProvider>
				<OrchestratorReplacementDialog
					pending={Boolean(replacementErrorProjectId && restartingProjectIds.has(replacementErrorProjectId))}
					pendingCount={replacementErrorCount}
					error={replacementErrorProjectId ? orchestratorReplacementErrors[replacementErrorProjectId] : undefined}
					onOpenChange={(open) => {
						if (!open && replacementErrorProjectId) setOrchestratorReplacementError(replacementErrorProjectId, null);
					}}
					onRetry={(projectId) => void restartOrchestrator(projectId)}
					onRetryAsTui={(projectId) => void restartOrchestrator(projectId, "tui")}
					onRetryWithoutApprovals={(projectId) =>
						void restartOrchestrator(projectId, undefined, "bypass-permissions")
					}
					projectId={replacementErrorProjectId}
					workspaces={workspaces}
				/>
					<CommandPalette />
				</div>
				</TerminalCacheProvider>
			</SessionTopbarProvider>
			</AppBrowserLinkContext.Provider>
		</ShellProvider>
	);
}
export { createProjectConfig } from "../shell/projectConfig";
