import { readNavState } from "./nav-events";
import { isAllowedBrowserURL } from "./url";
import type { BrowserEntry, BrowserSessionEntry } from "./types";
import { OFFSCREEN_BOUNDS } from "./constants";
import { disposeNetworkCapture } from "./network-capture";
import type { HostContext } from "./context";

// Tab-level teardown and rebuild used when a browser session closes or switches profile.
export type SavedBrowserTab = { tabId: string; url?: string; agentTab?: string; viewer?: string; agentDriven?: boolean };

export function createSessionTabs(ctx: HostContext) {
  const { options, tabsByWebContentsId } = ctx;
  const { activateTab, applyBrowserViewBounds, createTab } = ctx.fns;

  const destroyTabView = (entry: BrowserEntry): void => {
    applyBrowserViewBounds(entry.view, OFFSCREEN_BOUNDS, false);
    options.mainWindow.contentView.removeChildView?.(entry.view);
    if (entry.view.webContents.debugger?.isAttached()) {
      entry.view.webContents.debugger.detach();
    }
    entry.view.webContents.close?.();
  };

  // agentTab and viewer: an agent tab's binding and, until it is first shown,
  // its viewer (BrowserEntry). They are kept as is: its address says nothing
  // while its host is away.

  const disposeSessionTabs = (session: BrowserSessionEntry): void => {
    for (const entry of session.tabs.values()) {
      tabsByWebContentsId.delete(entry.view.webContents.id);
      disposeNetworkCapture(entry, "profile-switch");
      if (!options.mainWindow.isDestroyed?.()) destroyTabView(entry);
    }
    session.tabs.clear();
    session.activeTabId = "";
    session.networkTabId = undefined;
    session.nativeActiveTabId = undefined;
    session.snapshotDeltaBaseline = undefined;
  };

  const savedTabsForSession = (
    session: BrowserSessionEntry,
  ): SavedBrowserTab[] =>
    [...session.tabs.values()].map((entry) => {
      const { tabId, agentTab, viewer, agentDriven } = entry;
      try {
        const url = entry.view.webContents.getURL();
        return {
          tabId,
          agentTab,
          viewer,
          ...(agentDriven ? { agentDriven } : {}),
          ...(isAllowedBrowserURL(url, options.rendererOrigin) ? { url } : {}),
        };
      } catch {
        return { tabId, agentTab, viewer, ...(agentDriven ? { agentDriven } : {}) };
      }
    });

  const rebuildSessionTabs = async (
    session: BrowserSessionEntry,
    savedTabs: SavedBrowserTab[],
    activeTabId: string,
    nextTabNumber: number,
    assertCurrentSession: () => void,
  ): Promise<void> => {
    assertCurrentSession();
    session.activeTabId = "";
    session.nextTabNumber = 1;
    const tabs = savedTabs;
    if (!tabs.length) {
      session.nextTabNumber = nextTabNumber;
      session.nativeActiveTabId = undefined;
      return;
    }
    let highestTabNumber = 1;
    for (const saved of tabs) {
      assertCurrentSession();
      const match = /^t(\d+)$/.exec(saved.tabId);
      if (match)
        highestTabNumber = Math.max(highestTabNumber, Number(match[1]));
      const entry = createTab(session, false, false, saved.tabId, undefined, saved.agentDriven === true);
      // An agent tab stays one, bound to the same page.
      entry.agentTab = saved.agentTab;
      entry.viewer = saved.viewer;
    }
    session.nextTabNumber = Math.max(nextTabNumber, highestTabNumber + 1);
    // Activate before reloading: pages can take seconds to load in the new
    // profile, and IPC, overlay refreshes, and agent commands that run in the
    // meantime all need an active tab (an empty one crashed the main process).
    const nextActiveTabId = session.tabs.has(activeTabId)
      ? activeTabId
      : tabs[0]!.tabId;
    activateTab(session, nextActiveTabId, false);
    for (const saved of tabs) {
      if (!saved.url) continue;
      const entry = session.tabs.get(saved.tabId);
      if (!entry) continue;
      await entry.ready;
      assertCurrentSession();
      try {
        await entry.view.webContents.loadURL(saved.url);
      } catch (error) {
        entry.state = {
          ...readNavState(entry),
          error:
            error instanceof Error
              ? error.message
              : "Unable to reload page after profile switch",
        };
      }
      assertCurrentSession();
    }
    assertCurrentSession();
    // A newly-created agent-browser runtime starts on the provider's first
    // target (agent tabs are none), regardless of which human tab Tallpine restored
    // as active. Preserve that distinction so the next agent command selects
    // the right target.
    session.nativeActiveTabId = tabs.find((tab) => tab.agentTab === undefined)?.tabId;
  };

  return { destroyTabView, disposeSessionTabs, savedTabsForSession, rebuildSessionTabs };
}
