import type { BrowserSessionEntry } from "./types";
import { isValidBrowserProfileSessionId, normalizeBrowserProfileId, type BrowserProfileId, type BrowserProfileViewState } from "../../shared/browser-profiles";
import { browserError } from "./args";
import { pushNavState, pushTabsState } from "./nav-events";
import { activeEntry } from "./entry-state";
import type { HostContext } from "./context";
import { createSessionTabs } from "./profile-session-tabs";
import type { SavedBrowserTab } from "./profile-session-tabs";



export type SwitchRun = {
  previousActiveTabId: string;
  previousNextTabNumber: number;
  savedTabs: SavedBrowserTab[];
  bindingChanged: boolean;
  didTearDown: boolean;
};


// Rejects a switch the session cannot take; "unchanged" means it is already on that profile.
export const validateSwitch = (
  ctx: HostContext,
  viewId: string,
  requestedProfileId: BrowserProfileId | null,
):
  | { unchanged: BrowserProfileViewState }
  | { session: BrowserSessionEntry; store: NonNullable<HostContext["options"]["browserProfileStore"]>; normalizedRequestedProfileId: BrowserProfileId | null } => {
  const { pushProfileState } = ctx.fns;
  const { entries, options } = ctx;
  const session = entries.get(viewId);
  if (!session)
    throw browserError(
      "BROWSER_TARGET_UNAVAILABLE",
      "Browser target is unavailable",
    );
  const store = options.browserProfileStore;
  if (!store)
    throw browserError(
      "BROWSER_PROFILE_UNAVAILABLE",
      "Browser profiles are unavailable",
    );
  if (!isValidBrowserProfileSessionId(session.sessionId)) {
    throw browserError(
      "INVALID_ARGUMENT",
      "Worker session ID is invalid for a durable browser profile binding",
    );
  }
  const normalizedRequestedProfileId =
    requestedProfileId === null
      ? null
      : normalizeBrowserProfileId(requestedProfileId);
  if (requestedProfileId !== null && !normalizedRequestedProfileId) {
    throw browserError("INVALID_ARGUMENT", "Profile ID is invalid");
  }
  if (
    normalizedRequestedProfileId !== null &&
    !store.getProfile(normalizedRequestedProfileId)
  ) {
    throw browserError(
      "BROWSER_PROFILE_NOT_FOUND",
      "Browser profile was not found",
    );
  }
  if (normalizedRequestedProfileId === session.profileId)
    return { unchanged: pushProfileState(session) };
  if (session.profileSwitching) {
    throw browserError(
      "BROWSER_PROFILE_ACTIVE",
      "Wait for browser activity to finish before switching profiles",
    );
  }
  if (
    normalizedRequestedProfileId !== null &&
    store.isProfileOperationInProgress(normalizedRequestedProfileId)
  ) {
    throw browserError(
      "BROWSER_PROFILE_ACTIVE",
      "Wait for browser profile data operations to finish before switching",
    );
  }
  return { session, store, normalizedRequestedProfileId };
};


// Undoes a failed switch: the binding, then the old profile's tabs.
export const rollbackSwitch = async (
  ctx: HostContext,
  tabs: ReturnType<typeof createSessionTabs>,
  viewId: string,
  session: BrowserSessionEntry,
  store: NonNullable<HostContext["options"]["browserProfileStore"]>,
  previousProfileId: BrowserProfileId | null,
  previousPartition: string,
  run: SwitchRun,
  assertCurrentSession: () => void,
): Promise<void> => {
  const { entries, options } = ctx;
  const { pushDevToolsState, pushProfileState, registerBrowserSignalWatcher, unregisterBrowserSignalWatcher } = ctx.fns;
  const { disposeSessionTabs, rebuildSessionTabs } = tabs;
    if (run.bindingChanged) {
      try {
        await store.bindSession(session.sessionId, previousProfileId);
      } catch (rollbackError) {
        console.error(
          "browser profile binding rollback failed:",
          rollbackError,
        );
      }
    }
    const sessionIsCurrent = entries.get(viewId) === session;
    if (sessionIsCurrent && run.didTearDown && session.tabs.size > 0) {
      unregisterBrowserSignalWatcher(session);
      disposeSessionTabs(session);
    }
    session.profileId = previousProfileId;
    session.profilePartition = previousPartition;
    if (sessionIsCurrent && run.didTearDown) {
      try {
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
      } catch (restoreError) {
        console.error("browser profile view rollback failed:", restoreError);
      }
    }
};

