import { type BrowserProfileId, type BrowserProfileViewState, type BrowserProfileUsage } from "../../shared/browser-profiles";
import { browserError } from "./args";
import { isBlankBrowserEntry } from "./entry-state";
import { disposeNetworkCapture } from "./network-capture";
import type { HostContext } from "./context";
import { createSessionTabs } from "./profile-session-tabs";
import { createProfileSwitch } from "./profile-switch";

export function createDestroyProfile(ctx: HostContext) {
  const { entries, forgetBrowserShortcutTarget, forgetNativeFocus, options, rendererOwnersByViewId, tabsByWebContentsId, viewIdsBySessionId } = ctx;
  const { clearTemporaryPartition, destroyDevTools, profileStateForSession, pushProfileState, unregisterBrowserSignalWatcher } = ctx.fns;

  const sessionTabs = createSessionTabs(ctx);
  const { destroyTabView } = sessionTabs;
  const switchProfile = createProfileSwitch(ctx, sessionTabs);

  const destroy = (viewId: string): void => {
    const session = entries.get(viewId);
    if (!session) return;
		const live = session.live;
		live?.sink.error("BROWSER_SESSION_CLOSED", "The desktop browser session was closed");
		session.live = undefined;
		if (live?.screencast) void live.screencast.stop();
    const partitionToClear =
      session.profileId === null ? session.profilePartition : undefined;
    const agentPartitionToClear = session.agentPartition;
    session.signals.entries.length = 0;
    unregisterBrowserSignalWatcher(session);
    if (options.mainWindow.isDestroyed?.()) session.devtools = undefined;
    else destroyDevTools(session);
    void options.agentBrowserRuntime?.closeSession(session.sessionId);
    entries.delete(viewId);
    viewIdsBySessionId.delete(session.sessionId);
    rendererOwnersByViewId.delete(viewId);
    forgetNativeFocus(viewId);
    forgetBrowserShortcutTarget(viewId);
    // BaseWindow close leaves child WebContents alive, but destroys contentView.
    if (options.mainWindow.isDestroyed?.()) {
      for (const entry of session.tabs.values()) {
        tabsByWebContentsId.delete(entry.view.webContents.id);
        disposeNetworkCapture(entry, "session-closed");
        try {
          entry.view.webContents.close?.();
        } catch {
          // The tab may already have closed before the window teardown.
        }
      }
      if (partitionToClear) clearTemporaryPartition(partitionToClear);
      if (agentPartitionToClear) clearTemporaryPartition(agentPartitionToClear);
      return;
    }
    for (const entry of session.tabs.values()) {
      tabsByWebContentsId.delete(entry.view.webContents.id);
      disposeNetworkCapture(entry, "session-closed");
      destroyTabView(entry);
    }
    if (partitionToClear) clearTemporaryPartition(partitionToClear);
    if (agentPartitionToClear) clearTemporaryPartition(agentPartitionToClear);
  };



  const getProfileState = (viewId: string): BrowserProfileViewState | null => {
    const session = entries.get(viewId);
    return session ? profileStateForSession(session) : null;
  };

  const refreshProfileState = (profileId: BrowserProfileId): void => {
    for (const session of entries.values()) {
      if (session.profileId === profileId) pushProfileState(session);
    }
  };

  const getProfileSwitchInfo = (
    viewId: string,
  ): { hasNavigated: boolean; agentActive: boolean } | null => {
    const session = entries.get(viewId);
    if (!session) return null;
    return {
      hasNavigated: [...session.tabs.values()].some(
        (entry) => !isBlankBrowserEntry(entry),
      ),
      // Renderer page loads do not block: switchProfile stops them itself.
      agentActive: session.agentBrowserCommands > 0 || session.profileSwitching,
    };
  };

  const isProfileLive = (profileId: BrowserProfileId): boolean =>
    [...entries.values()].some(
      (session) =>
        session.profileId === profileId ||
        (session.profileSwitching &&
          session.profileSwitchTargetId === profileId),
    );

  const getProfileUsage = (profileId: BrowserProfileId): BrowserProfileUsage[] =>
    [...entries.values()]
      .filter((session) => session.profileId === profileId ||
        (session.profileSwitching && session.profileSwitchTargetId === profileId))
      .map((session) => ({
        viewId: session.viewId,
        sessionId: session.sessionId,
        blocker: session.profileSwitching ? "switching" :
          session.agentBrowserCommands > 0 ? "agent-active" : null,
      }));

  // Called while the store reserves the profile against new users. Start all
  // switches synchronously so each session blocks new agent commands before
  // yielding; wait for every switch (including failures) before releasing it.
  const detachProfile = async (profileId: BrowserProfileId): Promise<void> => {
    const usage = getProfileUsage(profileId);
    const blocked = usage.filter((session) => session.blocker !== null);
    if (blocked.length) {
      throw browserError("BROWSER_PROFILE_ACTIVE",
        `Wait for browser activity to finish in: ${blocked.map((session) => session.sessionId).join(", ")}. Then try again.`);
    }
    const results = await Promise.allSettled(
      usage.map((session) => switchProfile(session.viewId, null)),
    );
    if (results.some((result) => result.status === "rejected")) {
      throw browserError("BROWSER_PROFILE_SWITCH_FAILED",
        "Some browsers could not switch to temporary profiles. The profile was kept. Browsers already switched remain temporary; please try again.");
    }
  };

  const clearProfileData = async (
    profileId: BrowserProfileId,
  ): Promise<void> => {
    const store = options.browserProfileStore;
    if (!store || !options.clearBrowserProfileData) {
      throw browserError(
        "BROWSER_PROFILE_UNAVAILABLE",
        "Browser profile storage is unavailable",
      );
    }
    const partition = store.partitionForProfile(profileId);
    await Promise.all([
      options.clearBrowserProfileData(partition),
      options.browserHistoryStore?.clear(profileId) ?? Promise.resolve(),
    ]);
  };

  return {
    clearProfileData,
    destroy,
    destroyTabView,
    detachProfile,
    getProfileState,
    getProfileSwitchInfo,
    getProfileUsage,
    isProfileLive,
    refreshProfileState,
    switchProfile,
  };
}
