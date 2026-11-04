import type { WebContents } from "electron";
import { attachAppShortcuts } from "../app-shortcuts";
import { trustPreviewCertificate } from "../preview-certificate";
import { REMOTE_TAB_REPORT, parseRemoteTabReport } from "../../shared/remote-tab";
import { BROWSER_NO_DRAG_CSS, browserScrollbarCSS } from "./bounds";
import { loadEntryReady } from "./load-entry";
import { hardenWebContents, pushNavState, pushTabsState, wireAutomationEvents, wireNavEvents } from "./nav-events";
import { sanitizeBrowserURL, sanitizeURLsInText } from "./url";
import { emptyNavState, isBlankBrowserEntry } from "./entry-state";
import type { BrowserEntry, BrowserFindState, BrowserNavState, BrowserPopupOptions, BrowserRect, BrowserSessionEntry, BrowserViewLike } from "./types";
import { wireFaviconEvents } from "./favicon";
import { BROWSER_VIEW_BORDER_RADIUS, OFFSCREEN_BOUNDS } from "./constants";
import { canWriteBrowserClipboard, shouldHandleAppShortcutInBrowserContext } from "./shortcuts";
import type { HostContext } from "./context";
import { proxyAgentSession } from "./agent-proxy";

export type TabDeps = {
  applyBrowserViewBounds: (view: BrowserViewLike, bounds: BrowserRect, visible: boolean) => void;
  agentPartitionFor: (session: BrowserSessionEntry) => string;
  pushFindState: (entry: BrowserEntry) => BrowserFindState;
  resetFindState: (entry: BrowserEntry, push: boolean) => void;
  pushDevToolsState: (session: BrowserSessionEntry) => void;
  destroyDevTools: (session: BrowserSessionEntry) => void;
};

export type PrepareArgs = {
  ctx: HostContext;
  deps: TabDeps;
  session: BrowserSessionEntry;
  agentDriven: boolean;
  popupOptions: BrowserPopupOptions | undefined;
};

export type EntryArgs = {
  ctx: HostContext;
  session: BrowserSessionEntry;
  tabId: string;
  view: BrowserViewLike;
  webPreferences: Electron.WebPreferences;
  agentDriven: boolean;
};

// Everything a wiring step needs about the tab being created.
export type TabWiring = {
  ctx: HostContext;
  deps: TabDeps;
  session: BrowserSessionEntry;
  entry: BrowserEntry;
  view: BrowserViewLike;
  tabId: string;
  webPreferences: Electron.WebPreferences;
  isCurrentEntry: () => boolean;
  // Set once the tab's entry exists: the agent-tab reports.
  remote: { report?: (message: string) => void };
  createTab: (
    session: BrowserSessionEntry,
    activate: boolean,
    syncNativeOnActivate?: boolean,
    preferredTabId?: string,
    popupOptions?: BrowserPopupOptions,
    agentDriven?: boolean,
  ) => BrowserEntry;
  agentProxyReady: Promise<void>;
  popupOptions: BrowserPopupOptions | undefined;
  agentDriven: boolean;
};


export function prepareView(w: PrepareArgs): { view: BrowserViewLike; webPreferences: Electron.WebPreferences; agentProxyReady: Promise<void> } {
  const { session, popupOptions, agentDriven } = w;
  const { options } = w.ctx;
  const { applyBrowserViewBounds, agentPartitionFor } = w.deps;
  const webPreferences: Electron.WebPreferences = {
    backgroundThrottling: false,
    contextIsolation: true,
    nodeIntegration: false,
    partition: agentDriven ? agentPartitionFor(session) : session.profilePartition,
    // Chromium's built-in PDF viewer is plugin-backed in Electron. Keep it
    // enabled for Tallpine's browser surface so HTTP(S) PDFs render in-panel.
    plugins: true,
    preload: options.annotatePreloadPath,
    sandbox: true,
  };
  const view = new options.WebContentsView({
    webPreferences,
    ...(popupOptions?.webContents ? { webContents: popupOptions.webContents } : {}),
  });
  applyBrowserViewBounds(view, OFFSCREEN_BOUNDS, false);
  options.mainWindow.contentView.addChildView(view);
  options.onNativeViewAdded?.();
  view.setBorderRadius?.(BROWSER_VIEW_BORDER_RADIUS);
  // The agent's throwaway partition reaches the network only through the
  // resolver proxy; a profile the person granted stays as it was.
  const proxiedAgentTab = agentDriven && webPreferences.partition === session.agentPartition;
  const agentSession = view.webContents.session;
  const agentProxyReady = proxiedAgentTab && agentSession ? proxyAgentSession(agentSession) : Promise.resolve();
  // SOCKS carries no UDP: without this WebRTC would probe the LAN directly.
  // The policy is per WebContents, so adopted popups need it too.
  if (proxiedAgentTab) view.webContents.setWebRTCIPHandlingPolicy("disable_non_proxied_udp");
  // Main-frame sites use this permission for actions such as "Copy link".
  // Keep embedded frames, clipboard reads, and every other permission denied.
  view.webContents.session?.setPermissionCheckHandler?.(
    (contents, permission, _origin, details) =>
      canWriteBrowserClipboard(
        contents,
        permission,
        details.requestingUrl,
        details.isMainFrame,
      ),
  );
  view.webContents.session?.setPermissionRequestHandler?.(
    (contents, permission, callback, details) =>
      callback(
        canWriteBrowserClipboard(
          contents,
          permission,
          details.requestingUrl,
          details.isMainFrame,
        ),
      ),
  );
  // Remote app previews are HTTPS on loopback with Tallpine's own certificate,
  // so Chromium speaks HTTP/2 to them (preview-certificate.ts).
  const viewSession = view.webContents.session;
  if (viewSession?.setCertificateVerifyProc) trustPreviewCertificate(viewSession);
  options.browserDownloadManager?.attach(view.webContents.session);
  return { view, webPreferences, agentProxyReady };
}

export function wireScrollbarStyle(w: { view: BrowserViewLike }): void {
  const { view } = w;
  let scrollbarStyleKey: string | undefined;
  let scrollbarStyleUpdate = Promise.resolve();
  const applyScrollbarStyle = (): void => {
    scrollbarStyleUpdate = scrollbarStyleUpdate
      .then(async () => {
        const previousKey = scrollbarStyleKey;
        scrollbarStyleKey = await view.webContents.insertCSS(
          BROWSER_NO_DRAG_CSS +
            browserScrollbarCSS(view.webContents.getZoomFactor()),
          { cssOrigin: "user" },
        );
        if (previousKey)
          await view.webContents.removeInsertedCSS(previousKey);
      })
      .catch(() => undefined);
  };
  view.webContents.on("dom-ready", applyScrollbarStyle);
  view.webContents.on("zoom-changed", applyScrollbarStyle);
}

export function buildEntry(w: EntryArgs): BrowserEntry {
  const { session, view, tabId, webPreferences, agentDriven } = w;
  const { fns, tabsByWebContentsId } = w.ctx;
  const state: BrowserNavState = emptyNavState(session.viewId);
  const entry: BrowserEntry = {
    sessionId: session.sessionId,
    tabId,
    view,
    driver: () => fns.tabDriver(session, entry),
    ready: Promise.resolve(),
    state,
    lastInUseAt: Date.now(),
    ...(agentDriven ? { agentDriven: true } : {}),
    partition: webPreferences.partition,
    findState: {
      viewId: session.viewId,
      tabId,
      query: "",
      activeMatchOrdinal: 0,
      matches: 0,
      finalUpdate: true,
    },
    annotationEnabled: false,
    annotationSessions: new Map(),
    documentGeneration: 0,
    navigationGeneration: 0,
    blockedNavigationGeneration: 0,
  };
  session.tabs.set(tabId, entry);
  tabsByWebContentsId.set(view.webContents.id, entry);
  return entry;
}

export function wireTabLifecycle(w: TabWiring): void {
  const { session, entry, view, tabId, isCurrentEntry } = w;
  const { fns, tabsByWebContentsId } = w.ctx;
  const { pushFindState, pushDevToolsState } = w.deps;
  // A page can close its own tab: window.close() is allowed when the tab has
  // a single history entry, which is exactly what OAuth popup handlers do
  // after sign-in. Electron then destroys the WebContents and the view's
  // `webContents` becomes undefined, so the tab must leave the session before
  // the next layout or IPC call dereferences it.
  const closedContents = {
    id: view.webContents.id,
    session: view.webContents.session,
    contents: view.webContents,
  };
  view.webContents.on("destroyed", () => {
    if (!isCurrentEntry()) return;
    if (session.profileReplacing) {
      session.tabs.delete(tabId);
      tabsByWebContentsId.delete(closedContents.id);
      return;
    }
    fns.closeTab(session, tabId, undefined, true, closedContents);
  });
  view.webContents.on("found-in-page", (_event, result) => {
    if (
      !isCurrentEntry() ||
      entry.findState.requestId !== result.requestId
    )
      return;
    entry.findState = {
      ...entry.findState,
      activeMatchOrdinal: result.activeMatchOrdinal,
      matches: result.matches,
      finalUpdate: result.finalUpdate,
    };
    if (session.activeTabId === entry.tabId) pushFindState(entry);
  });
  // Native Chromium DevTools can be closed from its own window controls. Keep
  // the renderer's toggle state in sync with that user action. Programmatic
  // close/reopen cycles used for retargeting or placement changes are marked
  // and ignored so they do not look like a user close.
  view.webContents.on("devtools-closed", () => {
    const devtools = session.devtools;
    if (!devtools || devtools.contents !== view.webContents) return;
    if (devtools.nativeCloseForReopen) {
      devtools.nativeCloseForReopen = false;
      return;
    }
    session.devtools = undefined;
    pushDevToolsState(session);
  });
}

export function wireConsoleReports(w: TabWiring): void {
  const { session, view, remote } = w;
  const { fns } = w.ctx;
  // Always-on: level 0-2 are verbose/info/warning, 3 is error. This is a
  // native WebContents event, not a CDP domain, so it never competes with
  // agent-browser's own debugger attachment for this tab.
  // Set once the tab's entry exists (below): the agent-tab reports.
  view.webContents.on(
    "console-message",
    (_event, level, message, line, sourceId) => {
      if (message.startsWith(REMOTE_TAB_REPORT)) return remote.report?.(message);
      if (level < 3) return;
      const location = sourceId
        ? `${sanitizeBrowserURL(sourceId)}${typeof line === "number" ? `:${line}` : ""}`
        : "";
      fns.recordBrowserSignal(session, {
        kind: "console-error",
        message: location
          ? `${sanitizeURLsInText(message)} (${location})`
          : sanitizeURLsInText(message),
      });
    },
  );
}

export function wirePopups(w: TabWiring): void {
  const { session, entry, view, webPreferences, isCurrentEntry } = w;
  const { fns, options } = w.ctx;
  hardenWebContents(
    view.webContents,
    options,
    entry,
    () => isCurrentEntry() && !session.profileReplacing,
    webPreferences,
    (windowOptions, details) => {
      // Adopt Electron's exact guest, rather than creating an unrelated
      // WebContents. Chromium then preserves the WindowProxy and opener.
      const popup = w.createTab(
        session, false, false, undefined,
        windowOptions.webContents ? windowOptions : undefined,
        // A popup inherits its opener's partition: an agent tab's popup stays
        // out of the person's profile.
        entry.agentDriven === true,
      );
      // OpenURLFromTab (e.g. a target=_blank link) may have no native guest.
      // In that case the main process must perform the original navigation.
      if (!windowOptions.webContents && details.url !== "about:blank") {
        const postBody = details.postBody;
        void fns.navigateEntry(popup, details.url, {
          httpReferrer: details.referrer,
          ...(postBody
            ? {
                postData: postBody.data,
                extraHeaders: `content-type: ${postBody.contentType}${postBody.boundary ? `; boundary=${postBody.boundary}` : ""}`,
              }
            : {}),
        }).catch(() => undefined);
      }
      // The native guest is still being initialized inside createWindow.
      // Defer queries/layout until Electron has returned from that callback.
      setImmediate(() => {
        if (!isCurrentEntry() || session.tabs.get(popup.tabId) !== popup) return;
        fns.activateTab(session, popup.tabId, false);
        fns.queueNativeActiveTabSync(session);
        pushTabsState(options, session, { kind: "popup", tabId: popup.tabId });
      });
      return popup.view.webContents as WebContents;
    },
  );
}

export function wireNavigation(w: TabWiring): void {
  const { session, entry, view, isCurrentEntry } = w;
  const { fns, options } = w.ctx;
  const { resetFindState, destroyDevTools } = w.deps;
  wireNavEvents(
    view.webContents,
    options,
    entry,
    () => isCurrentEntry() && session.activeTabId === entry.tabId,
    () => {
      if (!isCurrentEntry()) return;
      if (!isBlankBrowserEntry(entry)) session.livePreviewPending = false;
      if (session.devtools && isBlankBrowserEntry(entry))
        destroyDevTools(session);
      fns.applySessionBounds(session, entry);
    },
    () => {
      if (isCurrentEntry()) pushTabsState(options, session);
    },
    (url, title, incrementVisit) => {
      const profileId = session.profileId;
      if (!isCurrentEntry() || !profileId || !options.browserHistoryStore)
        return;
      // The agent's pages are not the person's history.
      if (entry.agentTab !== undefined) return;
      // Reloading a discarded tab is not a new visit.
      if (entry.restoring) {
        if (!incrementVisit) entry.restoring = false;
        return;
      }
      void options.browserHistoryStore
        .record(profileId, url, title, incrementVisit)
        .catch(() => undefined);
    },
    () => resetFindState(entry, session.activeTabId === entry.tabId),
  );
  wireFaviconEvents(view.webContents, entry, () => {
    if (isCurrentEntry()) pushTabsState(options, session);
  });
}

export function wireRemoteTabReports(w: TabWiring): void {
  const { session, entry, view, isCurrentEntry, remote } = w;
  const { fns, options } = w.ctx;
  // A tab showing the agent's own browser: its viewer reports the remote
  // page, which the tab then shows and drives as its own. Only Tallpine's own
  // agent-browser viewer is believed (any page can write to its console),
  // and only in a tab opened as an agent tab: a person's tab on the
  // viewer's address (typed, or reopened from Recently Closed) stays theirs.
  remote.report = (message: string) => {
    if (!isCurrentEntry() || entry.agentTab === undefined || !options.isAgentBrowserUrl?.(view.webContents.getURL())) return;
    const report = parseRemoteTabReport(message);
    if (!report) return;
    // A follow is news once: each report repeats the viewer's last one.
    const followed = report.follow && report.follow !== entry.remote?.follow ? report.follow : "";
    entry.remote = report;
    entry.agentTab ||= report.tabId;
    entry.driver().show(session.activeTabId === entry.tabId);
    // The page's icon comes inline, as an image data URL (remote-tab.ts),
    // and is shown as is: never fetched from here, where an address the
    // remote page names would be fetched from the person's network. ""
    // is a page without one.
    entry.favicon = report.favicon || undefined;
    // A report lists every agent page: a tab shows its page from the list
    // until its own viewer reports.
    for (const tab of session.tabs.values()) {
      const page = tab.agentTab && report.tabs.find(({ id }) => id === tab.agentTab);
      if (page) tab.listed = page;
    }
    pushTabsState(options, session);
    if (session.activeTabId === entry.tabId) pushNavState(options, entry);
    // Off this tab's own event: the sync may close it.
    setTimeout(() => {
      try {
        if (isCurrentEntry()) fns.syncAgentTabs(session, entry, followed);
      } catch (error) {
        console.warn("Unable to sync the agent browser's tabs", error);
      }
    }, 0);
  };
}

export function wireInputEvents(w: TabWiring): void {
  const { session, entry, view, isCurrentEntry } = w;
  const { fns, hostState, options, shellWebContents } = w.ctx;
  wireAutomationEvents(view.webContents, entry);
  // The preview is a separate WebContentsView, so renderer-window keydown
  // listeners never see keys typed here. Forward application shortcuts to the
  // shell renderer so they still work with the panel focused.
  attachAppShortcuts(
    view.webContents,
    Boolean(options.isMac),
    shellWebContents,
    true,
    options.getKeybindingOverrides,
    options.isKeybindingRecording,
    (id, chord) =>
      shouldHandleAppShortcutInBrowserContext(
        id,
        chord,
        Boolean(options.isMac),
      ),
    (id) => {
      if (
        id !== "toggle-browser-devtools" ||
        !isCurrentEntry() ||
        session.profileSwitching
      )
        return;
      hostState.lastFocusedViewId = session.viewId;
      void fns.withBrowserOperation(session, () =>
        fns.devtoolsAction(session, "toggle"),
      ).catch(() => undefined);
    },
  );
  view.webContents.on("focus", () => {
    if (!isCurrentEntry()) return;
    hostState.lastFocusedViewId = session.viewId;
    hostState.lastUsedViewId = session.viewId;
    shellWebContents.send("browser:pageFocus", session.viewId);
  });
  fns.attachBrowserShortcuts(view.webContents, () => session, true);
}

export function wireEntryReady(w: TabWiring): void {
  const { entry, view, agentProxyReady, popupOptions } = w;
  const { entryReadyTimeoutMs } = w.ctx;
  // A newly-created WebContentsView reports about:blank before its renderer
  // has actually been initialized. CDP commands can hang until that initial
  // document has completed, so make readiness explicit for every tab.
  entry.ready = popupOptions
    ? new Promise<void>((resolve) => {
        const done = () => resolve();
        view.webContents.on("dom-ready", done);
        view.webContents.on("destroyed", done);
        // Bound the wait: an adopted popup that never fires dom-ready or
        // destroyed must not block native tab sync (and the session queue)
        // indefinitely. resolve is idempotent, so an earlier event still wins.
        setTimeout(done, entryReadyTimeoutMs);
      })
    : agentProxyReady.then(() =>
        loadEntryReady(view, entryReadyTimeoutMs, () => {
          entry.ready = Promise.resolve();
        }),
      );
  // Keep an unobserved tab initialization failure from becoming an unhandled
  // rejection; callers that need the target still await the original promise.
  void entry.ready.catch(() => undefined);
}

