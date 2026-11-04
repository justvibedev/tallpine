import { browserError, throwIfAborted } from "./args";
import { listTabs, pushNavState, pushTabsState } from "./nav-events";
import { activeEntry, emptyNavState, isBlankBrowserEntry, shellContents } from "./entry-state";
import type { BrowserElectronSession, BrowserEntry, BrowserSessionEntry, BrowserTabsState, BrowserWebContents } from "./types";
import { isAgentBrowserCommandFailure, isBrowserTargetMismatch, tabResult } from "./agent-results";
import { OFFSCREEN_BOUNDS } from "./constants";
import { disposeNetworkCapture } from "./network-capture";
import type { HostContext } from "./context";

export function createUserTabs(ctx: HostContext) {
  const { fns, forgetBrowserShortcutTarget, forgetNativeFocus, hostState, options, shellWebContents, tabsByWebContentsId } = ctx;
  const { applyBrowserViewBounds, destroyDevTools, ensureNativeActiveTab, openTab, publicFindState, pushDevToolsState, pushFindState, queueNativeOperation, unregisterBrowserSignalWatcher } = ctx.fns;

  function activateTab(
    session: BrowserSessionEntry,
    tabId: string,
    notify = true,
  ): BrowserEntry {
    const next = session.tabs.get(tabId);
    if (!next)
      throw browserError(
        "TAB_NOT_FOUND",
        `Browser tab ${tabId} does not exist`,
      );
    const previous = session.tabs.get(session.activeTabId);
    if (previous && previous !== next) {
      applyBrowserViewBounds(previous.view, OFFSCREEN_BOUNDS, false);
    }
    session.activeTabId = tabId;
    session.activationRevision += 1;
    if (previous && previous !== next) previous.driver().show(false);
    next.driver().show(true);
    if (session.devtools && isBlankBrowserEntry(next)) destroyDevTools(session);
    fns.applySessionBounds(session, next);
    pushNavState(options, next);
    pushFindState(next);
    if (notify) pushTabsState(options, session, { kind: "selected", tabId });
    if (session.devtools) pushDevToolsState(session);
    if (session.devtools && session.devtools.desiredTabId !== tabId) {
      void fns.retargetDevTools(session, tabId).catch(() => undefined);
    }
		if (session.live && session.live.tabId !== tabId) {
			void fns.retargetLiveStream(session).catch((error) =>
				session.live?.sink.error("BROWSER_CAPTURE_UNAVAILABLE", error instanceof Error ? error.message : "Browser capture failed"),
			);
		}
    return next;
  }

  // Re-verified 2026-08-19: a long-session report claimed tab close buttons
  // eventually "stop working" (tab stays in the rail / count badge doesn't
  // decrement). An accelerated stress repro -- open many tabs and
  // close back to one, 20 cycles, interleaving selectTab/DevTools
  // open-close/annotation-mode toggles -- against this real closeTab/
  // destroyTabView path (see "browser tab lifecycle stress" in
  // browser-view-host.test.ts) completed cleanly with no stuck or leaked
  // tab. Several stabilization fixes already landed on main since the
  // original report and likely already covered it: 73aa2c9bc (stabilize
  // browser tab controls), 7d80f4a32 (prevent wrong tab content after
  // switching), b00aa0414 (cancel stale overlay captures), and 4a98e4a92
  // (release tab menu mirror on selection).
  function closeTab(
    session: BrowserSessionEntry,
    tabId = session.activeTabId,
    // The tab to bring forward when the closed one is in front (default: the
    // last tab), and whether the keyboard moves to it.
    show?: string,
    focus = true,
    closedContents?: {
      id: number;
      session?: BrowserElectronSession;
      contents: BrowserWebContents;
    },
  ): BrowserTabsState {
    fns.assertTabsNotReplacing(session);
    const tab = session.tabs.get(tabId);
    if (!tab)
      throw browserError(
        "TAB_NOT_FOUND",
        `Browser tab ${tabId} does not exist`,
      );
    // An agent tab is the agent's page, not the person's to reopen: Recently
    // Closed only takes the person's own tabs.
    const closedTab = tab.agentTab !== undefined
      ? undefined
      : closedContents
        ? {
            id: tabId,
            url: tab.state.url,
            title: tab.state.title,
            active: false,
            ...(tab.favicon ? { favicon: tab.favicon } : {}),
            untrustedExternalContent: true as const,
          }
        : tabResult(tab, false);
    const wasActive = tabId === session.activeTabId;
    if (closedContents && session.devtools?.contents === closedContents.contents)
      destroyDevTools(session);
    // Activate the replacement BEFORE destroying the closed view. Destroying
    // the focused view first leaves OS focus in limbo until the async close
    // resolves — a second ⌘W in that window bypasses every before-input-event
    // handler and hits the menu's Close item, killing the app window.
    const nextTabId = wasActive
      ? show && show !== tabId && session.tabs.has(show)
        ? show
        : [...session.tabs.keys()].filter((id) => id !== tabId).at(-1)
      : undefined;
    const wasLastTab = session.tabs.size === 1;
    // A destroyed guest's native view must not be laid out as the previous
    // active tab. Remove it before activating the surviving opener.
    if (closedContents) session.tabs.delete(tabId);
    if (nextTabId) activateTab(session, nextTabId, false);
    disposeNetworkCapture(tab, "tab-closed");
    if (session.networkTabId === tabId) session.networkTabId = undefined;
    if (wasLastTab)
      unregisterBrowserSignalWatcher(session, closedContents?.session);
    session.tabs.delete(tabId);
    tabsByWebContentsId.delete(closedContents?.id ?? tab.view.webContents.id);
    if (closedContents) {
      options.mainWindow.contentView.removeChildView?.(tab.view);
      if (session.nativeActiveTabId === tabId)
        session.nativeActiveTabId = undefined;
      session.snapshotDeltaBaseline = undefined;
    } else {
      fns.destroyTabView(tab);
    }
    if (session.tabs.size === 0) {
      session.activeTabId = "";
      session.nativeActiveTabId = undefined;
      session.snapshotDeltaBaseline = undefined;
      destroyDevTools(session);
      session.visible = false;
      session.bounds = OFFSCREEN_BOUNDS;
      shellContents(options).focus();
      forgetNativeFocus(session.viewId);
      forgetBrowserShortcutTarget(session.viewId);
      shellContents(options).send("browser:navState", emptyNavState(session.viewId));
    }
    if (wasActive && nextTabId && focus) {
      const replacement = session.tabs.get(nextTabId);
      if (replacement) {
        hostState.lastUsedViewId = session.viewId;
        hostState.lastFocusedViewId = session.viewId;
        if (isBlankBrowserEntry(replacement)) {
          focusLocation(session);
        } else {
          fns.applySessionBounds(session, replacement);
          replacement.view.webContents.focus();
        }
      }
    }
    const state = listTabs(session, { kind: "closed", tabId, ...(closedTab ? { tab: closedTab } : {}) });
    shellContents(options).send("browser:tabsState", state);
    return state;
  }

  const openUserTab = async (
    session: BrowserSessionEntry,
    url?: string,
  ): Promise<BrowserTabsState> => {
    if (!options.agentBrowserRuntime) {
      await openTab(session, url, true);
      return listTabs(session);
    }
    return queueNativeOperation(session, async () => {
      if (!session.tabs.size) {
        // Connecting agent-browser to zero targets bootstraps its own page
        // before tab-new runs. Create the first user tab directly so one
        // request cannot produce both a bootstrap page and a requested page.
        await openTab(session, url, true);
        session.nativeActiveTabId = undefined;
        session.snapshotDeltaBaseline = undefined;
        return listTabs(session);
      }
      await options.agentBrowserRuntime!.runAction(
        session.sessionId,
        "tab-new",
        { url },
        fns.agentBrowserTargets(session),
      );
      session.nativeActiveTabId = session.activeTabId;
      session.snapshotDeltaBaseline = undefined;
      return listTabs(session);
    });
  };

  const closeNativeTab = async (
    session: BrowserSessionEntry,
    tabId: string,
    signal?: AbortSignal,
  ): Promise<BrowserTabsState> => {
    return queueNativeOperation(
      session,
      async () => {
        if (!session.tabs.has(tabId)) return listTabs(session);
        if (session.tabs.size === 1) return closeTab(session, tabId);
        try {
          await ensureNativeActiveTab(session, signal, true);
        } catch (error) {
          if (!isBrowserTargetMismatch(error)) throw error;
          throwIfAborted(signal);
          // Target drift must remain fail-closed for page actions, but closing
          // this exact Tallpine-owned view cannot dispatch against another native tab.
          if (session.tabs.has(tabId)) closeTab(session, tabId);
          session.nativeActiveTabId = undefined;
          session.snapshotDeltaBaseline = undefined;
          return listTabs(session);
        }
        try {
          const targets = fns.agentBrowserTargets(session);
          if (signal) {
            await options.agentBrowserRuntime!.runAction(
              session.sessionId,
              "tab-close",
              { tabId },
              targets,
              signal,
            );
          } else {
            await options.agentBrowserRuntime!.runAction(
              session.sessionId,
              "tab-close",
              { tabId },
              targets,
            );
          }
        } catch (error) {
          if (!isAgentBrowserCommandFailure(error)) throw error;
          // Closing the Tallpine-owned WebContentsView by its exact logical ID is safe
          // even when agent-browser forgot that ID. It cannot fall through to a
          // different target, unlike a read or page mutation.
          if (session.tabs.has(tabId)) {
            throwIfAborted(signal);
            closeTab(session, tabId);
          }
        }
        session.nativeActiveTabId = undefined;
        session.snapshotDeltaBaseline = undefined;
        return listTabs(session);
      },
      signal,
    );
  };

  const closeUserTab = async (
    session: BrowserSessionEntry,
    tabId: string,
  ): Promise<BrowserTabsState> => {
    if (!session.tabs.has(tabId)) return listTabs(session);
    const entry = session.tabs.get(tabId)!;
    const wasActive = session.activeTabId === tabId;
    try {
      fns.assertProfileStable(session);
      return await entry.driver().close();
    } catch (error) {
      try {
        options.onTabCloseFailure?.({
          error, sessionId: session.sessionId, tabId,
          tabCount: session.tabs.size, tabPresent: session.tabs.has(tabId),
          wasActive, agentTab: entry.agentTab !== undefined,
        });
      } catch {
        // Logging must not change close semantics.
      }
      // A concurrent close (including the runtime's own Target.closeTarget)
      // may have completed the requested operation before reporting failure.
      // Keep its diagnostic, but don't report an already-closed tab as stuck.
      if (!session.tabs.has(tabId)) return listTabs(session);
      throw error;
    }
  };

  const focusLocation = (session: BrowserSessionEntry): void => {
    hostState.lastUsedViewId = session.viewId;
    if (typeof shellWebContents.isFocused === "function") {
      if (!shellWebContents.isFocused()) {
        shellWebContents.focus();
      }
    } else {
      shellWebContents.focus();
    }
    shellWebContents.send("browser:focusLocation", session.viewId);
  };
  const reopenClosedTab = (session: BrowserSessionEntry): void => {
    shellWebContents.send("browser:reopenClosedTab", session.viewId);
  };
  const focusFind = (session: BrowserSessionEntry): void => {
    hostState.lastUsedViewId = session.viewId;
    if (typeof shellWebContents.isFocused === "function") {
      if (!shellWebContents.isFocused()) shellWebContents.focus();
    } else {
      shellWebContents.focus();
    }
    const entry = activeEntry(session);
    shellWebContents.send("browser:findOpen", publicFindState(entry));
  };

  return {
    activateTab,
    closeNativeTab,
    closeTab,
    closeUserTab,
    focusFind,
    focusLocation,
    openUserTab,
    reopenClosedTab,
  };
}
