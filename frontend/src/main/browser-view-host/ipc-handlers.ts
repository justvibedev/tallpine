import type { IpcMainEvent, IpcMainInvokeEvent, WebContents } from "electron";
import type { BrowserAnnotationActionInput, BrowserAnnotationCompleteInput, BrowserAnnotationDiscardInput, BrowserAnnotationModeInput, BrowserAnnotationPageCancelPayload, BrowserAnnotationPageSubmitPayload, BrowserAnnotationSession } from "../../shared/browser-annotations";
import type { BrowserDownloadActionInput } from "../../shared/browser-downloads";
import { browserError } from "./args";
import { listTabs, pushNavState, viewerRuns } from "./nav-events";
import { isAllowedBrowserURL, normalizeBrowserURL } from "./url";
import { activeEntry, emptyDevToolsState, emptyNavState, emptyTabsState, isBlankBrowserEntry } from "./entry-state";
import type { BrowserBoundsInput, BrowserDevToolsInput, BrowserFindInput, BrowserFindStopInput, BrowserHistoryFaviconInput, BrowserHistorySuggestInput, BrowserNavigateInput, BrowserOpenTabInput, BrowserTabInput } from "./types";
import { fetchFaviconFromSession, originOf } from "./favicon";
import type { HostContext } from "./context";

export function createIpcHandlers(ctx: HostContext) {
  const { entries, forgetBrowserShortcutTarget, historyFaviconCache, hostState, ipcDisposers, options, shellWebContents } = ctx;
  const { activateTab, assertProfileStable, captureAnnotation, clear, closeUserTab, completeAnnotation, destroy, devtoolsAction, discardAnnotationFromPage, discardAnnotationFromShell, ensureNativeActiveTab, ensureSessionReady, focusLocation, forwardAnnotationAction, forwardAnnotationCancel, forwardAnnotationSubmit, invokeNav, isRendererOwned, navigate, navigateEntry, openAgentTab, openUserTab, publicFindState, pushDevToolsState, pushFindState, pushProfileState, queueNativeOperation, resetFindState, setAnnotationMode, setBounds, updateAnnotationState, withBrowserOperation } = ctx.fns;

  const handle = <Args extends unknown[], Result>(
    channel: string,
    fn: (event: IpcMainInvokeEvent, ...args: Args) => Result,
  ): void => {
    options.ipcMain.handle(channel, fn);
    ipcDisposers.push(() => options.ipcMain.removeHandler(channel));
  };
  const on = <Args extends unknown[]>(
    channel: string,
    fn: (event: IpcMainEvent, ...args: Args) => void,
  ): void => {
    options.ipcMain.on(channel, fn);
    ipcDisposers.push(() => options.ipcMain.off(channel, fn));
  };

  handle("browser:ensure", async (event, sessionId: string, createInitialTab: boolean = true) => {
    const session = await ensureSessionReady(
      sessionId,
      event.sender.id,
      () => event.sender.isDestroyed?.() ?? false,
      createInitialTab,
    );
    pushDevToolsState(session);
    pushProfileState(session);
    return session.tabs.size ? pushNavState(options, activeEntry(session)) : emptyNavState(session.viewId);
  });
  on("browser:setBounds", (event, input: BrowserBoundsInput) => {
    if (!input || !isRendererOwned(event, input.viewId)) return;
    const applied = setBounds(input, event.sender.getZoomFactor());
    if (applied) event.sender.send("browser:boundsApplied", applied);
  });
  handle("browser:navigate", (event, input: BrowserNavigateInput) =>
    isRendererOwned(event, input.viewId)
      ? navigate(input)
      : emptyNavState(input.viewId),
  );
  handle(
    "browser:history:suggest",
    (event, input: BrowserHistorySuggestInput) => {
      if (
        !input ||
        typeof input.viewId !== "string" ||
        typeof input.query !== "string" ||
        input.query.length > 512 ||
        !isRendererOwned(event, input.viewId)
      ) {
        return [];
      }
      const profileId = entries.get(input.viewId)?.profileId;
      if (!profileId || !options.browserHistoryStore) return [];
      return options.browserHistoryStore.suggest(profileId, input.query);
    },
  );
  handle(
    "browser:history:favicon",
    (event, input: BrowserHistoryFaviconInput) => {
      if (
        !input ||
        typeof input.viewId !== "string" ||
        typeof input.url !== "string" ||
        input.url.length > 4_096 ||
        !isRendererOwned(event, input.viewId)
      ) {
        return undefined;
      }
      const origin = originOf(input.url);
      const entry = entries.get(input.viewId);
      if (!origin || !entry) return undefined;
      const tabSession = (
        activeEntry(entry).view.webContents as unknown as WebContents
      ).session;
      let cache = historyFaviconCache.get(tabSession);
      if (!cache) {
        cache = new Map();
        historyFaviconCache.set(tabSession, cache);
      }
      const cached = cache.get(origin);
      if (cached) return cached;
      const pending = fetchFaviconFromSession(
        tabSession,
        `${origin}/favicon.ico`,
      ).then((favicon) => {
        if (!favicon) cache?.delete(origin);
        return favicon;
      });
      cache.set(origin, pending);
      return pending;
    },
  );
  handle("browser:clear", (event, viewId: string) =>
    isRendererOwned(event, viewId) ? clear(viewId) : emptyNavState(viewId),
  );
  handle("browser:goBack", (event, viewId: string) =>
    isRendererOwned(event, viewId)
      ? invokeNav(viewId, "back", true)
      : emptyNavState(viewId),
  );
  handle("browser:goForward", (event, viewId: string) =>
    isRendererOwned(event, viewId)
      ? invokeNav(viewId, "forward", true)
      : emptyNavState(viewId),
  );
  handle("browser:reload", (event, viewId: string) =>
    isRendererOwned(event, viewId)
      ? invokeNav(viewId, "reload", false, true)
      : emptyNavState(viewId),
  );
  handle("browser:stop", (event, viewId: string) =>
    isRendererOwned(event, viewId)
      ? invokeNav(viewId, "stop", true)
      : emptyNavState(viewId),
  );
  handle("browser:find", (event, input: BrowserFindInput) => {
    const session =
      typeof input?.viewId === "string" ? entries.get(input.viewId) : undefined;
    if (
      !session ||
      !isRendererOwned(event, input.viewId) ||
      typeof input.query !== "string" ||
      input.query.length > 10_000 ||
      typeof input.forward !== "boolean" ||
      typeof input.newSession !== "boolean"
    ) {
      throw browserError("INVALID_ARGUMENT", "Invalid find-in-page request");
    }
    const entry = activeEntry(session);
    if (!input.query) {
      entry.view.webContents.stopFindInPage("clearSelection");
      return resetFindState(entry);
    }
    const requestId = entry.view.webContents.findInPage(input.query, {
      forward: input.forward,
      findNext: input.newSession,
      matchCase: false,
    });
    if (input.newSession && input.query.length < 4) {
      // Chromium delays new find sessions shorter than four characters by
      // 400 ms. A follow-up request for the same query flushes that pending
      // search immediately without advancing the match. Keep tracking the
      // initial request id because that is the request Chromium emits.
      entry.view.webContents.findInPage(input.query, {
        forward: input.forward,
        findNext: false,
        matchCase: false,
      });
    }
    entry.findState = {
      viewId: session.viewId,
      tabId: entry.tabId,
      query: input.query,
      activeMatchOrdinal: input.newSession
        ? 0
        : entry.findState.activeMatchOrdinal,
      matches: input.newSession ? 0 : entry.findState.matches,
      finalUpdate: false,
      requestId,
    };
    return pushFindState(entry);
  });
  handle("browser:find:stop", (event, input: BrowserFindStopInput) => {
    const session =
      typeof input?.viewId === "string" ? entries.get(input.viewId) : undefined;
    if (!session || !isRendererOwned(event, input.viewId)) {
      throw browserError(
        "INVALID_ARGUMENT",
        "Invalid find-in-page stop request",
      );
    }
    const entry = activeEntry(session);
    entry.view.webContents.stopFindInPage("clearSelection");
    const state = resetFindState(entry);
    if (input.focusPage) {
      if (isBlankBrowserEntry(entry)) focusLocation(session);
      else entry.view.webContents.focus();
    }
    return state;
  });
  handle("browser:captureScreenshot", async (event, viewId: string) => {
    const session =
      typeof viewId === "string" ? entries.get(viewId) : undefined;
    if (!session || !isRendererOwned(event, viewId)) {
      throw browserError(
        "BROWSER_TARGET_UNAVAILABLE",
        "Browser tab is unavailable",
      );
    }
    if (!options.clipboard) {
      throw browserError(
        "SCREENSHOT_UNAVAILABLE",
        "Screenshot clipboard access is unavailable",
      );
    }
    assertProfileStable(session);
    const entry = activeEntry(session);
    await entry.ready;
    if (isBlankBrowserEntry(entry)) {
      throw browserError(
        "SCREENSHOT_UNAVAILABLE",
        "Open a page before taking a screenshot",
      );
    }
    const image = await entry.view.webContents.capturePage();
    if (image.isEmpty()) {
      throw browserError(
        "SCREENSHOT_UNAVAILABLE",
        "The browser page could not be captured",
      );
    }
    await options.clipboard.writeImage(image);
  });
  handle("browser:downloads:list", (event) => {
    if (event.sender.id !== shellWebContents.id) return { downloads: [] };
    return options.browserDownloadManager?.list() ?? { downloads: [] };
  });
  handle(
    "browser:downloads:action",
    (event, input: BrowserDownloadActionInput) => {
      if (
        event.sender.id !== shellWebContents.id ||
        !options.browserDownloadManager
      ) {
        throw browserError(
          "BROWSER_TARGET_UNAVAILABLE",
          "Browser downloads are unavailable",
        );
      }
      return options.browserDownloadManager.action(input);
    },
  );
  handle("browser:downloads:clear", (event) => {
    if (
      event.sender.id !== shellWebContents.id ||
      !options.browserDownloadManager
    ) {
      throw browserError(
        "BROWSER_TARGET_UNAVAILABLE",
        "Browser downloads are unavailable",
      );
    }
    return options.browserDownloadManager.clear();
  });
  handle("browser:getTabs", (event, viewId: string) => {
    const session = entries.get(viewId);
    return session && isRendererOwned(event, viewId)
      ? listTabs(session)
      : emptyTabsState(viewId);
  });
  handle("browser:find:get", (event, viewId: string) => {
    const session = entries.get(viewId);
    return session && session.tabs.size > 0 && isRendererOwned(event, viewId)
      ? publicFindState(activeEntry(session))
      : {
          viewId,
          tabId: "",
          query: "",
          activeMatchOrdinal: 0,
          matches: 0,
          finalUpdate: true,
        };
  });
  handle("browser:selectTab", (event, input: BrowserTabInput) => {
    const session = entries.get(input.viewId);
    if (!session || !isRendererOwned(event, input.viewId))
      return emptyTabsState(input.viewId);
    assertProfileStable(session);
    return queueNativeOperation(session, async () => {
      activateTab(session, input.tabId);
      if (session.tabs.get(input.tabId)?.agentTab === undefined)
        await ensureNativeActiveTab(session);
      return listTabs(session);
    });
  });
  handle("browser:closeTab", (event, input: BrowserTabInput) => {
    const session = entries.get(input.viewId);
    if (!session || !isRendererOwned(event, input.viewId))
      return emptyTabsState(input.viewId);
    // A renderer can still show a tab removed by another close or by its page.
    return closeUserTab(session, input.tabId);
  });
  on("browser:panelUsed", (event, viewId: string) => {
    if (isRendererOwned(event, viewId) && entries.has(viewId))
      hostState.lastUsedViewId = viewId;
  });
  on("browser:panelBlur", (event, viewId: string) => {
    if (isRendererOwned(event, viewId)) forgetBrowserShortcutTarget(viewId);
  });
  handle("browser:devtools", (event, input: BrowserDevToolsInput) => {
    if (
      !input ||
      typeof input.viewId !== "string" ||
      !isRendererOwned(event, input.viewId)
    ) {
      return emptyDevToolsState(input?.viewId ?? "");
    }
    const session = entries.get(input.viewId);
    if (!session) return emptyDevToolsState(input.viewId);
    assertProfileStable(session);
    if (!["open", "close", "setPlacement"].includes(input.operation)) {
      throw browserError(
        "INVALID_ARGUMENT",
        "Unsupported browser DevTools operation",
      );
    }
    return withBrowserOperation(session, () =>
      devtoolsAction(session, input.operation, input.placement),
    );
  });
  handle("browser:openTab", async (event, input: BrowserOpenTabInput) => {
    const session = entries.get(input.viewId);
    if (!session || !isRendererOwned(event, input.viewId))
      return emptyTabsState(input.viewId);
    assertProfileStable(session);
    let url = input.url;
    if (url) {
      const normalized = normalizeBrowserURL(url);
      if (!isAllowedBrowserURL(normalized.href, options.rendererOrigin)) {
        throw browserError("NAVIGATION_FAILED", "Unsupported browser URL");
      }
      url = normalized.href;
    }
    // The agent's browser opens once, behind the page in use, then shows as
    // one tab per agent page (syncAgentTabs). It never takes over a tab:
    // agent-browser numbers the tabs it sees, and a tab gone from its sight
    // would shift its numbers off Tallpine's. A reconnected host may serve it on a
    // new origin: its tabs move there. On the same origin a viewer reconnects
    // by itself, unless it failed to load while the host was away (an error
    // page, without the viewer's script): that one loads again.
    if (url && options.isAgentBrowserUrl?.(url)) {
      const agentTabs = [...session.tabs.values()].filter((tab) => tab.agentTab !== undefined);
      if (!agentTabs.length) void openAgentTab(session, url, "", false).catch(() => undefined);
      for (const tab of agentTabs) {
        const moved = new URL(url);
        if (tab.agentTab) moved.searchParams.set("tab", tab.agentTab);
        // One not shown yet loads its viewer there when first shown.
        if (tab.viewer) {
          tab.viewer = moved.href;
          continue;
        }
        const sameOrigin = new URL(tab.view.webContents.getURL() || url).origin === moved.origin;
        void (async () => {
          if (sameOrigin && (await viewerRuns(tab))) return;
          await withBrowserOperation(session, () => navigateEntry(tab, moved.href));
        })().catch(() => undefined);
      }
      return listTabs(session);
    }
    return openUserTab(session, url);
  });
  handle(
    "browser:annotation:setMode",
    (event, input: BrowserAnnotationModeInput) =>
      setAnnotationMode(event, input),
  );
  on("browser:destroy", (event, viewId: string) => {
    if (isRendererOwned(event, viewId)) destroy(viewId);
  });
  handle(
    "browser:annotation:submit",
    (event, payload: BrowserAnnotationPageSubmitPayload) =>
      forwardAnnotationSubmit(event, payload),
  );
  handle("browser:annotation:capture", (event) => captureAnnotation(event));
  handle(
    "browser:annotation:complete",
    (event, input: BrowserAnnotationCompleteInput) =>
      completeAnnotation(event, input),
  );
  handle(
    "browser:annotation:discard",
    (event, input: BrowserAnnotationDiscardInput) =>
      discardAnnotationFromShell(event, input),
  );
  handle(
    "browser:annotation:action",
    (event, input: BrowserAnnotationActionInput) =>
      forwardAnnotationAction(event, input),
  );
  on(
    "browser:annotation:cancel",
    (event, payload: BrowserAnnotationPageCancelPayload) =>
      forwardAnnotationCancel(event, payload),
  );
  on(
    "browser:annotation:state",
    (event, annotationSession: BrowserAnnotationSession) =>
      updateAnnotationState(event, annotationSession),
  );
  on("browser:annotation:discard", (event) => discardAnnotationFromPage(event));

  return {
  };
}
