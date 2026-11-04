import type { AgentLoginsConfirm } from "./agent-partition";
import type { IpcMain, IpcMainInvokeEvent, Rectangle, Session, View, WebContents, OpenDevToolsOptions } from "electron";
import type { BrowserCloseFailure } from "../browser-close-diagnostics";
import type { BrowserAnnotationModeInput, BrowserAnnotationSession } from "../../shared/browser-annotations";
import type { BrowserProfileId, BrowserProfileViewState, BrowserProfileUsage } from "../../shared/browser-profiles";
import type { KeybindingOverrides } from "../../shared/shortcuts";
import type { AgentBrowserRuntime } from "../agent-browser-runtime/runtime";
import type { BrowserProfileStore } from "../browser-profile-store";
import type { BrowserHistoryStore } from "../browser-history-store";
import type { TabLifecycleLimits } from "../browser-tab-lifecycle";
import type { BrowserDownloadManager } from "../browser-download-manager";
import type { RemoteTabReport } from "../../shared/remote-tab";
import { BrowserScreencast } from "../browser-screencast";
import type { BrowserLiveSink, BrowserLiveState, BrowserRemoteInput, BrowserRemoteNavigation, BrowserRemoteTabAction } from "../browser-live-types";

export type BrowserRect = Pick<Rectangle, "x" | "y" | "width" | "height">;

export type BrowserNavState = {
  viewId: string;
  url: string;
  title: string;
  canGoBack: boolean;
  canGoForward: boolean;
  isLoading: boolean;
  error?: string;
};

export type BrowserTabState = {
  id: string;
  url: string;
  title: string;
  active: boolean;
  favicon?: string;
  // Set when the tab shows the agent's own browser: the agent browser tab it
  // shows ("" until its viewer first reports).
  remote?: { tabId: string };
};

export type BrowserTabsState = {
  viewId: string;
  activeTabId: string;
  tabs: BrowserTabState[];
  change?: {
    kind: "opened" | "popup" | "selected" | "closed";
    tabId: string;
    tab?: BrowserTabState;
  };
};

export type BrowserFindState = {
  viewId: string;
  tabId: string;
  query: string;
  activeMatchOrdinal: number;
  matches: number;
  finalUpdate: boolean;
};

export type BrowserFindInput = {
  viewId: string;
  query: string;
  forward: boolean;
  newSession: boolean;
};

export type BrowserFindStopInput = {
  viewId: string;
  focusPage?: boolean;
};

export type BrowserAgentActivityState = {
  viewId: string;
  active: boolean;
  action: string;
  phase?: "started" | "finished";
  commandId?: string;
};

export type BrowserRuntimeState = {
	/** The daemon answered this app's hello with hello-ok. */
	connected: boolean;
	/** A handshake that should settle soon is in flight: the link just started, or an
	 *  accepted link dropped and is redialing. Not yet a disconnect worth showing. */
	connecting: boolean;
	/** The daemon refused this app's hello. Reconnecting with the same token can't help. */
	rejected: boolean;
	/** The daemon's reason for the refusal (a BROWSER_RUNTIME_* code), when it gave one. */
	rejectionCode?: string;
};

export type BrowserDevToolsState = {
  viewId: string;
  open: boolean;
  activeTabId: string;
  placement?: BrowserDevToolsPlacement;
};

export type BrowserDevToolsPlacement = "right" | "bottom" | "left" | "undocked";

export type BrowserDevToolsInput = {
  viewId: string;
  operation: "open" | "close" | "setPlacement";
  placement?: BrowserDevToolsPlacement;
};

export type InternalBrowserDevToolsOperation =
  | BrowserDevToolsInput["operation"]
  | "toggle";

export type BrowserBoundsInput = {
  viewId: string;
  revision: number;
  rect: BrowserRect;
  visible: boolean;
};

export type BrowserNavigateInput = {
  viewId: string;
  url: string;
};

export type BrowserHistorySuggestInput = {
  viewId: string;
  query: string;
};

export type BrowserHistoryFaviconInput = {
  viewId: string;
  url: string;
};

export type BrowserTabInput = {
  viewId: string;
  tabId: string;
};

export type BrowserOpenTabInput = {
  viewId: string;
  url?: string;
};

export type BrowserWebContents = Pick<
  WebContents,
  | "id"
  | "canGoBack"
  | "canGoForward"
  | "capturePage"
  | "clearHistory"
  | "debugger"
  | "executeJavaScript"
  | "focus"
  | "findInPage"
  | "mainFrame"
  | "getTitle"
  | "getURL"
  | "getZoomFactor"
  | "goBack"
  | "goForward"
  | "isLoading"
  | "insertCSS"
  | "loadURL"
  | "on"
  | "reload"
  | "removeInsertedCSS"
  | "send"
  | "sendInputEvent"
  | "setWebRTCIPHandlingPolicy"
  | "setWindowOpenHandler"
  | "stop"
  | "stopFindInPage"
> & {
  openDevTools?: (
    options?: Pick<OpenDevToolsOptions, "mode" | "activate">,
  ) => void;
  closeDevTools?: () => void;
  close?: () => void;
  isFocused?: () => boolean;
  isDestroyed?: () => boolean;
  isCrashed?: () => boolean;
  isCurrentlyAudible?: () => boolean;
  getOSProcessId?: () => number;
  setBackgroundThrottling?: (allowed: boolean) => void;
  forcefullyCrashRenderer?: () => void;
  session?: Pick<
    Session,
    | "on"
    | "removeListener"
    | "downloadURL"
    | "setPermissionCheckHandler"
    | "setPermissionRequestHandler"
    | "setProxy"
    | "setCertificateVerifyProc"
    | "webRequest"
  >;
};

export type BrowserElectronSession = NonNullable<BrowserWebContents["session"]>;

export type BrowserViewLike = View & {
  webContents: BrowserWebContents;
  setBounds: (bounds: BrowserRect) => void;
  setBorderRadius?: (radius: number) => void;
  setVisible?: (visible: boolean) => void;
};

type BrowserWindowLike = {
  contentView: {
    addChildView: (view: BrowserViewLike) => void;
    removeChildView?: (view: BrowserViewLike) => void;
  };
  getContentBounds: () => BrowserRect;
  webContents?: WebContents;
  isDestroyed?: () => boolean;
  isFocused?: () => boolean;
  isMinimized?: () => boolean;
  isVisible?: () => boolean;
};

type ShellLike = {
  openExternal: (url: string) => Promise<void>;
};

type WebContentsViewConstructor = new (options: {
  webPreferences: Electron.WebPreferences;
  webContents?: WebContents;
}) => BrowserViewLike;

// Electron supplies this native guest to createWindow, but omits the internal
// field from BrowserWindowConstructorOptions in its public type declaration.
export type BrowserPopupOptions = Electron.BrowserWindowConstructorOptions & {
  webContents?: WebContents;
};

export type BrowserViewHostOptions = {
  // True in a packaged app. Turns off the dev-only wiring guard in context.ts;
  // omitted (tests, dev) keeps it on.
  isPackaged?: boolean;
  // Whether a tab's URL is Tallpine's own agent-browser viewer (a connected remote
  // host's): only those may report the remote page a tab shows.
  isAgentBrowserUrl?: (url: string) => boolean;
  mainWindow: BrowserWindowLike;
  shellWebContents?: WebContents;
  ipcMain: Pick<IpcMain, "handle" | "on" | "removeHandler" | "off">;
  shell: ShellLike;
  WebContentsView: WebContentsViewConstructor;
  annotatePreloadPath: string;
  rendererOrigin: string;
  // Platform flag for application shortcuts forwarded from each preview view
  // to the shell. Defaults to non-mac when omitted (tests).
  isMac?: boolean;
  getKeybindingOverrides?: () => KeybindingOverrides;
  isKeybindingRecording?: () => boolean;
  agentBrowserRuntime?: AgentBrowserRuntime;
  isCloseShellTerminalShortcutEnabled?: () => boolean;
  browserProfileStore?: BrowserProfileStore;
  browserHistoryStore?: BrowserHistoryStore;
  browserDownloadManager?: BrowserDownloadManager;
  // Asks the person whether the agent may use a named profile's logins.
  confirmAgentLogins?: AgentLoginsConfirm;
  clearBrowserProfileData?: (partition: string) => Promise<void>;
  // Electron 44 removed clipboard.writeImage; main.ts adapts the async
  // ClipboardItem API to this narrow image-only writer.
  clipboard?: { writeImage(image: Electron.NativeImage): void | Promise<void> };
  // Called after a page view is appended to the window so the composition can
  // keep a raised shell (open dialog/notice) above the new view.
  onNativeViewAdded?: () => void;
  onTabCloseFailure?: (failure: BrowserCloseFailure) => void;
  // Test hook: how long a tab's initial about:blank load may take before
  // awaiting callers get a typed failure instead of hanging forever.
  entryReadyTimeoutMs?: number;
  // Hidden-tab throttling and discarding; tests shorten these.
  tabLifecycleLimits?: TabLifecycleLimits;
  tabLifecycleSweepMs?: number;
};

export type BrowserViewHost = {
  dispose: () => Promise<void>;
  destroy: (viewId: string) => void;
  destroyAll: () => void;
  execute: (
    sessionId: string,
    action: string,
    args?: Record<string, unknown>,
    signal?: AbortSignal,
  ) => Promise<unknown>;
  // webContents of the most recently focused browser panel (or null); the titlebar menu targets it for Edit/Reload/Zoom/DevTools.
  getLastFocusedPanelContents: () => WebContents | null;
  // The app menu's Reload for an agent tab's viewer: reloads the agent's page,
  // as the tab's toolbar Reload does. False for any other contents, which the
  // caller reloads itself.
  reloadAgentTab: (contents: Pick<WebContents, "id">) => boolean;
  /** Toggle Chromium DevTools for the last focused Tallpine browser panel. */
  toggleDevToolsForLastFocused: () => Promise<BrowserDevToolsState | null>;
  // Drop the remembered panel; call when the shell gains focus for a real reason so a stale panel stops absorbing menu actions.
  forgetLastFocusedPanel: () => void;
  isRendererOwned: (event: IpcMainInvokeEvent, viewId: string) => boolean;
  getProfileState: (viewId: string) => BrowserProfileViewState | null;
  refreshProfileState: (profileId: BrowserProfileId) => void;
  getProfileSwitchInfo: (
    viewId: string,
  ) => { hasNavigated: boolean; agentActive: boolean } | null;
  switchProfile: (
    viewId: string,
    profileId: BrowserProfileId | null,
  ) => Promise<BrowserProfileViewState>;
  getProfileUsage: (profileId: BrowserProfileId) => BrowserProfileUsage[];
  detachProfile: (profileId: BrowserProfileId) => Promise<void>;
  isProfileLive: (profileId: BrowserProfileId) => boolean;
  clearProfileData: (profileId: BrowserProfileId) => Promise<void>;
  // Whether browser-owned UI was the most recently used application surface.
  isLastUsedBrowser: () => boolean;
  // Refresh the live page after raising the transparent shell for an overlay.
  // Hide immediately, then restore visibility with the bounds nudge on the next
  // tick — a same-turn hide/show can coalesce into a no-op on macOS and leave
  // the page blank for the whole overlay lifetime.
  refreshLastFocusedPanelSurface: () => void;
	startLiveStream: (sessionId: string, streamId: number, sink: BrowserLiveSink) => Promise<BrowserLiveState>;
	stopLiveStream: (sessionId: string, streamId: number) => Promise<void>;
	stopAllLiveStreams: () => Promise<void>;
	handleRemoteInput: (sessionId: string, input: BrowserRemoteInput) => Promise<void>;
	handleRemoteNavigation: (sessionId: string, input: BrowserRemoteNavigation) => Promise<void>;
	handleRemoteTab: (sessionId: string, input: BrowserRemoteTabAction) => Promise<void>;
	openLivePreview: (sessionId: string, url: string) => Promise<void>;
};

export type TabNavCommand = "back" | "forward" | "reload" | "stop";

// The page a tab shows, as its address bar, its tab strip entry and
// Back/Forward show it.
type TabPage = Pick<BrowserNavState, "url" | "title" | "canGoBack" | "canGoForward" | "isLoading">;

// How the panel reads and drives one tab (tabDriver).
export type TabDriver = {
  page: () => TabPage;
  // Back/Forward/Reload/Stop, from the toolbar, a shortcut or the app menu.
  nav: (command: TabNavCommand) => void;
  // An address for the tab, already checked (navigateEntry). Load options (a
  // popup's referrer and POST body) apply to the person's own tabs.
  navigate: (href: string, loadOptions?: Electron.LoadURLOptions) => Promise<BrowserNavState>;
  // Whether the tab is the one the panel shows.
  show: (shown: boolean) => void;
  close: (signal?: AbortSignal) => Promise<BrowserTabsState>;
};

export type BrowserEntry = {
  sessionId: string;
  tabId: string;
  view: BrowserViewLike;
  // This tab's driver (tabDriver), picked on each call: a tab is bound to an
  // agent page after it is made.
  driver: () => TabDriver;
  ready: Promise<void>;
  state: BrowserNavState;
  // Set on a tab that shows the agent's own browser: the agent browser tab it
  // shows, for life ("" until its viewer first reports).
  agentTab?: string;
  // Created for the agent: lives in the throwaway agent partition unless the
  // person granted their logins (agent-partition.ts).
  agentDriven?: boolean;
  // The partition this tab's view was created in.
  partition?: string;
  // The agent browser page this tab shows, as its viewer last reported it.
  remote?: RemoteTabReport;
  // An agent tab not shown yet: the viewer it loads when first shown
  // (openAgentTab). Until then it has no viewer, so no socket or CDP session
  // on the agent's host.
  viewer?: string;
  // Its page as the viewers' tab lists show it: what the tab shows until its
  // own viewer reports.
  listed?: { title: string; url: string };
  // A person is closing this agent tab (closeAgentTab): their close finishes
  // it, not its page's own report of closing meanwhile.
  closing?: boolean;
  // The newest renderer navigation that is still loading. A profile switch stops
  // in-flight loads and reloads each tab in the new profile. Until the server's
  // first response arrives, getURL() still returns the previous page, so the
  // switch reloads this URL instead of dropping the page the user asked for.
  // Compared by identity: an older, aborted load of the same URL must not clear
  // a newer one.
  pendingNavigation?: { url: string };
  findState: BrowserFindState & { requestId?: number };
  annotationEnabled: boolean;
  annotationSessions: Map<
    string,
    { session: BrowserAnnotationSession; token: string }
  >;
  annotationTheme?: BrowserAnnotationModeInput["theme"];
  documentGeneration: number;
  navigationGeneration: number;
  blockedNavigationGeneration: number;
  networkCapture?: BrowserNetworkCapture;
  favicon?: string;
  // URL of the favicon currently applied to `favicon` (fetch succeeded).
  faviconSourceUrl?: string;
  // URL of a favicon fetch currently in flight, so a duplicate event for the
  // same URL doesn't start a second fetch while one is already pending.
  faviconPendingUrl?: string;
  // Origin `favicon` was captured for, so a same-tab navigation to a new
  // origin can drop the (now-stale) favicon immediately instead of leaving
  // the previous site's icon showing until the new one finishes loading.
  faviconOrigin?: string;
  // Lifecycle (browser-tab-lifecycle.ts): when the tab was last in use, and
  // whether it is throttled or discarded now. `restoring` is set while a
  // discarded tab reloads, so the reload is not recorded as a new visit.
  lastInUseAt: number;
  throttled?: boolean;
  discarded?: boolean;
  restoring?: boolean;
};

export type BrowserSessionEntry = {
  sessionId: string;
  viewId: string;
  profileId: BrowserProfileId | null;
  profilePartition: string;
  // Throwaway partition for agent-created tabs; created on first use.
  agentPartition?: string;
  // Who the native command now running is for; see agentBrowserTargets.
  nativeCommandOrigin?: "agent";
  // The named profile whose logins the person let the agent use in this session.
  agentLoginsGrantedProfileId?: string;
  tabs: Map<string, BrowserEntry>;
  activeTabId: string;
  activationRevision: number;
  nextTabNumber: number;
  // Automatic mobile preview selection only owns the initial unused tab.
  // Keep this across reconnects so user-created blank tabs stay blank.
  livePreviewPending: boolean;
  bounds: BrowserRect;
  rendererBounds: BrowserRect;
  // Size the desktop panel last showed this session at. A remotely viewed
  // session keeps laying out at that size while its native view is parked.
  lastVisibleSize?: { width: number; height: number };
  zoomFactor: number;
  visible: boolean;
  layoutRevision: number;
  networkTabId?: string;
  agentBrowserCommands: number;
  browserOperations: number;
  // Woken when no agent command or renderer operation is in flight.
  idleWaiters: Array<() => void>;
  profileSwitching: boolean;
  // Set once a switch has drained in-flight work and is replacing the tabs.
  // Until then, work admitted before the switch (an agent tab-new or tab-close)
  // may still open and close tabs; profileSwitching refuses only new work.
  profileReplacing: boolean;
  profileSwitchTargetId: BrowserProfileId | null;
  nativeActiveTabId?: string;
  // Agent browser pages whose tabs closed here: they are closing, so the
  // agent tab sync must not bring them back meanwhile. A page a person closed
  // (closePage) stays closed for as long as reports still list it.
  agentTabsClosed?: Map<string, { at: number; closePage: boolean }>;
  // Agent tabs are numbered apart (a1, a2, ...) and kept from agent-browser:
  // it numbers the tabs it knows t1, t2, ... and Tallpine relies on those matching.
  agentTabNumber?: number;
  snapshotDeltaBaseline?: {
    tabId: string;
    interactive: boolean;
    revision: number;
  };
  nativeOperationQueue: Promise<void>;
  devtoolsPlacement: BrowserDevToolsPlacement;
	live?: {
		streamId: number;
		sink: BrowserLiveSink;
		screencast?: BrowserScreencast;
		tabId?: string;
		lastStateKey?: string;
	};
  // Bounded browser diagnostics exposed only through an explicit errors query.
  signals: {
    entries: BrowserSignalEntry[];
  };
  devtools?: {
    contents: BrowserWebContents;
    placement: BrowserDevToolsPlacement;
    nativeCloseForReopen?: boolean;
    targetTabId: string;
    desiredTabId: string;
    retargetGeneration: number;
    retargetQueue: Promise<void>;
    revealRequested: boolean;
  };
};

export type BrowserLogEntry = {
  level: string;
  message: string;
  source?: string;
  line?: number;
  timestamp: string;
};

// Text only — never a request/response body, matching the existing on-demand
// network capture's privacy stance (see MAX_NETWORK_REQUESTS below).
export type BrowserSignalEntry = {
  kind: "console-error" | "network-failure";
  message: string;
  timestamp: string;
};

export type BrowserNetworkRequest = {
  id: string;
  method: string;
  url: string;
  resourceType?: string;
  startedAt: string;
  status?: number;
  statusText?: string;
  mimeType?: string;
  durationMs?: number;
  failed?: boolean;
  canceled?: boolean;
  errorText?: string;
  fromCache?: boolean;
  fromServiceWorker?: boolean;
  redirectedTo?: string;
  requestHeaders?: Record<string, string>;
  responseHeaders?: Record<string, string>;
};

export type InternalBrowserNetworkRequest = BrowserNetworkRequest & {
  protocolRequestId: string;
  startedMonotonic?: number;
};

export type BrowserNetworkCapture = {
  active: boolean;
  tabId: string;
  startedAt: string;
  expiresAt: string;
  stoppedAt?: string;
  stopReason?: string;
  maxEntries: number;
  nextSequence: number;
  requests: InternalBrowserNetworkRequest[];
  byRequestId: Map<string, InternalBrowserNetworkRequest>;
  timer?: ReturnType<typeof setTimeout>;
};
