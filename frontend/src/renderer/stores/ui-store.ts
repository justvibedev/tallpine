import { create } from "zustand";
import { legacyDeveloperModeStorageKey, windowTranslucencyStorageKey } from "../lib/window-translucency";
import { sessionUiKey } from "../lib/hosts";
import type { GlobalSettingsSection, HarnessDiagnosticsRequest, ProjectSettingsSection, SettingsModal } from "./settings-modal";
import type { TerminalTarget } from "../types/terminal";
import type { FilesSelection } from "../hooks/workspace-files/workspace-files-types";
import {
	applyDocumentTheme,
	applyDocumentThemeStyle,
	isDocumentThemeStyle,
	readStoredThemePreference,
	readStoredThemeStyle,
	resolveTheme,
	runThemeTransition,
	systemTheme,
	themeStorageKey,
	themeStyleStorageKey,
	type Theme,
	type ThemePreference,
	type ThemeStyle,
} from "../lib/theme";

export type { Theme, ThemePreference, ThemeStyle } from "../lib/theme";
export { readStoredThemePreference, readStoredThemeStyle, resolveTheme } from "../lib/theme";


/** Worker detail view toggles — Changes (Git rail) is the default. */
export type WorkbenchTab = "changes" | "files" | "terminal";
export type InspectorView = "summary" | "reviews" | "browser" | "sideChat" | "files" | "artifacts" | "plan";

export type InspectorSessionState = {
	isOpen: boolean;
	view: InspectorView;
	/** The current non-empty browser content lifecycle has already been revealed. */
	browserContentRevealed?: boolean;
	/** Real browser activity occurred while Browser was not visible. */
	browserUnseen?: boolean;
	/** Files tab: review changed files directly. Defaults to true; false shows the full tree. */
	filesChangedOnly?: boolean;
	/** Files tab: the branch and changes under review, shared by the docked and maximized explorers. Unset follows the default. */
	filesSelection?: FilesSelection;
	/** Files: display mode picked per path in a centre tab, restored for the same open request after a remount. */
	fileDisplayModes?: Record<string, RememberedFileDisplayMode>;
	/** The session-entry defaulting (Summary tab, baseline browser reveal) has already run once for this session's lifetime. */
	initialized?: boolean;
};

export type FileDisplayMode = "diff" | "file" | "rendered";

export type RememberedFileDisplayMode = { mode: FileDisplayMode; requestKey: number };

export type GlobalToast = {
	title: string;
	body?: string;
	tone?: "info" | "error";
	placement?: "bottom-right" | "top-center";
	dismissible?: boolean;
	durationMs?: number;
	dedupeKey?: string;
	/** A single follow-up the user can take from the toast, e.g. Undo. Running it dismisses the toast. */
	action?: { label: string; onClick: () => void };
	nonce: number;
};

/** A backlog item a new worker starts on: the New Task dialog prefills its brief and sends the ref. */
export type NewTaskBacklog = { ref: string; title: string; force?: boolean };

export type GlobalToastOptions = Pick<GlobalToast, "tone" | "placement" | "dismissible" | "durationMs" | "dedupeKey" | "action">;

export type WorkspaceFileOpenRequest = {
	sessionId: string;
	hostId?: string;
	path: string;
	/** "artifact" resolves `path` against the session's artifact files instead of its workspace. */
	source?: "artifact";
	nonce: number;
};

// Selection (which project/session is open) now lives in the URL — the router
// is the single source of truth, read via route params. This store holds only
// ephemeral UI: theme, sidebar collapse, command palette, per-session inspector
// state, and the active workbench tab within a session.
export type UiState = {
	workbenchTab: WorkbenchTab;
	/** The user's durable sidebar preference. */
	isSidebarOpen: boolean;
	/** The shell rail shows a text label beside each icon; the choice persists. */
	isRailExpanded: boolean;
	/** Last Sessions-section page (session chat, board); where leaving a full-screen view returns. */
	lastSessionsPath: string;
	setLastSessionsPath: (path: string) => void;
	/** Narrow window: the Sessions panel overlays the page instead of taking layout. */
	isNarrowShell: boolean;
	/** Transient open state of the narrow-window overlay; never persisted. */
	isNarrowPanelOpen: boolean;
	inspectorSessions: Record<string, InspectorSessionState>;
	isCommandPaletteOpen: boolean;
	settingsModal: SettingsModal | null;
	themePreference: ThemePreference;
	/** Resolved light/dark for React consumers; may track OS while preference is system. */
	resolvedTheme: Theme;
	/** Named color style theme (e.g. "catppuccin", "nord") — independent of light/dark mode. */
	themeStyle: ThemeStyle;
	/** When true, developer-only release controls are available. Default off. */
	/** Experimental: connect to Tallpine daemons on other machines. Default off. */
	remoteHosts: boolean;
	/** Memory and CPU monitoring (card chips, memory light, Diagnostics page). Default off. */
	diagnostics: boolean;
	/** Experimental side chats in the inspector. Default off. */
	sideChats: boolean;
	/** Developer Mode, macOS only: blur the desktop behind a translucent window. */
	windowTranslucency: boolean;
	/** Copy the terminal selection to the clipboard on mouse-up, like native terminals. Default on. */
	terminalCopyOnSelect: boolean;
	restartingProjectIds: ReadonlySet<string>;
	// Projects whose initial orchestrator spawn (after import/clone) is still
	// running in the background. The board renders a progress banner and gates
	// session actions until the spawn settles, instead of blocking navigation.
	provisioningProjectIds: ReadonlySet<string>;
	projectCreationPending: boolean;
	orchestratorReplacementErrors: Record<string, OrchestratorReplacementFailure>;
	orchestratorStartupErrors: Record<string, string>;
	globalToasts: GlobalToast[];
	globalToast: GlobalToast | null;
	globalToastSequence: number;
	// Transient "open the New Task dialog for this project" signal. The nonce
	// bumps on every request so a repeat press (even for the same project) still
	// re-fires; the always-mounted GlobalNewTaskDialog consumes it. Selection
	// still lives in the URL — this is a one-shot action, not persisted state.
	newTaskRequest: { projectId: string; hostId?: string; backlog?: NewTaskBacklog; nonce: number } | null;
	/** One-shot request to open the lane orchestrator dialog for a project. */
	laneOrchestratorRequest: { projectId: string; nonce: number } | null;
	/** One-shot request to open the Add to backlog popover, optionally with a title already typed. */
	backlogCaptureRequest: { title: string; nonce: number } | null;
	/** Transient one-shot request to reveal a path in a session's existing Files UI. */
	workspaceFileOpenRequest: WorkspaceFileOpenRequest | null;
	// Bumps to ask the sidebar's create-project flow to open (the ⌘N fallback
	// when no project is in scope).
	createProjectNonce: number;
	// Transient "a folder was dropped onto the app window — open the
	// create-project flow for this path" signal, mirroring newTaskRequest: the
	// nonce always bumps so dropping the same folder twice in a row still
	// re-fires. Consumed by the same CreateProjectFlow instance that owns
	// openSignal for ⌘N (Sidebar's CreateProjectButton).
	folderDropRequest: { path: string; nonce: number } | null;
	// Bumps to ask for a new standalone shell terminal. Like newTaskRequest this
	// is a one-shot signal, not state: the tab-strip + button and Ctrl+Shift+` both
	// raise it so they cannot drift apart, and a repeat press re-fires because
	// the nonce always changes. The shell layout is its single consumer — it is
	// mounted on every route, so the request is honoured from anywhere in the app.
	newShellTerminalNonce: number;
	// The shell terminal the user most recently opened or selected. Both the
	// session view (tabs beside the session's pane) and the standalone terminals
	// view read it, so whichever one is on screen shows the same shell.
	activeShellTerminalHandleId: string | null;
	// Which terminal each mounted session is actually showing. The session pane
	// renders one terminal at a time, so opening a shell or the reviewer swaps
	// the agent's terminal off screen even though the route still points at that
	// session. Surfaces outside the session subtree (the notification runtime)
	// need that distinction, and SessionView's own target is local state.
	visibleTerminalKindBySession: Record<string, TerminalTarget["kind"]>;
	setWorkbenchTab: (tab: WorkbenchTab) => void;
	setThemePreference: (theme: ThemePreference) => void;
	setThemeStyle: (style: ThemeStyle) => void;
	setRemoteHosts: (enabled: boolean) => void;
	setDiagnostics: (enabled: boolean) => void;
	setSideChats: (enabled: boolean) => void;
	setWindowTranslucency: (enabled: boolean) => void;
	setTerminalCopyOnSelect: (enabled: boolean) => void;
	/** True while the restart-to-update confirmation is open. */
	openGlobalSettings: (section?: GlobalSettingsSection, options?: { focusAgentId?: string; hostId?: string; startLogin?: boolean; showDiagnostics?: HarnessDiagnosticsRequest; preserveProject?: boolean }) => void;
	/** `options` as a string is the owning daemon's host ID (remote projects). */
	openProjectSettings: (projectId: string, options?: string | { section?: ProjectSettingsSection }) => void;
	closeSettings: () => void;
	/** Refresh resolvedTheme from OS without writing light/dark to storage. */
	syncSystemTheme: () => void;
	toggleSidebar: () => void;
	toggleRailExpanded: () => void;
	setNarrowShell: (narrow: boolean) => void;
	closeNarrowPanel: () => void;
	setInspectorOpen: (sessionId: string, isOpen: boolean) => void;
	toggleInspector: (sessionId: string) => void;
	setInspectorView: (sessionId: string, view: InspectorView, remember?: boolean) => void;
	/**
	 * Runs the "entering this session" defaults — Summary tab, baseline browser
	 * reveal — exactly once per session's lifetime. Backed by persisted store
	 * state (not a component-local ref) so it stays a no-op across unmount and
	 * remount of the session view, not just across re-renders of one mounted
	 * instance.
	 */
	initializeInspectorSession: (sessionId: string, hasBrowserContent: boolean, hasInspector: boolean) => void;
	setBrowserContentRevealed: (sessionId: string, revealed: boolean) => void;
	setBrowserUnseen: (sessionId: string, unseen: boolean) => void;
	setFilesChangedOnly: (sessionId: string, changedOnly: boolean) => void;
	setFilesSelection: (sessionId: string, selection: FilesSelection) => void;
	/** Back to the checked-out branch and the default changes. */
	resetFilesSelection: (sessionId: string) => void;
	setFileDisplayMode: (sessionId: string, path: string, mode: FileDisplayMode, requestKey: number) => void;
	setCommandPaletteOpen: (open: boolean) => void;
	setProjectRestarting: (projectId: string, restarting: boolean, hostId?: string) => void;
	setProjectProvisioning: (projectId: string, provisioning: boolean, hostId?: string) => void;
	setProjectCreationPending: (pending: boolean) => void;
	setOrchestratorReplacementError: (projectId: string, failure: OrchestratorReplacementFailure | null) => void;
	setOrchestratorStartupError: (projectId: string, message: string | null, hostId?: string) => void;
	showGlobalToast: (title: string, body?: string, style?: GlobalToast["tone"] | GlobalToast["placement"] | GlobalToastOptions) => void;
	dismissGlobalToast: (nonce: number) => void;
	clearGlobalToast: () => void;
	requestNewTask: (projectId: string, hostId?: string, backlog?: NewTaskBacklog) => void;
	requestBacklogCapture: (title?: string) => void;
	requestLaneOrchestrator: (projectId: string) => void;
	requestWorkspaceFileOpen: (sessionId: string, path: string, hostId?: string, source?: "artifact") => void;
	clearWorkspaceFileOpenRequest: (nonce: number) => void;
	requestCreateProject: () => void;
	requestCreateProjectFromPath: (path: string) => void;
	requestNewShellTerminal: () => void;
	setActiveShellTerminal: (handleId: string | null) => void;
	setVisibleTerminalKind: (sessionId: string, kind: TerminalTarget["kind"]) => void;
	clearVisibleTerminalKind: (sessionId: string) => void;
};

export type OrchestratorReplacementFailure = {
	message: string;
	code?: string;
	requestId?: string;
	details?: Record<string, unknown>;
};

const sidebarStorageKey = "ao.sidebar.open";
const inspectorViewsStorageKey = "tallpine.inspectorViews";
const railExpandedStorageKey = "ao.rail.expanded";
const remoteHostsStorageKey = "ao.remoteHosts";
const diagnosticsStorageKey = "ao.diagnostics";
const sideChatsStorageKey = "ao.sideChats";
const terminalCopyOnSelectStorageKey = "ao.terminalCopyOnSelect";
function getLocalStorage() {
	if (typeof window === "undefined" || !window.localStorage) return null;
	return window.localStorage;
}

function initialSidebarOpen() {
	return getLocalStorage()?.getItem(sidebarStorageKey) !== "false";
}

/** The Developer mode master switch is gone. Keep each user's effective state:
 * with it off, the Remote hosts, Diagnostics and Side chats flags it hid become
 * off; with it on they keep their stored values. The retired key is then removed. */
export function migrateDeveloperModeFlags(storage: Pick<Storage, "getItem" | "setItem" | "removeItem"> | null) {
	if (!storage) return;
	const legacy = storage.getItem(legacyDeveloperModeStorageKey);
	if (legacy === null) return;
	if (legacy === "true") {
		// Remote hosts used to default on once Developer mode was on.
		if (storage.getItem(remoteHostsStorageKey) === null) storage.setItem(remoteHostsStorageKey, "true");
	} else {
		storage.setItem(remoteHostsStorageKey, "false");
		storage.setItem(diagnosticsStorageKey, "false");
		storage.setItem(sideChatsStorageKey, "false");
	}
	storage.removeItem(legacyDeveloperModeStorageKey);
}

function initialRemoteHosts() {
	// Off until switched on (or carried over by the Developer mode migration).
	return getLocalStorage()?.getItem(remoteHostsStorageKey) === "true";
}

function initialDiagnostics() {
	return getLocalStorage()?.getItem(diagnosticsStorageKey) === "true";
}

function initialSideChats() {
	return getLocalStorage()?.getItem(sideChatsStorageKey) === "true";
}

function initialWindowTranslucency() {
	return getLocalStorage()?.getItem(windowTranslucencyStorageKey) === "true";
}

function initialTerminalCopyOnSelect() {
	return getLocalStorage()?.getItem(terminalCopyOnSelectStorageKey) !== "false";
}

/** The tab the user last picked per session; automatic reveals (Browser) never land here. */
function rememberedInspectorViews(): Record<string, InspectorView> {
	try {
		const parsed: unknown = JSON.parse(getLocalStorage()?.getItem(inspectorViewsStorageKey) ?? "{}");
		return parsed && typeof parsed === "object" ? (parsed as Record<string, InspectorView>) : {};
	} catch {
		return {};
	}
}

function rememberInspectorView(sessionId: string, view: InspectorView) {
	getLocalStorage()?.setItem(inspectorViewsStorageKey, JSON.stringify({ ...rememberedInspectorViews(), [sessionId]: view }));
}

function inspectorState(sessions: Record<string, InspectorSessionState>, sessionId: string): InspectorSessionState {
	return sessions[sessionId] ?? { isOpen: false, view: "summary" };
}

/** Opening a session keeps the inspector closed until the user (or a browser
 *  reveal) opens it; read every open check through here so that default can't drift. */
export function inspectorIsOpen(sessions: Record<string, InspectorSessionState>, sessionId: string): boolean {
	return sessions[sessionId]?.isOpen ?? false;
}

/**
 * The display mode the user picked for this file in this open request, if any.
 * A newer open request (a different key) chooses its own mode instead.
 */
export function rememberedFileDisplayMode(
	state: Pick<UiState, "inspectorSessions">,
	sessionId: string,
	path: string | null,
	requestKey: number,
): FileDisplayMode | undefined {
	if (!path) return undefined;
	const remembered = state.inspectorSessions[sessionId]?.fileDisplayModes?.[path];
	return remembered?.requestKey === requestKey ? remembered.mode : undefined;
}

type SidebarVisibilityState = Pick<UiState, "isSidebarOpen"> & Partial<Pick<UiState, "isNarrowShell" | "isNarrowPanelOpen">>;

export function sidebarIsVisible(state: SidebarVisibilityState): boolean {
	return state.isNarrowShell ? Boolean(state.isNarrowPanelOpen) : state.isSidebarOpen;
}

/** The expanded sidebar occupies shell layout; a user close or a narrow-window overlay does not. */
export function sidebarOccupiesLayout(state: SidebarVisibilityState): boolean {
	return !state.isNarrowShell && state.isSidebarOpen;
}

const initialThemePreference = readStoredThemePreference();
const initialThemeStyle = readStoredThemeStyle();
migrateDeveloperModeFlags(getLocalStorage());

export const useUiStore = create<UiState>((set, get) => ({
	workbenchTab: "changes",
	isSidebarOpen: initialSidebarOpen(),
	isRailExpanded: getLocalStorage()?.getItem(railExpandedStorageKey) === "true",
	lastSessionsPath: "/sessions",
	setLastSessionsPath: (path) => set((state) => (state.lastSessionsPath === path ? state : { lastSessionsPath: path })),
	isNarrowShell: false,
	isNarrowPanelOpen: false,
	inspectorSessions: {},
	isCommandPaletteOpen: false,
	settingsModal: null,
	themePreference: initialThemePreference,
	resolvedTheme: resolveTheme(initialThemePreference),
	themeStyle: initialThemeStyle,
	remoteHosts: initialRemoteHosts(),
	diagnostics: initialDiagnostics(),
	sideChats: initialSideChats(),
	windowTranslucency: initialWindowTranslucency(),
	terminalCopyOnSelect: initialTerminalCopyOnSelect(),
	restartingProjectIds: new Set<string>(),
	provisioningProjectIds: new Set<string>(),
	projectCreationPending: false,
	orchestratorReplacementErrors: {},
	orchestratorStartupErrors: {},
	globalToasts: [],
	globalToast: null,
	globalToastSequence: 0,
	newTaskRequest: null,
	backlogCaptureRequest: null,
	laneOrchestratorRequest: null,
	workspaceFileOpenRequest: null,
	createProjectNonce: 0,
	folderDropRequest: null,
	newShellTerminalNonce: 0,
	activeShellTerminalHandleId: null,
	visibleTerminalKindBySession: {},
	setWorkbenchTab: (workbenchTab) => set({ workbenchTab }),
	setThemePreference: (themePreference) => {
		if (get().themePreference === themePreference) return;
		runThemeTransition(() => {
			const resolvedTheme = resolveTheme(themePreference);
			getLocalStorage()?.setItem(themeStorageKey, themePreference);
			applyDocumentTheme(resolvedTheme);
			set({ themePreference, resolvedTheme });
		});
	},
	setThemeStyle: (themeStyle) => {
		if (get().themeStyle === themeStyle) return;
		// Choosing the theme being previewed keeps the preview as-is. A View
		// Transition here would apply it a frame late, after the menu's close
		// has restored the old theme, and flash between the two.
		if (isDocumentThemeStyle(themeStyle)) {
			getLocalStorage()?.setItem(themeStyleStorageKey, themeStyle);
			set({ themeStyle });
			return;
		}
		runThemeTransition(() => {
			getLocalStorage()?.setItem(themeStyleStorageKey, themeStyle);
			applyDocumentThemeStyle(themeStyle);
			set({ themeStyle });
		});
	},
	setRemoteHosts: (remoteHosts) => {
		getLocalStorage()?.setItem(remoteHostsStorageKey, String(remoteHosts));
		set({ remoteHosts });
	},
	setDiagnostics: (diagnostics) => {
		getLocalStorage()?.setItem(diagnosticsStorageKey, String(diagnostics));
		set({ diagnostics });
	},
	setSideChats: (sideChats) => {
		getLocalStorage()?.setItem(sideChatsStorageKey, String(sideChats));
		set({ sideChats });
	},
	setWindowTranslucency: (windowTranslucency) => {
		getLocalStorage()?.setItem(windowTranslucencyStorageKey, String(windowTranslucency));
		set({ windowTranslucency });
	},
	setTerminalCopyOnSelect: (terminalCopyOnSelect) => {
		getLocalStorage()?.setItem(terminalCopyOnSelectStorageKey, String(terminalCopyOnSelect));
		set({ terminalCopyOnSelect });
	},
	openGlobalSettings: (section, options) => set((state) => ({
		settingsModal: {
			scope: "global",
			section,
			...(options?.focusAgentId ? { focusAgentId: options.focusAgentId } : {}),
			...(options?.hostId && options.hostId !== "local" ? { hostId: options.hostId } : {}),
			...(options?.startLogin && options.focusAgentId ? { startLogin: true } : {}),
			...(options?.showDiagnostics && options.focusAgentId ? { showDiagnostics: options.showDiagnostics } : {}),
			...(options?.preserveProject && state.settingsModal?.scope === "project"
				? { returnTo: state.settingsModal }
				: options?.preserveProject && state.settingsModal?.scope === "global" && state.settingsModal.returnTo
					? { returnTo: state.settingsModal.returnTo }
					: {}),
		},
	})),
	openProjectSettings: (projectId, options) => set({
		settingsModal: {
			scope: "project",
			projectId,
			...(typeof options === "string" && options !== "local" ? { hostId: options } : {}),
			...(typeof options === "object" && options?.section ? { section: options.section } : {}),
		},
	}),
	closeSettings: () => set((state) => ({
		settingsModal: state.settingsModal?.scope === "global" ? state.settingsModal.returnTo ?? null : null,
	})),
	syncSystemTheme: () => {
		const { themePreference, resolvedTheme } = get();
		if (themePreference !== "system") return;
		const next = systemTheme();
		if (next === resolvedTheme) return;
		runThemeTransition(() => {
			applyDocumentTheme(next);
			set({ resolvedTheme: next });
		});
	},
	setNarrowShell: (narrow) =>
		set((state) => (state.isNarrowShell === narrow ? state : { isNarrowShell: narrow, isNarrowPanelOpen: false })),
	closeNarrowPanel: () => set((state) => (state.isNarrowPanelOpen ? { isNarrowPanelOpen: false } : state)),
	toggleRailExpanded: () =>
		set((state) => {
			const isRailExpanded = !state.isRailExpanded;
			getLocalStorage()?.setItem(railExpandedStorageKey, String(isRailExpanded));
			return { isRailExpanded };
		}),
	toggleSidebar: () =>
		set((state) => {
			// The overlay is a transient peek: it must not overwrite the saved preference.
			if (state.isNarrowShell) return { isNarrowPanelOpen: !state.isNarrowPanelOpen };
			const isSidebarOpen = !state.isSidebarOpen;
			getLocalStorage()?.setItem(sidebarStorageKey, String(isSidebarOpen));
			return { isSidebarOpen };
		}),
	setInspectorOpen: (sessionId, isOpen) =>
		set((state) => {
			const current = inspectorState(state.inspectorSessions, sessionId);
			return {
				inspectorSessions: {
					...state.inspectorSessions,
					[sessionId]: { ...current, isOpen },
				},
			};
		}),
	toggleInspector: (sessionId) =>
		set((state) => {
			const current = inspectorState(state.inspectorSessions, sessionId);
			return {
				inspectorSessions: {
					...state.inspectorSessions,
					[sessionId]: { ...current, isOpen: !current.isOpen },
				},
			};
		}),
	setInspectorView: (sessionId, view, remember = false) =>
		set((state) => {
			if (remember) rememberInspectorView(sessionId, view);
			const current = inspectorState(state.inspectorSessions, sessionId);
			const browserUnseen = view === "browser" ? false : current.browserUnseen;
			return {
				inspectorSessions: {
					...state.inspectorSessions,
					[sessionId]: { ...current, view, browserUnseen },
				},
			};
		}),
	initializeInspectorSession: (sessionId, hasBrowserContent, hasInspector) =>
		set((state) => {
			// Sessions without an inspector (e.g. orchestrator sessions) must not
			// gain a store entry at all — leave inspectorSessions[sessionId]
			// undefined so callers that key off its presence stay correct.
			if (!hasInspector) return state;
			const current = inspectorState(state.inspectorSessions, sessionId);
			if (current.initialized) return state;
			return {
				inspectorSessions: {
					...state.inspectorSessions,
					[sessionId]: {
						...current,
						initialized: true,
						view: rememberedInspectorViews()[sessionId] ?? "summary",
						browserContentRevealed: current.browserContentRevealed ?? hasBrowserContent,
					},
				},
			};
		}),
	setBrowserContentRevealed: (sessionId, browserContentRevealed) =>
		set((state) => {
			const current = inspectorState(state.inspectorSessions, sessionId);
			if (Boolean(current.browserContentRevealed) === browserContentRevealed) return state;
			return {
				inspectorSessions: {
					...state.inspectorSessions,
					[sessionId]: {
						...current,
						browserContentRevealed,
						browserUnseen: browserContentRevealed ? current.browserUnseen : false,
					},
				},
			};
		}),
	setBrowserUnseen: (sessionId, browserUnseen) =>
		set((state) => {
			const current = inspectorState(state.inspectorSessions, sessionId);
			if (Boolean(current.browserUnseen) === browserUnseen) return state;
			return {
				inspectorSessions: {
					...state.inspectorSessions,
					[sessionId]: { ...current, browserUnseen },
				},
			};
		}),
	setFilesChangedOnly: (sessionId, filesChangedOnly) =>
		set((state) => {
			const current = inspectorState(state.inspectorSessions, sessionId);
			if ((current.filesChangedOnly ?? true) === filesChangedOnly) return state;
			return {
				inspectorSessions: {
					...state.inspectorSessions,
					[sessionId]: { ...current, filesChangedOnly },
				},
			};
		}),
	setFilesSelection: (sessionId, filesSelection) =>
		set((state) => {
			const current = inspectorState(state.inspectorSessions, sessionId);
			if (current.filesSelection === filesSelection) return state;
			return {
				inspectorSessions: {
					...state.inspectorSessions,
					[sessionId]: { ...current, filesSelection },
				},
			};
		}),
	resetFilesSelection: (sessionId) =>
		set((state) => {
			const current = state.inspectorSessions[sessionId];
			if (!current?.filesSelection) return state;
			return {
				inspectorSessions: {
					...state.inspectorSessions,
					[sessionId]: { ...current, filesSelection: undefined },
				},
			};
		}),
	setFileDisplayMode: (sessionId, path, mode, requestKey) =>
		set((state) => {
			const current = inspectorState(state.inspectorSessions, sessionId);
			const previous = current.fileDisplayModes?.[path];
			if (previous?.mode === mode && previous.requestKey === requestKey) return state;
			return {
				inspectorSessions: {
					...state.inspectorSessions,
					[sessionId]: { ...current, fileDisplayModes: { ...current.fileDisplayModes, [path]: { mode, requestKey } } },
				},
			};
		}),
	setCommandPaletteOpen: (isCommandPaletteOpen) => set({ isCommandPaletteOpen }),
	setProjectRestarting: (projectId, restarting, hostId) =>
		set((state) => {
			const restartingProjectIds = new Set(state.restartingProjectIds);
			const key = sessionUiKey(projectId, hostId);
			if (restarting) {
				restartingProjectIds.add(key);
			} else {
				restartingProjectIds.delete(key);
			}
			return { restartingProjectIds };
		}),
	setProjectCreationPending: (pending) => set({ projectCreationPending: pending }),
	setProjectProvisioning: (projectId, provisioning, hostId) =>
		set((state) => {
			const provisioningProjectIds = new Set(state.provisioningProjectIds);
			const key = sessionUiKey(projectId, hostId);
			if (provisioning) {
				provisioningProjectIds.add(key);
			} else {
				provisioningProjectIds.delete(key);
			}
			return { provisioningProjectIds };
		}),
	setOrchestratorReplacementError: (projectId, failure) =>
		set((state) => {
			const orchestratorReplacementErrors = { ...state.orchestratorReplacementErrors };
			if (failure) {
				orchestratorReplacementErrors[projectId] = failure;
			} else {
				delete orchestratorReplacementErrors[projectId];
			}
			return { orchestratorReplacementErrors };
		}),
	setOrchestratorStartupError: (projectId, message, hostId) =>
		set((state) => {
			const orchestratorStartupErrors = { ...state.orchestratorStartupErrors };
			const key = sessionUiKey(projectId, hostId);
			if (message) {
				orchestratorStartupErrors[key] = message;
			} else {
				delete orchestratorStartupErrors[key];
			}
			return { orchestratorStartupErrors };
		}),
	showGlobalToast: (title, body, style) =>
		set((state) => {
			const nonce = state.globalToastSequence + 1;
			const options = typeof style === "object" ? style : undefined;
			const tone = options?.tone ?? (style === "error" || style === "info" ? style : "info");
			const placement = options?.placement ?? (style === "top-center" || style === "bottom-right" ? style : "bottom-right");
			const toast = { title, body, tone, placement, nonce, ...options };
			const globalToasts = toast.dedupeKey
				? state.globalToasts.filter((existing) => existing.dedupeKey !== toast.dedupeKey)
				: state.globalToasts;
			return { globalToast: toast, globalToasts: [...globalToasts, toast], globalToastSequence: nonce };
		}),
	dismissGlobalToast: (nonce) =>
		set((state) => ({
			globalToasts: state.globalToasts.filter((toast) => toast.nonce !== nonce),
			globalToast: state.globalToast?.nonce === nonce ? null : state.globalToast,
		})),
	clearGlobalToast: () => set({ globalToast: null, globalToasts: [], globalToastSequence: 0 }),
	requestLaneOrchestrator: (projectId) =>
		set((state) => ({ laneOrchestratorRequest: { projectId, nonce: (state.laneOrchestratorRequest?.nonce ?? 0) + 1 } })),
	requestBacklogCapture: (title = "") =>
		set((state) => ({ backlogCaptureRequest: { title, nonce: (state.backlogCaptureRequest?.nonce ?? 0) + 1 } })),
	requestNewTask: (projectId, hostId, backlog) => {
		// Central gate: every New Task entry point (buttons, sidebar menus,
		// shortcuts) funnels through here, so a project whose orchestrator is
		// still provisioning cannot start tasks before it exists.
		if (get().provisioningProjectIds.has(sessionUiKey(projectId, hostId))) {
			get().showGlobalToast(
				"Project is still being set up",
				"The orchestrator is starting. Try again in a moment.",
				"info",
			);
			return;
		}
		set((state) => ({ newTaskRequest: { projectId, hostId, ...(backlog ? { backlog } : {}), nonce: (state.newTaskRequest?.nonce ?? 0) + 1 } }));
	},
	requestWorkspaceFileOpen: (sessionId, path, hostId, source) =>
		set((state) => ({
			workspaceFileOpenRequest: {
				sessionId,
				path,
				...(hostId ? { hostId } : {}),
				...(source ? { source } : {}),
				nonce: (state.workspaceFileOpenRequest?.nonce ?? 0) + 1,
			},
		})),
	clearWorkspaceFileOpenRequest: (nonce) =>
		set((state) => state.workspaceFileOpenRequest?.nonce === nonce
			? { workspaceFileOpenRequest: null }
			: state),
	requestCreateProject: () => set((state) => ({ createProjectNonce: state.createProjectNonce + 1 })),
	requestCreateProjectFromPath: (path) =>
		set((state) => ({ folderDropRequest: { path, nonce: (state.folderDropRequest?.nonce ?? 0) + 1 } })),
	requestNewShellTerminal: () => set((state) => ({ newShellTerminalNonce: state.newShellTerminalNonce + 1 })),
	setActiveShellTerminal: (activeShellTerminalHandleId) => set({ activeShellTerminalHandleId }),
	setVisibleTerminalKind: (sessionId, kind) =>
		set((state) =>
			state.visibleTerminalKindBySession[sessionId] === kind
				? state
				: { visibleTerminalKindBySession: { ...state.visibleTerminalKindBySession, [sessionId]: kind } },
		),
	clearVisibleTerminalKind: (sessionId) =>
		set((state) => {
			if (!(sessionId in state.visibleTerminalKindBySession)) return state;
			const visibleTerminalKindBySession = { ...state.visibleTerminalKindBySession };
			delete visibleTerminalKindBySession[sessionId];
			return { visibleTerminalKindBySession };
		}),
}));

// Hydration synchronizes legacy renderer-only Developer Mode state into the
// main-process updater mirror. Until this completes, the updater is fail-closed.

export function useResolvedTheme(): Theme {
	return useUiStore((state) => state.resolvedTheme);
}
