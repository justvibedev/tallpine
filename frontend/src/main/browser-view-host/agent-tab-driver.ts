import { pushNavState, runInRemoteViewer, viewerRuns } from "./nav-events";
import type { BrowserEntry, BrowserSessionEntry, BrowserTabsState, TabDriver } from "./types";
import type { HostContext } from "./context";

export function createTabDriver(
  ctx: HostContext,
  pages: { loadViewer: (session: BrowserSessionEntry, entry: BrowserEntry) => void; closeAgentTab: (session: BrowserSessionEntry, tabId: string, closePage: boolean, show?: string) => Promise<BrowserTabsState> },
) {
  const { fns, options } = ctx;
  const { loadViewer, closeAgentTab } = pages;

  // How the panel reads and drives a tab: a person's tab is its own page, an
  // agent tab the agent's page, which its viewer reports and drives. Picked
  // here, once: an operation on a tab goes through its driver (entry.driver)
  // rather than checking agentTab. Checks that keep agent tabs out of what is
  // the person's (history, Recently Closed, agent-browser) stay where they are.
  const tabDriver = (session: BrowserSessionEntry, entry: BrowserEntry): TabDriver => {
    const { webContents } = entry.view;
    if (entry.agentTab === undefined)
      return {
        page: () => ({
          url: webContents.getURL(),
          title: webContents.getTitle(),
          canGoBack: webContents.canGoBack(),
          canGoForward: webContents.canGoForward(),
          isLoading: webContents.isLoading(),
        }),
        nav: (command) => {
          if (command === "back") {
            if (webContents.canGoBack()) webContents.goBack();
          } else if (command === "forward") {
            if (webContents.canGoForward()) webContents.goForward();
          } else if (command === "reload") webContents.reload();
          else webContents.stop();
        },
        navigate: (href, loadOptions) => fns.loadEntry(entry, href, loadOptions),
        show: () => undefined,
        // Keep closure serialized with native page actions.
        close: async (signal) =>
          options.agentBrowserRuntime ? fns.closeNativeTab(session, entry.tabId, signal) : fns.closeTab(session, entry.tabId),
      };
    return {
      page: () => {
        const remote = entry.remote;
        if (remote)
          return { url: remote.url, title: remote.title, canGoBack: remote.back, canGoForward: remote.fwd, isLoading: remote.loading };
        // Before its viewer reports, its page is as another viewer lists it,
        // and there is no page history yet: the viewer's own is not the
        // person's to walk (nav).
        const listed = entry.listed;
        return {
          url: listed ? listed.url : webContents.getURL(),
          title: listed ? listed.title : webContents.getTitle(),
          canGoBack: false,
          canGoForward: false,
          isLoading: webContents.isLoading(),
        };
      },
      // Back/Forward/Reload/Stop drive the agent's page through its viewer
      // and never the viewer's own history (Back there left the viewer for
      // about:blank). Before the viewer first reports there is no page to
      // drive: Reload reloads the viewer, and the rest do nothing (Stop would
      // abort the viewer's load). So does Reload in a viewer that has stopped
      // running.
      nav: (command) => {
        if (entry.remote && command === "reload") {
          void viewerRuns(entry)
            .then((runs) => (runs ? runInRemoteViewer(entry, "reload()") : webContents.reload()))
            .catch(() => undefined);
        } else if (entry.remote) void runInRemoteViewer(entry, `${command}()`);
        else if (command === "reload") webContents.reload();
      },
      // An address typed into it loads in the agent's page (nothing happens
      // before its viewer runs); the viewer's own address is main's load of
      // the viewer, into the tab itself.
      navigate: async (href) => {
        if (options.isAgentBrowserUrl?.(href)) return fns.loadEntry(entry, href);
        if (entry.remote) void runInRemoteViewer(entry, `navigate(${JSON.stringify(href)})`);
        return pushNavState(options, entry);
      },
      // Offscreen tabs stay "visible" to their pages, and the agent's Chrome
      // paints only its front page: the viewer streams, and brings its page to
      // the front, only while its tab is the one shown. Shown for the first
      // time, the tab loads its viewer (openAgentTab).
      show: (shown) => {
        if (shown) loadViewer(session, entry);
        if (entry.remote && entry.remote.shown !== shown) void runInRemoteViewer(entry, `show(${shown})`);
      },
      // Never through agent-browser, which never sees agent tabs: the page
      // closes in the agent browser too.
      close: () => closeAgentTab(session, entry.tabId, true),
    };
  };

  return tabDriver;
}
