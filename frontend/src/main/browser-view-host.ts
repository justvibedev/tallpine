import type { WebContents } from "electron";
import { activeEntry } from "./browser-view-host/entry-state";
import type { BrowserViewHost, BrowserViewHostOptions } from "./browser-view-host/types";
import { createHostPrelude } from "./browser-view-host/prelude";
import { createWiringFns, type HostContext } from "./browser-view-host/context";
import { createTabLifecycle } from "./browser-view-host/tab-lifecycle";
import { createSession } from "./browser-view-host/session";
import { createOperations } from "./browser-view-host/operations";
import { createAgentTabs } from "./browser-view-host/agent-tabs";
import { createUserTabs } from "./browser-view-host/user-tabs";
import { createShortcutWiring } from "./browser-view-host/shortcut-wiring";
import { createDevTools } from "./browser-view-host/devtools";
import { createBoundsNav } from "./browser-view-host/bounds-nav";
import { createDestroyProfile } from "./browser-view-host/destroy-profile";
import { createAnnotation } from "./browser-view-host/annotation";
import { createIpcHandlers } from "./browser-view-host/ipc-handlers";
import { createLiveStream } from "./browser-view-host/live-stream";
import { createAgentExecute } from "./browser-view-host/agent-execute";

export type { BrowserRect, BrowserNavState, BrowserTabState, BrowserTabsState, BrowserFindState, BrowserFindInput, BrowserFindStopInput, BrowserAgentActivityState, BrowserRuntimeState, BrowserDevToolsState, BrowserDevToolsPlacement, BrowserDevToolsInput, BrowserViewHostOptions, BrowserViewHost } from "./browser-view-host/types";
export type { BrowserShortcutAction } from "./browser-view-host/shortcuts";
export { browserShortcutAction, shouldHandleAppShortcutInBrowserContext } from "./browser-view-host/shortcuts";
export { normalizeBrowserURL, isAllowedBrowserURL, sanitizeBrowserURL, sanitizeBrowserTitle } from "./browser-view-host/url";
export { clampBoundsToWindow, scaleBoundsForZoom } from "./browser-view-host/bounds";

export function createBrowserViewHost(
  options: BrowserViewHostOptions,
): BrowserViewHost {
  const prelude = createHostPrelude(options);
  const { fns, seal } = createWiringFns(!options.isPackaged);
  const ctx: HostContext = { options, ...prelude, fns };
  Object.assign(fns, createTabLifecycle(ctx));
  Object.assign(fns, createSession(ctx));
  Object.assign(fns, createOperations(ctx));
  Object.assign(fns, createAgentTabs(ctx));
  Object.assign(fns, createUserTabs(ctx));
  Object.assign(fns, createShortcutWiring(ctx));
  Object.assign(fns, createDevTools(ctx));
  Object.assign(fns, createBoundsNav(ctx));
  Object.assign(fns, createDestroyProfile(ctx));
  Object.assign(fns, createAnnotation(ctx));
  Object.assign(fns, createIpcHandlers(ctx));
  Object.assign(fns, createLiveStream(ctx));
  Object.assign(fns, createAgentExecute(ctx));
  seal();

  const { entries, hostState, ipcDisposers, pendingTemporaryPartitionClears, tabsByWebContentsId } = ctx;
  const { applyBrowserViewBounds, clearProfileData, destroy, detachProfile, devtoolsAction, execute, getProfileState, getProfileSwitchInfo, getProfileUsage, handleRemoteInput, handleRemoteNavigation, handleRemoteTab, isProfileLive, isRendererOwned, openLivePreview, refreshProfileState, startLiveStream, stopAllLiveStreams, stopLiveStream, switchProfile, withBrowserOperation } = fns;

  return {
    execute,
    dispose: () => {
      if (hostState.disposePromise) return hostState.disposePromise;
      hostState.disposePromise = (async () => {
        if (hostState.tabSweepTimer !== undefined) clearTimeout(hostState.tabSweepTimer);
        ipcDisposers.splice(0).forEach((dispose) => dispose());
        options.browserDownloadManager?.dispose();
        for (const viewId of [...entries.keys()]) {
          destroy(viewId);
        }
        if (options.browserHistoryStore)
          await options.browserHistoryStore.drain();
        while (pendingTemporaryPartitionClears.size > 0) {
          await Promise.all([...pendingTemporaryPartitionClears]);
        }
        await options.agentBrowserRuntime?.dispose();
      })();
      return hostState.disposePromise;
    },
    destroy,
    destroyAll: () => {
      for (const viewId of [...entries.keys()]) {
        destroy(viewId);
      }
    },
		startLiveStream,
		stopLiveStream,
		stopAllLiveStreams,
		handleRemoteInput,
		handleRemoteNavigation,
		handleRemoteTab,
		openLivePreview,
    getLastFocusedPanelContents: () => {
      if (hostState.lastFocusedViewId === null) return null;
      const session = entries.get(hostState.lastFocusedViewId);
      if (!session || !session.tabs.size) return null;
      const entry = activeEntry(session);
      // Stored narrowed as BrowserWebContents but is a full WebContents at runtime.
      const contents = entry.view.webContents as unknown as WebContents;
      return contents.isDestroyed() ? null : contents;
    },
    reloadAgentTab: (contents) => {
      const entry = tabsByWebContentsId.get(contents.id);
      if (entry?.agentTab === undefined) return false;
      entry.driver().nav("reload");
      return true;
    },
    toggleDevToolsForLastFocused: async () => {
      if (hostState.lastFocusedViewId === null) return null;
      const session = entries.get(hostState.lastFocusedViewId);
      if (!session) return null;
      return withBrowserOperation(session, () =>
        devtoolsAction(session, "toggle"),
      );
    },
    forgetLastFocusedPanel: () => {
      hostState.lastFocusedViewId = null;
      hostState.lastUsedViewId = null;
    },
    isRendererOwned,
    getProfileState,
    refreshProfileState,
    getProfileSwitchInfo,
    switchProfile,
    getProfileUsage,
    detachProfile,
    isProfileLive,
    clearProfileData,
    isLastUsedBrowser: () =>
      hostState.lastUsedViewId !== null && entries.has(hostState.lastUsedViewId),
    // Reordering the transparent shell above a live page can leave either
    // WebContentsView showing a stale compositor surface on macOS. A one-pixel
    // bounds nudge alone is insufficient: Electron also needs a visibility
    // reset. Hide immediately, then restore visibility together with the
    // bounds on the next tick. A same-turn setVisible(false)+setVisible(true)
    // can coalesce into a no-op (symptom: page stays blank until the overlay
    // closes), which is why the restore must not run synchronously.
    refreshLastFocusedPanelSurface: () => {
      const targetViewId = hostState.lastFocusedViewId ?? hostState.lastUsedViewId;
      if (targetViewId === null) return;
      const session = entries.get(targetViewId);
      if (!session || !session.visible || !session.tabs.size) return;
      const entry = activeEntry(session);
      const bounds = session.bounds;
      if (bounds.width <= 0 || bounds.height <= 0) return;
      entry.view.setVisible?.(false);
      applyBrowserViewBounds(entry.view, {
        ...bounds,
        height: Math.max(1, bounds.height - 1),
      });
      setTimeout(() => {
        const current = entries.get(targetViewId);
        if (!current || !current.visible || !current.tabs.size) return;
        applyBrowserViewBounds(activeEntry(current).view, current.bounds, true);
      }, 0);
    },
  };
}
