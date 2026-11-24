import { contextBridge, ipcRenderer, webUtils } from "electron";
import { browserBridge } from "./preload-browser";
import { APP_SHORTCUT_CHANNEL, CLOSE_SHELL_TERMINAL_SHORTCUT_CHANNEL, KEYBOARD_SHORTCUTS_HELP_CHANNEL, NEXT_SESSION_SHORTCUT_CHANNEL, NEXT_TAB_SHORTCUT_CHANNEL, NEW_SESSION_SHORTCUT_CHANNEL, NEW_SHELL_TERMINAL_SHORTCUT_CHANNEL, OPEN_SETTINGS_SHORTCUT_CHANNEL, PREVIOUS_SESSION_SHORTCUT_CHANNEL, PREVIOUS_TAB_SHORTCUT_CHANNEL, QUIT_CONFIRM_REQUEST_CHANNEL, QUIT_CONFIRM_RESULT_CHANNEL, SET_CLOSE_SHELL_TERMINAL_SHORTCUT_ENABLED_CHANNEL, SET_TERMINAL_FOCUSED_CHANNEL, TERMINAL_FONT_SIZE_SHORTCUT_CHANNEL, type AppShortcutEvent, type KeybindingOverrides, type QuitConfirmRequest } from "./shared/shortcuts";
import {
	SET_CHAT_DRAFT_RISK_CHANNEL,
	type ChatDraftBoundaryKind,
	type ChatDraftDialogCopy,
} from "./shared/chat-draft-risk";
import {
	TRAY_OPEN_SESSION_CHANNEL,
	TRAY_RENDERER_READY_CHANNEL,
	TRAY_SET_ATTENTION_STATE_CHANNEL,
	type TrayAttentionState,
	type TrayOpenSessionTarget,
} from "./shared/tray";
import type { DaemonStatus } from "./shared/daemon-status";
import { DAEMON_WS_CLOSE_CHANNEL, DAEMON_WS_EVENT_CHANNEL, DAEMON_WS_OPEN_CHANNEL, DAEMON_WS_SEND_CHANNEL, type DaemonWsEvent } from "./shared/daemon-ws";
import type { RemoteHostView } from "./main/remotes-ipc";
import type { ConnectedHostView } from "./main/remote-registry";
import type { RemoteHealth } from "./main/remote-request";
import type {
	EditorHandoffState,
	EditorHandoffStateOptions,
	OpenSessionArtifactsInput,
	OpenSessionTargetInput,
	OpenSessionTargetResult,
} from "./shared/editor-handoff";
import type { LocalPathAction, LocalPathInspection, LocalPathResult } from "./shared/local-path";
import type { UiSettings } from "./main/ui-settings";
import type { UpdatePreferences, UpdateSettings, UpdateStatus } from "./main/update-settings";
import type {
	BrowserProfile,
	BrowserProfileListState,
	BrowserProfileUsageResult,
	BrowserProfileDeleteResult,
} from "./shared/browser-profiles";
import type {
	BrowserImportDiscovery,
	BrowserImportDiscoveryRequest,
	BrowserImportProgress,
	BrowserImportRequest,
	BrowserImportResult,
} from "./shared/browser-profile-import";

if (typeof document !== "undefined") {
	const markNativeBrowserComposition = () => {
		const root = document.documentElement;
		if (root) {
			root.dataset.nativeBrowserComposition = "true";
			root.dataset.tallpinePlatform = process.platform;
		}
	};
	if (document.readyState === "loading") {
		document.addEventListener("DOMContentLoaded", markNativeBrowserComposition, { once: true });
	} else {
		markNativeBrowserComposition();
	}
}

export type ImportFolderMode = "project" | "workspace";

export type ImportRepoScan = {
	name: string;
	path: string;
	relativePath: string;
	branch: string;
	remote: string;
	hasRemote: boolean;
	isRepo: boolean;
	hasCommit: boolean;
	status?: "ok" | "error";
	reason?: string;
	needsGitInit?: boolean;
};

export type ImportFolderScan = {
	path: string;
	repos: ImportRepoScan[];
	setupWarning?: string;
};

// A folder-drop path can arrive (cold start, or an early second-instance)
// before ShellLayout's own effect has registered app.onOpenFolderPath's
// listener below — React mounts TrayRuntime's child effect (which pings
// main.ts's readiness flush) before ShellLayout's parent effect that installs
// this listener. One dispatcher registered here, at preload module load
// (guaranteed to run before any renderer/React code), either forwards
// directly to the active listener or buffers a path that arrives too early —
// never both, so a normally delivered path can never be left in the buffer
// to be replayed by a later resubscription.
let bufferedOpenFolderPath: string | null = null;
let activeOpenFolderPathListener: ((path: string) => void) | null = null;
ipcRenderer.on("app:openFolderPath", (_event, path: string) => {
	if (activeOpenFolderPathListener) {
		activeOpenFolderPathListener(path);
	} else {
		bufferedOpenFolderPath = path;
	}
});

const api = {
	app: {
		getVersion: () => ipcRenderer.invoke("app:getVersion") as Promise<string>,
		chooseDirectory: (input?: string | { title?: string; defaultPath?: string }) => ipcRenderer.invoke("app:chooseDirectory", input) as Promise<string | null>,
		checkGitRepository: (remoteUrl: string) => ipcRenderer.invoke("app:checkGitRepository", remoteUrl) as Promise<boolean>,
		openExternal: (url: string) => ipcRenderer.invoke("app:openExternal", url) as Promise<void>,
		daemonTcpOpen: () => ipcRenderer.invoke("app:daemonTcpOpen") as Promise<boolean>,
		scanImportFolder: (input: { path: string; mode: ImportFolderMode }) =>
			ipcRenderer.invoke("app:scanImportFolder", input) as Promise<ImportFolderScan>,
		checkAncestorRepo: (path: string) =>
			ipcRenderer.invoke("app:checkAncestorRepo", path) as Promise<string | undefined>,
		getRepositoryBranch: (path: string) =>
			ipcRenderer.invoke("app:getRepositoryBranch", path) as Promise<string | undefined>,
		getGitHubLogin: (repoPath?: string) => ipcRenderer.invoke("app:getGitHubLogin", repoPath) as Promise<string>,
		getCachedGitHubOwners: () => ipcRenderer.invoke("app:getCachedGitHubOwners") as Promise<Array<{ login: string; avatarUrl: string }>>,
		refreshGitHubOwners: () => ipcRenderer.invoke("app:refreshGitHubOwners") as Promise<Array<{ login: string; avatarUrl: string }>>,
		checkGitHubRepositoryAvailability: (input: { owner: string; name: string }) =>
			ipcRenderer.invoke("app:checkGitHubRepositoryAvailability", input) as Promise<{ available: boolean; message?: string }>,
		// Resolves a dropped File's real filesystem path. Synchronous passthrough
		// (not ipcRenderer.invoke — a File can't cross that boundary) so it must be
		// called directly on the File from a drop event, in the same tick, per
		// Electron's documented webUtils usage.
		getPathForFile: (file: File) => webUtils.getPathForFile(file),
		// Fired by the main process when a folder is dropped onto the app's
		// taskbar icon/shortcut (cold start or an already-running instance).
		onOpenFolderPath: (listener: (path: string) => void) => {
			activeOpenFolderPathListener = listener;
			if (bufferedOpenFolderPath) {
				const path = bufferedOpenFolderPath;
				bufferedOpenFolderPath = null;
				listener(path);
			}
			return () => {
				if (activeOpenFolderPathListener === listener) activeOpenFolderPathListener = null;
			};
		},
		// Fired by the main process when the app-level new-session shortcut
		// (⌘N / Ctrl+Shift+N) is pressed in any web contents.
		onNewSessionShortcut: (listener: () => void) => {
			const wrapped = () => listener();
			ipcRenderer.on(NEW_SESSION_SHORTCUT_CHANNEL, wrapped);
			return () => {
				ipcRenderer.off(NEW_SESSION_SHORTCUT_CHANNEL, wrapped);
			};
		},
		onKeyboardShortcutsHelp: (listener: () => void) => {
			const wrapped = () => listener();
			ipcRenderer.on(KEYBOARD_SHORTCUTS_HELP_CHANNEL, wrapped);
			return () => {
				ipcRenderer.off(KEYBOARD_SHORTCUTS_HELP_CHANNEL, wrapped);
			};
		},
		// ⌘Q on macOS: show the quit confirm, then answer with resolveQuitConfirm.
		onQuitConfirmRequest: (listener: (request: QuitConfirmRequest) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, request?: Partial<QuitConfirmRequest>) =>
				listener({ keepsRunning: request?.keepsRunning === true });
			ipcRenderer.on(QUIT_CONFIRM_REQUEST_CHANNEL, wrapped);
			return () => {
				ipcRenderer.off(QUIT_CONFIRM_REQUEST_CHANNEL, wrapped);
			};
		},
		resolveQuitConfirm: (confirmed: boolean) => ipcRenderer.send(QUIT_CONFIRM_RESULT_CHANNEL, confirmed),
		// Fired by the main process when ⌘T / Ctrl+T is pressed in any web contents,
		// including while focus is inside a terminal pane.
		onNewShellTerminalShortcut: (listener: () => void) => {
			const wrapped = () => listener();
			ipcRenderer.on(NEW_SHELL_TERMINAL_SHORTCUT_CHANNEL, wrapped);
			return () => {
				ipcRenderer.off(NEW_SHELL_TERMINAL_SHORTCUT_CHANNEL, wrapped);
			};
		},
		onCloseShellTerminalShortcut: (listener: () => void) => {
			const wrapped = () => listener();
			ipcRenderer.on(CLOSE_SHELL_TERMINAL_SHORTCUT_CHANNEL, wrapped);
			return () => {
				ipcRenderer.off(CLOSE_SHELL_TERMINAL_SHORTCUT_CHANNEL, wrapped);
			};
		},
		setCloseShellTerminalShortcutEnabled: (enabled: boolean) => {
			ipcRenderer.send(SET_CLOSE_SHELL_TERMINAL_SHORTCUT_ENABLED_CHANNEL, enabled);
		},
		setChatDraftRisk: (risks: readonly ChatDraftBoundaryKind[], copy?: ChatDraftDialogCopy) => {
			ipcRenderer.send(SET_CHAT_DRAFT_RISK_CHANNEL, risks, copy);
		},
		onOpenSettingsShortcut: (listener: (section?: string) => void) => {
			const wrapped = (_event: unknown, section?: unknown) => listener(typeof section === "string" ? section : undefined);
			ipcRenderer.on(OPEN_SETTINGS_SHORTCUT_CHANNEL, wrapped);
			return () => {
				ipcRenderer.off(OPEN_SETTINGS_SHORTCUT_CHANNEL, wrapped);
			};
		},
		onPreviousSessionShortcut: (listener: () => void) => {
			const wrapped = () => listener();
			ipcRenderer.on(PREVIOUS_SESSION_SHORTCUT_CHANNEL, wrapped);
			return () => {
				ipcRenderer.off(PREVIOUS_SESSION_SHORTCUT_CHANNEL, wrapped);
			};
		},
		onNextSessionShortcut: (listener: () => void) => {
			const wrapped = () => listener();
			ipcRenderer.on(NEXT_SESSION_SHORTCUT_CHANNEL, wrapped);
			return () => {
				ipcRenderer.off(NEXT_SESSION_SHORTCUT_CHANNEL, wrapped);
			};
		},
		onPreviousTabShortcut: (listener: () => void) => {
			const wrapped = () => listener();
			ipcRenderer.on(PREVIOUS_TAB_SHORTCUT_CHANNEL, wrapped);
			return () => {
				ipcRenderer.off(PREVIOUS_TAB_SHORTCUT_CHANNEL, wrapped);
			};
		},
		onNextTabShortcut: (listener: () => void) => {
			const wrapped = () => listener();
			ipcRenderer.on(NEXT_TAB_SHORTCUT_CHANNEL, wrapped);
			return () => {
				ipcRenderer.off(NEXT_TAB_SHORTCUT_CHANNEL, wrapped);
			};
		},
		onShortcut: (listener: (event: AppShortcutEvent) => void) => {
			const wrapped = (_event: unknown, shortcut: AppShortcutEvent) => listener(shortcut);
			ipcRenderer.on(APP_SHORTCUT_CHANNEL, wrapped);
			return () => {
				ipcRenderer.off(APP_SHORTCUT_CHANNEL, wrapped);
			};
		},
	},
	terminal: {
		saveDroppedFile: (input: { name: string; bytes: Uint8Array }) =>
			ipcRenderer.invoke("terminal:saveDroppedFile", input) as Promise<string>,
		setFocused: (focused: boolean, handlesFontSize = false) =>
			ipcRenderer.send(SET_TERMINAL_FOCUSED_CHANNEL, focused, handlesFontSize),
		onFontSizeShortcut: (listener: (delta: -1 | 1) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, delta: -1 | 1) => listener(delta);
			ipcRenderer.on(TERMINAL_FONT_SIZE_SHORTCUT_CHANNEL, wrapped);
			return () => {
				ipcRenderer.off(TERMINAL_FONT_SIZE_SHORTCUT_CHANNEL, wrapped);
			};
		},
	},
	window: {
		getZoomFactor: () => ipcRenderer.invoke("window:getZoomFactor") as Promise<number>,
		onZoomFactor: (listener: (zoomFactor: number) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, zoomFactor: number) => listener(zoomFactor);
			ipcRenderer.on("window:zoom", wrapped);
			return () => { ipcRenderer.off("window:zoom", wrapped); };
		},
		isMaximized: () => ipcRenderer.invoke("window:isMaximized") as Promise<boolean>,
		onMaximized: (listener: (maximized: boolean) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, maximized: boolean) => listener(maximized);
			ipcRenderer.on("window:maximized", wrapped);
			return () => {
				ipcRenderer.off("window:maximized", wrapped);
			};
		},
		isFullScreen: () => ipcRenderer.invoke("window:isFullScreen") as Promise<boolean>,
		onFullScreen: (listener: (fullScreen: boolean) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, fullScreen: boolean) => listener(fullScreen);
			ipcRenderer.on("window:fullscreen", wrapped);
			return () => {
				ipcRenderer.off("window:fullscreen", wrapped);
			};
		},
	},
	theme: {
		// Propagate the app's theme preference to Electron's nativeTheme so embedded
		// WebContentsView previews (which follow prefers-color-scheme) stay in sync
		// with the shell. "system" lets both follow the OS.
		set: (preference: "light" | "dark" | "system") => ipcRenderer.invoke("theme:set", preference) as Promise<void>,
		persistTerminal: (scheme: "light" | "dark") =>
			ipcRenderer.invoke("theme:persist-terminal", scheme) as Promise<void>,
		// The shell goes transparent over a live native browser page, so whatever
		// shows through its unpainted gutters is the native window background.
		setWindowBackground: (color: string) => ipcRenderer.invoke("theme:set-window-background", color) as Promise<void>,
		// macOS only: swap the painted window background for native vibrancy so
		// the translucent shell blurs the desktop behind it.
		setWindowTranslucent: (enabled: boolean) =>
			ipcRenderer.invoke("theme:set-window-translucent", enabled) as Promise<void>,
	},
	menu: {
		action: (action: string) => ipcRenderer.invoke("menu:action", action) as Promise<void>,
		notifyShellFocus: () => ipcRenderer.send("shell:focus"),
	},
	clipboard: {
		writeText: (text: string) => ipcRenderer.invoke("clipboard:writeText", text) as Promise<void>,
		readText: () => ipcRenderer.invoke("clipboard:readText") as Promise<string>,
		writeImage: (bytes: Uint8Array) => ipcRenderer.invoke("clipboard:writeImage", bytes) as Promise<void>,
	},
	chatImage: {
		fetch: (url: string) => ipcRenderer.invoke("chatImage:fetch", url) as Promise<{ bytes: Uint8Array; mimeType: string }>,
		save: (input: { name: string; bytes: Uint8Array }) => ipcRenderer.invoke("chatImage:save", input) as Promise<boolean>,
	},
	daemon: {
		getStatus: () => ipcRenderer.invoke("daemon:getStatus") as Promise<DaemonStatus>,
		start: () => ipcRenderer.invoke("daemon:start") as Promise<DaemonStatus>,
		stop: () => ipcRenderer.invoke("daemon:stop") as Promise<DaemonStatus>,
		restart: () => ipcRenderer.invoke("daemon:restart") as Promise<DaemonStatus>,
		onStatus: (listener: (status: DaemonStatus) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, status: DaemonStatus) => listener(status);
			ipcRenderer.on("daemon:status", wrapped);
			return () => {
				ipcRenderer.off("daemon:status", wrapped);
			};
		},
	},
	// The shell's /mux and event-stream WebSockets, opened by main on the
	// daemon's user-only socket (B176; main/daemon-ws-bridge.ts).
	daemonSocket: {
		open: (url: string) => ipcRenderer.invoke(DAEMON_WS_OPEN_CHANNEL, url) as Promise<number | null>,
		send: (id: number, data: string) => ipcRenderer.send(DAEMON_WS_SEND_CHANNEL, id, data),
		close: (id: number) => ipcRenderer.send(DAEMON_WS_CLOSE_CHANNEL, id),
		onEvent: (listener: (event: DaemonWsEvent) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, event: DaemonWsEvent) => listener(event);
			ipcRenderer.on(DAEMON_WS_EVENT_CHANNEL, wrapped);
			return () => {
				ipcRenderer.off(DAEMON_WS_EVENT_CHANNEL, wrapped);
			};
		},
	},
	sideChats: {
		capture: (closedSideId?: string, cancelClose = false) => ipcRenderer.invoke("sideChats:capture", closedSideId, cancelClose) as Promise<void>,
	},
	editorHandoff: {
		getState: (sessionId: string, options?: EditorHandoffStateOptions) =>
			ipcRenderer.invoke("editorHandoff:getState", sessionId, options) as Promise<EditorHandoffState>,
		open: (input: OpenSessionTargetInput) =>
			ipcRenderer.invoke("editorHandoff:open", input) as Promise<OpenSessionTargetResult>,
		openArtifacts: (input: OpenSessionArtifactsInput) =>
			ipcRenderer.invoke("editorHandoff:openArtifacts", input) as Promise<OpenSessionTargetResult>,
		copyWorkspacePath: (sessionId: string) =>
			ipcRenderer.invoke("editorHandoff:copyWorkspacePath", sessionId) as Promise<void>,
	},
	localPath: {
		inspect: (filePath: string, sessionId?: string) => ipcRenderer.invoke("localPath:inspect", filePath, sessionId) as Promise<LocalPathInspection>,
		perform: (filePath: string, action: LocalPathAction) =>
			ipcRenderer.invoke("localPath:perform", filePath, action) as Promise<LocalPathResult>,
	},
	browser: browserBridge,
	browserProfiles: {
		list: () => ipcRenderer.invoke("browserProfiles:list") as Promise<BrowserProfileListState>,
		create: (name: string) => ipcRenderer.invoke("browserProfiles:create", { name }) as Promise<BrowserProfile>,
		rename: (input: { id: string; name: string }) =>
			ipcRenderer.invoke("browserProfiles:rename", input) as Promise<BrowserProfile>,
		clear: (id: string) => ipcRenderer.invoke("browserProfiles:clear", { id }) as Promise<void>,
		usage: (id: string) => ipcRenderer.invoke("browserProfiles:usage", { id }) as Promise<BrowserProfileUsageResult>,
		delete: (id: string, confirmedViewIds: string[]) => ipcRenderer.invoke("browserProfiles:delete", { id, confirmedViewIds }) as Promise<BrowserProfileDeleteResult>,
		setDefault: (id: string | null) => ipcRenderer.invoke("browserProfiles:setDefault", { id }) as Promise<void>,
		discoverImportSources: (input?: BrowserImportDiscoveryRequest) =>
			(input === undefined
				? ipcRenderer.invoke("browserProfiles:import:discover")
				: ipcRenderer.invoke("browserProfiles:import:discover", input)) as Promise<BrowserImportDiscovery>,
		import: (input: BrowserImportRequest) =>
			ipcRenderer.invoke("browserProfiles:import:start", input) as Promise<BrowserImportResult>,
		onImportProgress: (listener: (progress: BrowserImportProgress) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, progress: BrowserImportProgress) => listener(progress);
			ipcRenderer.on("browserProfiles:import:progress", wrapped);
			return () => {
				ipcRenderer.off("browserProfiles:import:progress", wrapped);
			};
		},
	},
	notifications: {
		show: (notification: { id: string; title: string; body?: string; type?: string; urgency?: string; watched?: boolean }) =>
			ipcRenderer.invoke("notifications:show", notification) as Promise<void>,
		setBadge: (count: number) => ipcRenderer.invoke("notifications:setBadge", count) as Promise<void>,
		devBounce: () => ipcRenderer.invoke("notifications:devBounce") as Promise<void>,
		onClick: (listener: (id: string) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, id: string) => listener(id);
			ipcRenderer.on("notifications:click", wrapped);
			return () => {
				ipcRenderer.off("notifications:click", wrapped);
			};
		},
		onPlaySound: (listener: () => void) => {
			const wrapped = () => listener();
			ipcRenderer.on("notifications:playSound", wrapped);
			return () => {
				ipcRenderer.off("notifications:playSound", wrapped);
			};
		},
		reportSoundFailure: () => ipcRenderer.send("notifications:soundFailed"),
	},
	tray: {
		setAttentionState: (state: TrayAttentionState) => ipcRenderer.send(TRAY_SET_ATTENTION_STATE_CHANNEL, state),
			onOpenSession: (listener: (target: TrayOpenSessionTarget) => void) => {
				const wrapped = (_event: Electron.IpcRendererEvent, target: TrayOpenSessionTarget) => listener(target);
				ipcRenderer.on(TRAY_OPEN_SESSION_CHANNEL, wrapped);
				ipcRenderer.send(TRAY_RENDERER_READY_CHANNEL);
			return () => {
				ipcRenderer.off(TRAY_OPEN_SESSION_CHANNEL, wrapped);
			};
		},
	},
	cliLink: {
		inspect: () => ipcRenderer.invoke("cliLink:inspect") as Promise<import("./shared/cli-link").CliLinkView>,
		link: (confirmReplacement: boolean, expectedTarget?: string | null) =>
			ipcRenderer.invoke("cliLink:link", confirmReplacement, expectedTarget) as Promise<import("./shared/cli-link").CliLinkResult>,
		unlink: (confirmReplacement: boolean, expectedTarget?: string | null) =>
			ipcRenderer.invoke("cliLink:unlink", confirmReplacement, expectedTarget) as Promise<import("./shared/cli-link").CliLinkResult>,
	},
	appUpdates: {
		getStatus: () => ipcRenderer.invoke("appUpdates:getStatus") as Promise<UpdateStatus>,
		getPreferences: () => ipcRenderer.invoke("appUpdates:getPreferences") as Promise<UpdatePreferences | null>,
		setSettings: (settings: UpdateSettings) => ipcRenderer.invoke("appUpdates:setSettings", settings) as Promise<void>,
		check: () => ipcRenderer.invoke("appUpdates:check") as Promise<void>,
		install: () => ipcRenderer.invoke("appUpdates:install") as Promise<boolean>,
		installWhenIdle: () => ipcRenderer.invoke("appUpdates:installWhenIdle") as Promise<boolean>,
		cancelInstallWhenIdle: () => ipcRenderer.invoke("appUpdates:cancelInstallWhenIdle") as Promise<void>,
		onStatus: (listener: (status: UpdateStatus) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, status: UpdateStatus) => listener(status);
			ipcRenderer.on("appUpdates:status", wrapped);
			return () => {
				ipcRenderer.off("appUpdates:status", wrapped);
			};
		},
	},
	uiSettings: {
		get: () => ipcRenderer.invoke("uiSettings:get") as Promise<UiSettings>,
		set: (settings: Partial<UiSettings>) => ipcRenderer.invoke("uiSettings:set", settings) as Promise<UiSettings>,
	},
	keybindings: {
		get: () => ipcRenderer.invoke("keybindings:get") as Promise<KeybindingOverrides>,
		set: (overrides: KeybindingOverrides) =>
			ipcRenderer.invoke("keybindings:set", overrides) as Promise<KeybindingOverrides>,
		setRecording: (active: boolean) => ipcRenderer.invoke("keybindings:setRecording", active) as Promise<void>,
	},
	// Saved Tallpine daemons, shared with the CLI's ~/.tallpine/remotes.json. Everything the
	// renderer receives back is password-free (see main/remotes-ipc.ts); the
	// plaintext password only travels renderer -> main on add or credential edit.
	remotes: {
		list: () => ipcRenderer.invoke("remotes:list") as Promise<RemoteHostView[]>,
		add: (input: { label: string; url: string; password: string; cert?: string }) =>
			ipcRenderer.invoke("remotes:add", input) as Promise<RemoteHealth>,
		// An edit carries only what changed: an omitted password keeps the saved
		// one, so a rotated credential is fixed without the renderer ever holding
		// the old one.
		update: (url: string, changes: { label?: string; url?: string; password?: string; cert?: string }) =>
			ipcRenderer.invoke("remotes:update", url, changes) as Promise<RemoteHealth>,
		remove: (url: string) => ipcRenderer.invoke("remotes:remove", url) as Promise<void>,
		connect: (url: string, hostId?: string) => ipcRenderer.invoke("remotes:connect", url, hostId) as Promise<ConnectedHostView>,
		disconnect: (url: string) => ipcRenderer.invoke("remotes:disconnect", url) as Promise<void>,
		// The name this machine is listed under on the account's other devices.
		certificate: (url: string) => ipcRenderer.invoke("remotes:certificate", url) as Promise<{ pin: string; cert: string }>,
		computerName: () => ipcRenderer.invoke("remotes:computerName") as Promise<string>,
		previewUrl: (hostId: string, sessionId: string, sourceUrl: string) =>
			ipcRenderer.invoke("remotes:previewUrl", hostId, sessionId, sourceUrl) as Promise<string>,
		resolvePreviewUrl: (hostId: string, sessionId: string, viewedUrl: string) =>
			ipcRenderer.invoke("remotes:resolvePreviewUrl", hostId, sessionId, viewedUrl) as Promise<string>,
		// The host's agent browser for the session's agent tab ("" when none).
		agentBrowserUrl: (hostId: string, sessionId: string) =>
			ipcRenderer.invoke("remotes:agentBrowserUrl", hostId, sessionId) as Promise<string>,
		// A preview's address for a browser outside Tallpine; any other URL unchanged.
		externalUrl: (hostId: string, url: string) =>
			ipcRenderer.invoke("remotes:externalUrl", hostId, url) as Promise<string>,
	},
};

contextBridge.exposeInMainWorld("tallpine", api);

export type TallpineBridge = typeof api;
