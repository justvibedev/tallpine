import { pushTabsState } from "./nav-events";
import type { BrowserEntry, BrowserSessionEntry } from "./types";
import { AGENT_LOGINS_DIALOG_WAIT_MS, POPUP_TARGET_SYNC_TIMEOUT_MS } from "./constants";
import { abortableBrowserResult } from "./args";
import type { HostContext } from "./context";
import { createEnsureNativeActiveTab } from "./agent-tabs-native";
import { createOpenTab } from "./agent-open-tab";
import { createAgentTabPages } from "./agent-tab-pages";
import { createTabDriver } from "./agent-tab-driver";
import { AgentLoginGrants, needsAgentTab, needsProfileTab, personPageInFront } from "./agent-partition";

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
  const resolveAgentLogins = (session: BrowserSessionEntry): Promise<void> | undefined => {
    const profileId = session.profileId;
    if (profileId === null || session.agentLoginsGrantedProfileId === profileId) return undefined;
    const profileName = options.browserProfileStore?.getProfile(profileId)?.name ?? "";
    return loginGrants.request(session.sessionId, profileId, profileName, () => {
      if (session.profileId === profileId) session.agentLoginsGrantedProfileId = profileId;
    });
  };

  // Like resolveAgentLogins, but when the person's own page is in front the
  // command waits (bounded) for their answer: a grant lets this very command
  // read their page, instead of a blank agent tab opened beside it.
  // Returns undefined (no await, so nothing reorders) when it does not wait.
  const settleAgentLogins = (
    session: BrowserSessionEntry,
    signal: AbortSignal | undefined,
  ): Promise<void> | undefined => {
    const answer = resolveAgentLogins(session);
    if (!answer || !personPageInFront(session.tabs.get(session.activeTabId), session)) return undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const timeout = new Promise<void>((resolve) => {
      timer = setTimeout(resolve, AGENT_LOGINS_DIALOG_WAIT_MS);
    });
    return abortableBrowserResult(Promise.race([answer, timeout]), signal).finally(() => clearTimeout(timer));
  };

  const openTab = createOpenTab(ctx, resolveAgentLogins);

  // Puts the session on an agent tab when its active one is the person's own and
  // logins are not granted: reuses an existing agent tab, else opens one.
  // returnToPerson (callers outside the native queue only): after a grant, a
  // still-blank agent tab gives way to the person's page it displaced and is
  // returned for the caller to close (its close runs in the native queue).
  const ensureAgentTab = async (
    session: BrowserSessionEntry,
    returnToPerson = false,
  ): Promise<BrowserEntry | undefined> => {
    const active = session.tabs.get(session.activeTabId);
    if (needsProfileTab(active, session)) {
      const inProfile = [...session.tabs.values()].find(
        (entry) => entry.agentDriven && entry.partition === session.profilePartition,
      );
      if (inProfile) {
        fns.activateTab(session, inProfile.tabId, false);
        return undefined;
      }
      const url = active!.view.webContents.getURL();
      // The agent was moved off the person's page and has loaded nothing since:
      // with logins granted it may now drive that page, so it goes back to it.
      const displaced = session.agentDisplacedTabId
        ? session.tabs.get(session.agentDisplacedTabId)
        : undefined;
      if (returnToPerson && !/^https?:/i.test(url) && displaced && !displaced.agentDriven && !needsAgentTab(displaced, session)) {
        fns.activateTab(session, displaced.tabId, false);
        return active;
      }
      // Carry the page over: the agent keeps its place, now with the person's logins.
      await openTab(session, /^https?:/i.test(url) ? url : undefined, true, "opened", true, true);
      return undefined;
    }
    if (!needsAgentTab(active, session)) return undefined;
    session.agentDisplacedTabId = active!.tabId;
    const existing = [...session.tabs.values()].find((entry) => entry.agentDriven);
    if (existing) {
      fns.activateTab(session, existing.tabId, false);
      return undefined;
    }
    await openTab(session, undefined, true, "opened", true, true);
    return undefined;
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
    settleAgentLogins,
    ensureAgentTab,
    redirectToAgentTab,
    queueNativeActiveTabSync,
    syncAgentTabs,
    tabDriver,
  };
}
