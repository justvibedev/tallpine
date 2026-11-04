import { pushTabsState } from "./nav-events";
import type { BrowserSessionEntry } from "./types";
import { POPUP_TARGET_SYNC_TIMEOUT_MS } from "./constants";
import type { HostContext } from "./context";
import { createEnsureNativeActiveTab } from "./agent-tabs-native";
import { createOpenTab } from "./agent-open-tab";
import { createAgentTabPages } from "./agent-tab-pages";
import { createTabDriver } from "./agent-tab-driver";
import { AgentLoginGrants, needsAgentTab, needsProfileTab } from "./agent-partition";

export function createAgentTabs(ctx: HostContext) {
  const { fns, options } = ctx;
  const { createTab, queueNativeOperation, withBrowserOperation } = ctx.fns;
  const ensureNativeActiveTab = createEnsureNativeActiveTab(ctx);


  function queueNativeActiveTabSync(session: BrowserSessionEntry): void {
    const controller = new AbortController();
    const timeout = setTimeout(
      () => controller.abort(),
      POPUP_TARGET_SYNC_TIMEOUT_MS,
    );
    void queueNativeOperation(
      session,
      () => ensureNativeActiveTab(session, controller.signal),
      controller.signal,
    )
      .catch(() => undefined)
      .finally(() => clearTimeout(timeout));
  }

  // Agent tabs get a throwaway partition unless the person lets the agent use
  // the named profile's logins; ask once per session and profile, without waiting for the answer.
  const loginGrants = new AgentLoginGrants(options.confirmAgentLogins);
  const resolveAgentLogins = (session: BrowserSessionEntry): void => {
    const profileId = session.profileId;
    if (profileId === null || session.agentLoginsGrantedProfileId === profileId) return;
    const profileName = options.browserProfileStore?.getProfile(profileId)?.name ?? "";
    loginGrants.request(session.sessionId, profileId, profileName, () => {
      if (session.profileId === profileId) session.agentLoginsGrantedProfileId = profileId;
    });
  };

  const openTab = createOpenTab(ctx, resolveAgentLogins);

  // Puts the session on an agent tab when its active one is the person's own and
  // logins are not granted: reuses an existing agent tab, else opens one.
  const ensureAgentTab = async (session: BrowserSessionEntry): Promise<void> => {
    const active = session.tabs.get(session.activeTabId);
    if (needsProfileTab(active, session)) {
      const inProfile = [...session.tabs.values()].find(
        (entry) => entry.agentDriven && entry.partition === session.profilePartition,
      );
      if (inProfile) {
        fns.activateTab(session, inProfile.tabId, false);
        return;
      }
      // Carry the page over: the agent keeps its place, now with the person's logins.
      const url = active!.view.webContents.getURL();
      await openTab(session, /^https?:/i.test(url) ? url : undefined, true, "opened", true, true);
      return;
    }
    if (!needsAgentTab(active, session)) return;
    const existing = [...session.tabs.values()].find((entry) => entry.agentDriven);
    if (existing) {
      fns.activateTab(session, existing.tabId, false);
      return;
    }
    await openTab(session, undefined, true, "opened", true, true);
  };

  // Call inside the native queue, after ensureNativeActiveTab and before the
  // active tab is targeted: the person may have selected their own tab while the
  // command waited.
  const redirectToAgentTab = async (
    session: BrowserSessionEntry,
    signal: AbortSignal | undefined,
  ): Promise<void> => {
    const before = session.activeTabId;
    await ensureAgentTab(session);
    if (session.activeTabId !== before) await ensureNativeActiveTab(session, signal, false);
  };


  // Opens a tab bound to one agent browser tab. The binding is set before
  // anything awaits, so the next report already finds the page shown.
  // listed: the page as the reporting viewer lists it.
  const openAgentTab = (
    session: BrowserSessionEntry,
    url: string,
    agentTab: string,
    activate: boolean,
    listed?: { title: string; url: string },
  ): Promise<void> =>
    withBrowserOperation(session, async () => {
      const id = `a${(session.agentTabNumber = (session.agentTabNumber ?? 0) + 1)}`;
      // With no tab in use (the person closed them all), the agent's tab is
      // the one in front: a panel with tabs always has an active one.
      activate ||= !session.tabs.has(session.activeTabId);
      const entry = createTab(session, activate, false, id);
      entry.agentTab = agentTab;
      entry.listed = listed;
      // A viewer holds a socket and a CDP session on the agent's host, and
      // every session showing the agent's browser has its own: a page not
      // brought to the front gets its viewer when first shown (loadViewer).
      // The first agent tab, bound to no page yet, loads its viewer at once:
      // the agent's pages reach the panel through it.
      if (agentTab && !activate) {
        entry.viewer = url;
        pushTabsState(options, session, { kind: "opened", tabId: entry.tabId });
        return;
      }
      await entry.ready;
      const navigation = fns.navigateEntry(entry, url);
      pushTabsState(options, session, { kind: "opened", tabId: entry.tabId });
      await navigation;
    });





  const pages = createAgentTabPages(ctx, openAgentTab);
  const { syncAgentTabs } = pages;
  const tabDriver = createTabDriver(ctx, pages);

  return {
    ensureNativeActiveTab,
    openAgentTab,
    openTab,
    resolveAgentLogins,
    ensureAgentTab,
    redirectToAgentTab,
    queueNativeActiveTabSync,
    syncAgentTabs,
    tabDriver,
  };
}
