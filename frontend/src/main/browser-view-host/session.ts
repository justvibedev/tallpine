import { browserProfilePartition } from "../../shared/browser-profiles";
import { browserError } from "./args";
import { temporaryBrowserPartition } from "./entry-state";
import type { BrowserSessionEntry } from "./types";
import { DEFAULT_NATIVE_DEVTOOLS_PLACEMENT, OFFSCREEN_BOUNDS } from "./constants";
import { disposeNetworkCapture } from "./network-capture";
import type { HostContext } from "./context";
import { createTabFactory } from "./create-tab";
import { createSessionStateHelpers } from "./session-state";

export function createSession(ctx: HostContext) {
  const { entries, fns, initialProfileIdForSession, options, rendererOwnersByViewId, tabsByWebContentsId, viewIdsBySessionId } = ctx;

  const {
    agentPartitionFor,
    setAgentBrowserActivity,
    liveViewportOverrides,
    agentViewportOverrides,
    applyBrowserViewBounds,
    pushDevToolsState,
    profileStateForSession,
    pushProfileState,
    publicFindState,
    pushFindState,
    resetFindState,
    destroyDevTools,
  } = createSessionStateHelpers(ctx);

  const createTab = createTabFactory(ctx, {
    applyBrowserViewBounds,
    agentPartitionFor,
    pushFindState,
    resetFindState,
    pushDevToolsState,
    destroyDevTools,
  });

  const ensureSession = (
    sessionId: string,
    rendererId?: number,
    createInitialTab = true,
    agentInitiated = false,
  ): BrowserSessionEntry => {
    const existingViewId = viewIdsBySessionId.get(sessionId);
    const viewId = existingViewId ?? `${rendererId ?? 0}:${sessionId}`;
    let session = entries.get(viewId);
    if (!session) {
      const profileId = initialProfileIdForSession(sessionId);
      session = {
        sessionId,
        viewId,
        profileId,
        // Temporary workers still use a persist: partition: Electron's built-in
        // PDF viewer does not reliably render in in-memory partitions, while Tallpine
        // clears this session-scoped partition when the browser session ends.
        profilePartition: profileId
          ? browserProfilePartition(profileId)
          : temporaryBrowserPartition(),
        tabs: new Map(),
        activeTabId: "",
        activationRevision: 0,
        nextTabNumber: 1,
        livePreviewPending: true,
        bounds: OFFSCREEN_BOUNDS,
        rendererBounds: OFFSCREEN_BOUNDS,
        zoomFactor: 1,
        visible: false,
        layoutRevision: 0,
        agentBrowserCommands: 0,
        browserOperations: 0,
        idleWaiters: [],
        profileSwitching: false,
        profileReplacing: false,
        profileSwitchTargetId: null,
        nativeOperationQueue: Promise.resolve(),
        devtoolsPlacement: DEFAULT_NATIVE_DEVTOOLS_PLACEMENT,
        signals: { entries: [] },
      };
      entries.set(viewId, session);
      viewIdsBySessionId.set(sessionId, viewId);
      try {
        // A session an agent command starts opens straight into the agent's partition.
        if (createInitialTab) createTab(session, true, false, undefined, undefined, agentInitiated);
      } catch (error) {
        for (const entry of session.tabs.values()) {
          tabsByWebContentsId.delete(entry.view.webContents.id);
          disposeNetworkCapture(entry, "session-startup-failed");
          if (!options.mainWindow.isDestroyed?.()) fns.destroyTabView(entry);
        }
        session.tabs.clear();
        entries.delete(viewId);
        viewIdsBySessionId.delete(sessionId);
        throw error;
      }
      // A fresh native session starts on the provider's first target. Recording
      // that invariant avoids an unnecessary tab command before the first action;
      // later human selections and popups explicitly invalidate it.
      session.nativeActiveTabId = session.activeTabId;
      pushProfileState(session);
      fns.registerBrowserSignalWatcher(session);
    }
    if (rendererId !== undefined) {
      const owners = rendererOwnersByViewId.get(viewId) ?? new Set<number>();
      owners.add(rendererId);
      rendererOwnersByViewId.set(viewId, owners);
    }
    return session;
  };

  const ensureSessionReady = async (
    sessionId: string,
    rendererId?: number,
    isUnavailable?: () => boolean,
    createInitialTab = true,
    agentInitiated = false,
  ): Promise<BrowserSessionEntry> => {
    const store = options.browserProfileStore;
    if (store) {
      for (;;) {
        const profileId = initialProfileIdForSession(sessionId);
        if (!profileId || !store.isProfileOperationInProgress(profileId))
          break;
        await store.waitForProfileOperation(profileId);
      }
    }
    if (isUnavailable?.()) {
      throw browserError(
        "BROWSER_TARGET_UNAVAILABLE",
        "Browser renderer is no longer available",
      );
    }
    return ensureSession(sessionId, rendererId, createInitialTab, agentInitiated);
  };

  return {
    applyBrowserViewBounds,
    createTab,
    destroyDevTools,
    ensureSessionReady,
    liveViewportOverrides,
    agentViewportOverrides,
    profileStateForSession,
    publicFindState,
    pushDevToolsState,
    pushFindState,
    pushProfileState,
    resetFindState,
    setAgentBrowserActivity,
  };
}
