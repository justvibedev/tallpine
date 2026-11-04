import { listTabs, runInRemoteViewer } from "./nav-events";
import type { BrowserEntry, BrowserSessionEntry, BrowserTabsState } from "./types";
import type { HostContext } from "./context";

export function createAgentTabPages(
  ctx: HostContext,
  openAgentTab: (session: BrowserSessionEntry, url: string, agentTab: string, activate: boolean, listed?: { title: string; url: string }) => Promise<void>,
) {
  const { fns } = ctx;
  const { withBrowserOperation } = ctx.fns;

  // Loads an agent tab's viewer, pinned to its page, the first time the tab
  // is shown, or once no other viewer is left to report the agent's pages.
  // ponytail: a viewer stays loaded once shown, so a session holds one per
  // agent tab it has shown; unload those left behind (back to `viewer`) if
  // hosts still run short of sockets.
  const loadViewer = (session: BrowserSessionEntry, entry: BrowserEntry): void => {
    const viewer = entry.viewer;
    if (!viewer) return;
    entry.viewer = undefined;
    void withBrowserOperation(session, () => fns.navigateEntry(entry, viewer)).catch(() => undefined);
  };

  // The one way an agent tab closes, whoever closes it. agent-browser never
  // sees agent tabs (agentBrowserTargets), so this never goes through its
  // tab-select or tab-close. closePage: a tab closed here closes its page in
  // the agent browser, as closing any tab would; a page that closed on its
  // own just takes its tab along. show: the tab to bring forward when this
  // one is in front (else the last tab, as for any tab).
  const closeAgentTab = async (
    session: BrowserSessionEntry,
    tabId: string,
    closePage: boolean,
    show?: string,
  ): Promise<BrowserTabsState> => {
    const entry = session.tabs.get(tabId);
    if (!entry) return listTabs(session);
    const page = entry.agentTab;
    // Its page is going: a report from another viewer that has not heard yet
    // must not bring it back meanwhile.
    if (page) (session.agentTabsClosed ??= new Map()).set(page, { at: Date.now(), closePage });
    if (closePage && page && !entry.remote?.closed) {
      entry.closing = true;
      // Before the view goes: a viewer's socket carries the close. Any viewer
      // closes any page, so one closed before its first report has another
      // agent tab's viewer close it (and one between sockets, syncAgentTabs).
      const via = entry.remote ? entry : [...session.tabs.values()].find((tab) => tab.remote);
      if (via) await Promise.race([runInRemoteViewer(via, `close(${JSON.stringify(page)})`), new Promise((resolve) => setTimeout(resolve, 800))]);
      // Another close (a second click, a profile switch) may have taken the
      // tab meanwhile: nothing left to do.
      if (session.tabs.get(tabId) !== entry) return listTabs(session);
    }
    if (session.nativeActiveTabId === tabId) session.nativeActiveTabId = undefined;
    // The keyboard moves with a close the person made, or off a tab that had
    // it; a page closing on its own leaves it where the person is typing.
    const tabs = fns.closeTab(session, tabId, show, closePage || entry.view.webContents.isFocused?.() === true);
    // The agent's pages reach the panel only through a viewer's reports: one
    // stays loaded while agent tabs are left.
    const left = [...session.tabs.values()].filter((tab) => tab.agentTab !== undefined);
    if (left.length && left.every((tab) => tab.viewer)) loadViewer(session, left[0]!);
    return tabs;
  };

  // The panel keeps one tab for each of the agent browser's tabs, as with a
  // person's own tabs: a page the agent opens appears as a tab (in front when
  // the agent opened it from the tab in use), and a page that closes takes
  // its tab along, back to the tab that opened it. Main owns this, so each
  // page gets exactly one tab however many viewers report at once.
  const syncAgentTabs = (
    session: BrowserSessionEntry,
    from: BrowserEntry,
    followed: string,
  ): void => {
    const report = from.remote;
    if (!report || session.profileSwitching || session.profileReplacing) return;
    const shown = new Map<string, BrowserEntry>();
    for (const tab of session.tabs.values()) {
      if (tab.agentTab && !shown.has(tab.agentTab)) shown.set(tab.agentTab, tab);
    }
    const closedHere = session.agentTabsClosed;
    for (const [id, { at, closePage }] of closedHere ?? []) {
      // Within 5 s its close is under way, and other viewers may not have heard.
      if (Date.now() - at <= 5_000) continue;
      // A page a person closed that is still listed never got the close (no
      // viewer was connected then): it goes again, through this viewer.
      if (closePage && report.tabs.some((tab) => tab.id === id)) void runInRemoteViewer(from, `close(${JSON.stringify(id)})`);
      else closedHere!.delete(id);
    }
    const inUse = session.activeTabId === from.tabId;
    // A tab the agent opened from the one in use comes to the front, as a
    // link opening a new tab does (one opened below opens in front).
    const follow = followed ? shown.get(followed) : undefined;
    if (inUse && follow) fns.activateTab(session, follow.tabId);
    for (const page of report.tabs) {
      const { id } = page;
      if (shown.has(id) || closedHere?.has(id)) continue;
      shown.set(id, from);
      const url = new URL(from.view.webContents.getURL());
      url.searchParams.set("tab", id);
      void openAgentTab(session, url.href, id, inUse && id === followed, page).catch(() => undefined);
    }
    // A tab without a viewer hears nothing from its page: it goes once no
    // viewer lists the page. Each list is as of its viewer's last report, so
    // a viewer that has not yet heard of a new page takes no tab away.
    const listedPages = new Set([...session.tabs.values()].flatMap((tab) => tab.remote?.tabs.map(({ id }) => id) ?? []));
    for (const tab of [...session.tabs.values()]) {
      if (tab.viewer && tab.agentTab && !listedPages.has(tab.agentTab))
        void closeAgentTab(session, tab.tabId, false).catch((error) => console.warn("Unable to close the agent browser's tab", error));
    }
    // The last agent tab stays (empty) while the agent has no page: its viewer
    // is how the agent's next page reaches the panel.
    const closed = report.closed;
    const others = [...session.tabs.values()].some((tab) => tab !== from && tab.agentTab !== undefined);
    if (!closed || !others || from.closing) return;
    // In front, it hands over to the tab that opened it. Pages opened just
    // above map to `from` itself: no opener to go back to.
    const opener = closed.next ? shown.get(closed.next) : undefined;
    void closeAgentTab(session, from.tabId, false, opener && opener !== from ? opener.tabId : undefined).catch(
      (error) => console.warn("Unable to close the agent browser's tab", error),
    );
  };

  return { loadViewer, closeAgentTab, syncAgentTabs };
}
