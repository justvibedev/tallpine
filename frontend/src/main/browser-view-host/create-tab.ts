import { browserError } from "./args";
import type { BrowserEntry, BrowserPopupOptions, BrowserSessionEntry } from "./types";
import type { HostContext } from "./context";
import { buildEntry, prepareView, wireConsoleReports, wireEntryReady, wireInputEvents, wireNavigation, wirePopups, wireRemoteTabReports, wireScrollbarStyle, wireTabLifecycle, type TabDeps, type TabWiring } from "./create-tab-steps";

export function createTabFactory(ctx: HostContext, deps: TabDeps) {
  const { entries } = ctx;
  const createTab = (
    session: BrowserSessionEntry,
    activate: boolean,
    syncNativeOnActivate = false,
    preferredTabId?: string,
    popupOptions?: BrowserPopupOptions,
    agentDriven = false,
  ): BrowserEntry => {
    if (session.tabs.size > 0) session.livePreviewPending = false;
    const tabId = preferredTabId ?? `t${session.nextTabNumber++}`;
    if (session.tabs.has(tabId)) {
      throw browserError(
        "INVALID_ARGUMENT",
        `Browser tab ${tabId} already exists`,
      );
    }
    const { view, webPreferences, agentProxyReady } = prepareView({ ctx, deps, session, agentDriven, popupOptions });
    wireScrollbarStyle({ view });
    const entry = buildEntry({ ctx, session, tabId, view, webPreferences, agentDriven });
    const isCurrentEntry = () =>
      entries.get(session.viewId) === session &&
      session.tabs.get(entry.tabId) === entry;
    const wiring: TabWiring = {
      ctx, deps, session, entry, view, tabId, webPreferences, isCurrentEntry,
      remote: {}, createTab, agentProxyReady, popupOptions, agentDriven,
    };
    wireTabLifecycle(wiring);
    wireConsoleReports(wiring);
    wirePopups(wiring);
    wireNavigation(wiring);
    wireRemoteTabReports(wiring);
    wireInputEvents(wiring);
    wireEntryReady(wiring);
    if (activate) {
      ctx.fns.activateTab(session, tabId, false);
      if (syncNativeOnActivate) ctx.fns.queueNativeActiveTabSync(session);
    }
    return entry;
  };
  return createTab;
}
