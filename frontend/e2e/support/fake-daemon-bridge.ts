import type { Page } from "@playwright/test";

import type { TallpineBridge } from "../../src/preload";
import type { DaemonStatus } from "../../src/shared/daemon-status";
import { coerceUiSettings, DEFAULT_UI_SETTINGS } from "../../src/shared/ui-locale";


// The e2e suite runs the renderer under `dev:web` (VITE_NO_ELECTRON=1) with no
// Electron preload, so `window.tallpine` is undefined and lib/bridge.ts falls back to
// a browser stub that reports the daemon as permanently "stopped" and the app
// version as "0.0.0-preview". The daemon/version smoke cases (DMN-*, INS-004)
// need a deterministic *ready* daemon and a known version string, so we inject
// a complete `window.tallpine` before any page script runs — the same seam the real
// Electron preload fills. This is a fake *bridge*, not a fake agent: no worker
// process and no GitHub repo are involved, matching the T0 POD constraints.
//
// In a real Linux pod running the packaged Electron build, `window.tallpine` is the
// live preload; the injected bridge is only the deterministic stand-in for the
// browser harness.
//
// SCOPE / CAVEAT — renderer smoke, not full e2e. Because `window.tallpine`,
// `EventSource`, and the workspace snapshot are all faked here, this harness
// CANNOT catch daemon, storage, API, preload, PTY, or filesystem regressions —
// those are the packaged-app pod gate's job (#2697). In particular,
// `useWorkspaceQuery` reads an already-shaped `WorkspaceSummary` straight from
// `window.__tallpineFakeAgent.snapshot()`, BYPASSING the generated API client + DTO
// mapping; DTO/client coverage comes from the pod gate + unit tests, never from
// these specs. Treat green here as "the renderer renders the injected state,"
// not "the boundary works."

export type FakeBridgeOptions = {
	/** Version string surfaced by app.getVersion(). */
	version?: string;
	/** Daemon lifecycle state the renderer should observe. */
	daemonState?: "ready" | "starting" | "stopped" | "error";
	/** REST port advertised when ready (mock data is served regardless). */
	daemonPort?: number;
};

export async function installFakeBridge(page: Page, opts: FakeBridgeOptions = {}): Promise<void> {
	const version = opts.version ?? "9.9.9-test";
	const daemonState = opts.daemonState ?? "ready";
	const daemonPort = opts.daemonPort ?? 8080;
	await page.addInitScript(
		({ version, daemonState, daemonPort }) => {
			const unsubscribe = () => () => undefined;
			const status: DaemonStatus =
				daemonState === "ready" ? { state: "ready", port: daemonPort } : { state: daemonState };
			const navState = (viewId: string) => ({
				viewId,
				url: "",
				title: "",
				canGoBack: false,
				canGoForward: false,
				isLoading: false,
			});

			// Full TallpineBridge surface (mirrors src/preload.ts) so any renderer call
			// resolves — an incomplete object would throw the moment the app touched
			// a missing method.
			const tallpine = {
					app: {
						getVersion: async () => version,
						chooseDirectory: async () => null,
						checkGitRepository: async () => true,
						openExternal: async () => undefined,
						daemonTcpOpen: async () => true,
						scanImportFolder: async ({ path }: { path: string }) => ({ path, repos: [] }),
						checkAncestorRepo: async () => undefined,
						getRepositoryBranch: async () => undefined,
						getGitHubLogin: async () => "",
						getCachedGitHubOwners: async () => [],
						refreshGitHubOwners: async () => [],
						checkGitHubRepositoryAvailability: async () => ({ available: true }),
						getPathForFile: () => "",
					onOpenFolderPath: () => () => undefined,
					onNewSessionShortcut: unsubscribe,
					onKeyboardShortcutsHelp: unsubscribe,
					onQuitConfirmRequest: unsubscribe,
					resolveQuitConfirm: () => undefined,
					onNewShellTerminalShortcut: unsubscribe,
					onCloseShellTerminalShortcut: unsubscribe,
					setCloseShellTerminalShortcutEnabled: () => undefined,
					setChatDraftRisk: () => undefined,
					onOpenSettingsShortcut: unsubscribe,
					onPreviousSessionShortcut: unsubscribe,
					onNextSessionShortcut: unsubscribe,
					onPreviousTabShortcut: unsubscribe,
					onNextTabShortcut: unsubscribe,
					onShortcut: unsubscribe,
				},
				terminal: {
					saveDroppedFile: async () => "",
					setFocused: () => undefined,
					onFontSizeShortcut: () => () => undefined,
				},
				window: {
					getZoomFactor: async () => 1,
					onZoomFactor: () => () => undefined,
					isMaximized: async () => false,
					onMaximized: () => () => undefined,
					isFullScreen: async () => false,
					onFullScreen: () => () => undefined,
				},
				theme: {
					set: async () => undefined,
					persistTerminal: async () => undefined,
					setWindowBackground: async () => undefined,
					setWindowTranslucent: async () => undefined,
				},
				menu: { action: async () => undefined, notifyShellFocus: () => undefined },
				clipboard: {
					writeText: async () => undefined,
					readText: async () => "",
					writeImage: async () => undefined,
				},
				chatImage: {
					fetch: async () => ({ bytes: new Uint8Array(), mimeType: "image/png" }),
					save: async () => true,
				},
				// No WebSocket bridge: the fake daemon is reached directly.
				daemonSocket: undefined as never,
				daemon: {
					getStatus: async () => status,
					start: async () => status,
					stop: async () => ({ state: "stopped" }),
					restart: async () => status,
					onStatus: (listener: (s: typeof status) => void) => {
						listener(status);
						return unsubscribe();
					},
				},
				sideChats: { capture: async () => undefined },
				editorHandoff: {
					copyWorkspacePath: async () => undefined,
					getState: async () => ({
						targets: [
							{ id: "cursor" as const, name: "Cursor", kind: "editor" as const },
							{ id: "file-manager" as const, name: "File Manager", kind: "file_manager" as const },
							{ id: "terminal" as const, name: "Terminal", kind: "terminal" as const },
						],
						preferredEditorId: "cursor" as const,
						workspaceAvailable: true,
					}),
					open: async () => ({ id: "cursor" as const, name: "Cursor", kind: "editor" as const }),
					openArtifacts: async () => ({ id: "file-manager" as const, name: "File Manager", kind: "file_manager" as const }),
				},
				browser: {
					reconnectRuntime: async () => undefined,
					getRuntimeState: async () => ({ connected: false, connecting: false, rejected: false }),
					nativeCompositionEnabled: true,
					ensure: async (sessionId: string) => navState(`preview:${sessionId}`),
					setBounds: () => undefined,
					onBoundsApplied: () => () => undefined,
					setOverlayOpen: () => undefined,
					navigate: async ({ viewId }: { viewId: string }) => navState(viewId),
					historySuggestions: async () => [],
					historyFavicon: async () => undefined,
					clear: async (viewId: string) => navState(viewId),
					goBack: async (viewId: string) => navState(viewId),
					goForward: async (viewId: string) => navState(viewId),
					reload: async (viewId: string) => navState(viewId),
					stop: async (viewId: string) => navState(viewId),
					getFindState: async (viewId: string) => ({
						viewId,
						tabId: "t1",
						query: "",
						activeMatchOrdinal: 0,
						matches: 0,
						finalUpdate: true,
					}),
					findInPage: async ({ viewId, query }: { viewId: string; query: string }) => ({
						viewId,
						tabId: "t1",
						query,
						activeMatchOrdinal: 0,
						matches: 0,
						finalUpdate: true,
					}),
					stopFindInPage: async ({ viewId }: { viewId: string }) => ({
						viewId,
						tabId: "t1",
						query: "",
						activeMatchOrdinal: 0,
						matches: 0,
						finalUpdate: true,
					}),
					getTabs: async (viewId: string) => ({
						viewId,
						activeTabId: "t1",
						tabs: [{ id: "t1", url: "", title: "", active: true }],
					}),
					selectTab: async ({ viewId, tabId }: { viewId: string; tabId: string }) => ({
						viewId,
						activeTabId: tabId,
						tabs: [{ id: tabId, url: "", title: "", active: true }],
					}),
					closeTab: async ({ viewId }: { viewId: string; tabId: string }) => ({
						viewId,
						activeTabId: "t1",
						tabs: [{ id: "t1", url: "", title: "", active: true }],
					}),
					openTab: async ({ viewId }: { viewId: string; url?: string }) => ({
						viewId,
						activeTabId: "t1",
						tabs: [{ id: "t1", url: "", title: "", active: true }],
					}),
					getProfile: async (viewId: string) => ({ viewId, profileId: null, temporary: true }),
					showProfileMenu: async () => undefined,
					selectProfile: async () => undefined,
					captureScreenshot: async () => undefined,
					downloads: {
						list: async () => ({ downloads: [] }),
						action: async () => ({ downloads: [] }),
						clear: async () => ({ downloads: [] }),
						onChanged: unsubscribe,
					},
					notifyPanelUsed: () => undefined,
					notifyPanelBlur: () => undefined,
					onFocusLocation: unsubscribe,
					onFindOpen: unsubscribe,
					onReopenClosedTab: unsubscribe,
					onClosePanel: unsubscribe,
					devtools: async (input: { viewId: string }) => ({ viewId: input.viewId, open: false, activeTabId: "" }),
					respondSitePermission: async () => undefined,
					resetSitePermission: async () => undefined,
					destroy: () => undefined,
					// Annotation contract (mirrors src/preload.ts): useBrowserView subscribes
					// to these whenever SessionView mounts with window.tallpine.browser present, so
					// an incomplete browser shape would crash the session-detail/preview specs.
					setAnnotationMode: async () => undefined,
					completeAnnotation: async () => undefined,
					discardAnnotations: async () => undefined,
					annotationAction: async () => undefined,
					onAnnotationSubmit: unsubscribe,
					onAnnotationCancel: unsubscribe,
					onAnnotationState: unsubscribe,
					onNavState: unsubscribe,
					onFindState: unsubscribe,
					onTabsState: unsubscribe,
					onAgentActivity: unsubscribe,
					onRuntimeState: unsubscribe,
					chromeState: async () => ({ running: false, activeTab: null }),
					chromeShow: async () => ({ shown: false }),
					chromeSignIn: async () => ({ running: false, activeTab: null }),
					chromeSignInDone: async () => ({ running: false, activeTab: null }),
					onChromeStateChanged: unsubscribe,
					handoffState: async () => null,
					handoffContinue: async () => false,
					onHandoffChanged: unsubscribe,
					onDevToolsState: unsubscribe,
					onProfileState: unsubscribe,
					onProfileManage: unsubscribe,
					onPageFocus: unsubscribe,
				},
				browserProfiles: {
					list: async () => ({ profiles: [], defaultProfileId: null }),
					create: async (name: string) => {
						const now = new Date().toISOString();
						return { id: `fake-${name}`, name, createdAt: now, updatedAt: now };
					},
					rename: async ({ id, name }: { id: string; name: string }) => {
						const now = new Date().toISOString();
						return { id, name, createdAt: now, updatedAt: now };
					},
					clear: async () => undefined,
					usage: async () => ({ sessions: [] }),
					delete: async () => ({}),
					setDefault: async () => undefined,
					discoverImportSources: async () => ({ sources: [] }),
					import: async () => ({ sourceName: "", entries: [] }),
					onImportProgress: () => () => undefined,
				},
				notifications: {
					show: async () => undefined,
					setBadge: async (_count: number) => undefined,
					devBounce: async () => undefined,
					onClick: unsubscribe,
					onPlaySound: unsubscribe,
					reportSoundFailure: () => undefined,
				},
				tray: {
					setAttentionState: () => undefined,
					onOpenSession: unsubscribe,
				},
				cliLink: {
					inspect: async () => ({ ok: false as const, code: "unsupported" as const, message: "unavailable" }),
					link: async () => ({ ok: false as const, code: "unsupported" as const, message: "unavailable" }),
					unlink: async () => ({ ok: false as const, code: "unsupported" as const, message: "unavailable" }),
				},
				appUpdates: {
					getStatus: async () => ({ state: "disabled" as const, reason: "unpackaged" as const }),
					getPreferences: async () => null,
					setSettings: async () => undefined,
					check: async () => undefined,
					install: async () => false,
					installWhenIdle: async () => false,
					cancelInstallWhenIdle: async () => undefined,
					onStatus: () => () => undefined,
				},
				uiSettings: {
					get: async () => ({ ...DEFAULT_UI_SETTINGS }),
					set: async (settings) => coerceUiSettings({ ...DEFAULT_UI_SETTINGS, ...settings }),
				},
				keybindings: {
					get: async () => ({}),
					set: async (overrides) => overrides,
					setRecording: async () => undefined,
				},
				remotes: {
					list: async () => [],
					add: async () => "offline" as const,
					update: async () => "offline" as const,
					remove: async () => undefined,
					connect: async (url: string) => {
						throw new Error(`No fake remote for ${url}`);
					},
					disconnect: async () => undefined,
					certificate: async () => ({ pin: "0".repeat(64), cert: "" }),
					computerName: async () => "E2E computer",
					previewUrl: async (_hostId: string, _sessionId: string, sourceUrl: string) => sourceUrl,
					resolvePreviewUrl: async (_hostId: string, _sessionId: string, viewedUrl: string) => viewedUrl,
					agentBrowserUrl: async () => "",
					externalUrl: async (_hostId: string, url: string) => url,
				},
				localPath: {
					inspect: async () => ({ ok: false, reason: "not_found" }),
					perform: async () => ({ ok: false, reason: "not_found" }),
				},
			} satisfies TallpineBridge;
			(window as unknown as { tallpine: unknown }).tallpine = tallpine;
		},
		{ version, daemonState, daemonPort },
	);
}
