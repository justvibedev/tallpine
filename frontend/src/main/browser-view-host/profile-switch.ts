import { rollbackSwitch, validateSwitch, type SwitchRun } from "./profile-switch-checks";
import { browserProfilePartition, type BrowserProfileId, type BrowserProfileViewState } from "../../shared/browser-profiles";
import { cancelAnnotation } from "./annotation-helpers";
import { browserError } from "./args";
import { pushNavState, pushTabsState } from "./nav-events";
import { activeEntry, temporaryBrowserPartition } from "./entry-state";
import { PROFILE_SWITCH_SETTLE_MS } from "./constants";
import type { HostContext } from "./context";
import { createSessionTabs } from "./profile-session-tabs";

export function createProfileSwitch(ctx: HostContext, tabs: ReturnType<typeof createSessionTabs>) {
  const { entries, explicitTemporarySessionIds, isBrowserIdle, options, settlesWithin, waitForBrowserIdle } = ctx;
  const { clearTemporaryPartition, destroyDevTools, profileStateForSession, pushDevToolsState, pushProfileState, registerBrowserSignalWatcher, unregisterBrowserSignalWatcher } = ctx.fns;
  const { disposeSessionTabs, savedTabsForSession, rebuildSessionTabs } = tabs;

  const switchProfile = async (
    viewId: string,
    requestedProfileId: BrowserProfileId | null,
  ): Promise<BrowserProfileViewState> => {
    const checked = validateSwitch(ctx, viewId, requestedProfileId);
    if ("unchanged" in checked) return checked.unchanged;
    const { session, store, normalizedRequestedProfileId } = checked;
    session.profileSwitching = true;
    session.profileSwitchTargetId = normalizedRequestedProfileId;
    const previousProfileId = session.profileId;
    const previousPartition = session.profilePartition;
    const run: SwitchRun = {
      previousActiveTabId: session.activeTabId,
      previousNextTabNumber: session.nextTabNumber,
      savedTabs: [],
      bindingChanged: false,
      didTearDown: false,
    };
    const assertCurrentSession = (): void => {
      if (entries.get(viewId) !== session) {
        throw browserError(
          "BROWSER_TARGET_UNAVAILABLE",
          "Browser target is no longer available",
        );
      }
    };
    try {
      // A confirmed human switch outranks in-flight browser work. Setting
      // profileSwitching above already refuses new agent commands; let the ones
      // in flight finish. Page loads can stay pending for as long as a slow page
      // keeps loading, so remember their targets, stop them so navigate()
      // settles, and reload them in the new profile.
      const pendingURLs = new Map<string, string>();
      for (const entry of session.tabs.values()) {
        if (entry.pendingNavigation)
          pendingURLs.set(entry.tabId, entry.pendingNavigation.url);
      }
      if (session.browserOperations > 0) {
        for (const entry of session.tabs.values())
          entry.view.webContents.stop();
      }
      const deadline = Date.now() + PROFILE_SWITCH_SETTLE_MS;
      const assertSettled = (settled: boolean): void => {
        assertCurrentSession();
        if (!settled || !isBrowserIdle(session)) {
          throw browserError(
            "BROWSER_PROFILE_ACTIVE",
            "Wait for browser activity to finish before switching profiles",
          );
        }
      };
      await waitForBrowserIdle(session, PROFILE_SWITCH_SETTLE_MS);
      assertSettled(true);
      // A renderer tab-selection operation does not increment the agent activity
      // counter. Let already-queued native work finish before tearing down CDP,
      // within the same deadline; the queue itself is left intact.
      assertSettled(
        await settlesWithin(session.nativeOperationQueue, deadline - Date.now()),
      );
      session.profileReplacing = true;
      run.previousActiveTabId = session.activeTabId;
      run.previousNextTabNumber = session.nextTabNumber;
      run.savedTabs = savedTabsForSession(session).map((tab) => {
        const url = pendingURLs.get(tab.tabId);
        return url ? { ...tab, url } : tab;
      });
      destroyDevTools(session);
      for (const entry of session.tabs.values())
        cancelAnnotation(options, entry, "navigation");
      await options.agentBrowserRuntime?.closeSession(session.sessionId);
      assertCurrentSession();
      await store.bindSession(session.sessionId, normalizedRequestedProfileId);
      run.bindingChanged = true;
      assertCurrentSession();

      unregisterBrowserSignalWatcher(session);
      disposeSessionTabs(session);
      run.didTearDown = true;
      session.profileId = normalizedRequestedProfileId;
      session.profilePartition = normalizedRequestedProfileId
        ? browserProfilePartition(normalizedRequestedProfileId)
        : temporaryBrowserPartition();
      await rebuildSessionTabs(
        session,
        run.savedTabs,
        run.previousActiveTabId,
        run.previousNextTabNumber,
        assertCurrentSession,
      );
      registerBrowserSignalWatcher(session);
      pushTabsState(options, session);
      pushProfileState(session);
      pushDevToolsState(session);
      if (session.tabs.size) pushNavState(options, activeEntry(session));
      if (normalizedRequestedProfileId === null)
        explicitTemporarySessionIds.add(session.sessionId);
      else explicitTemporarySessionIds.delete(session.sessionId);
      if (previousProfileId === null)
        await clearTemporaryPartition(previousPartition);
      return profileStateForSession(session);
    } catch (error) {
      await rollbackSwitch(ctx, tabs, viewId, session, store, previousProfileId, previousPartition, run, assertCurrentSession);
      throw error;
    } finally {
      session.profileSwitching = false;
      session.profileReplacing = false;
      session.profileSwitchTargetId = null;
    }
  };

  return switchProfile;
}
