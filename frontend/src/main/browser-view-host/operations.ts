import { abortableBrowserResult, externalText, throwIfAborted } from "./args";
import { sanitizeBrowserURL } from "./url";
import type { BrowserElectronSession, BrowserSessionEntry, BrowserSignalEntry } from "./types";
import { MAX_BROWSER_SIGNALS, MAX_BROWSER_SIGNAL_BYTES } from "./constants";
import type { HostContext } from "./context";

export function createOperations(ctx: HostContext) {
  const { entries, nativeOperationsInFlight, signalWatchers, tabsByWebContentsId, wakeIfIdle } = ctx;
  const { restoreDiscardedTab, syncTabLifecycle } = ctx.fns;

  const queueNativeOperation = <T>(
    session: BrowserSessionEntry,
    operation: () => Promise<T>,
    signal?: AbortSignal,
  ): Promise<T> => {
    const run = async () => {
      throwIfAborted(signal);
      // agent-browser starts from the active tab, and a tab without a renderer
      // never answers it: a discarded active tab reloads before the work
      // starts, and no tab is discarded while it runs. A tab it selects comes
      // back through activation.
      nativeOperationsInFlight.set(session, (nativeOperationsInFlight.get(session) ?? 0) + 1);
      try {
        const active = session.tabs.get(session.activeTabId);
        if (active?.discarded) {
          restoreDiscardedTab(active);
          await active.ready.catch(() => undefined);
        }
        throwIfAborted(signal);
        return await operation();
      } finally {
        nativeOperationsInFlight.set(session, Math.max(0, (nativeOperationsInFlight.get(session) ?? 1) - 1));
      }
    };
    const queued = session.nativeOperationQueue.then(run, run);
    // A failed operation is returned to its caller, but must not permanently
    // poison the session queue. Keep the tail chained to the actual operation,
    // even when an aborted caller stops awaiting it, so later native work can
    // never overtake an operation already in progress.
    session.nativeOperationQueue = queued.then(
      () => undefined,
      () => undefined,
    );
    return abortableBrowserResult(queued, signal);
  };

  const withBrowserOperation = async <T>(
    session: BrowserSessionEntry,
    operation: () => Promise<T>,
  ): Promise<T> => {
    session.browserOperations += 1;
    syncTabLifecycle(session);
    try {
      return await operation();
    } finally {
      session.browserOperations = Math.max(0, session.browserOperations - 1);
      syncTabLifecycle(session);
      wakeIfIdle(session);
    }
  };

  // Electron reuses one Session for every worker attached to the same named
  // profile. Register one watcher on that shared object, then route each event
  // back to the owning Tallpine worker by webContentsId. Never broadcast an event to
  // every worker sharing the profile.
  const registerBrowserSignalWatcher = (session: BrowserSessionEntry): void => {
    const firstTab = session.tabs.values().next().value;
    const electronSession = firstTab?.view.webContents.session;
    // Optional in BrowserWebContents (real Electron always provides it; test
    // doubles that don't care about network-failure signals can omit it).
    if (!electronSession?.webRequest) return;
    const existing = signalWatchers.get(electronSession);
    if (existing) {
      existing.viewIds.add(session.viewId);
      return;
    }
    const watcher = {
      viewIds: new Set([session.viewId]),
      webRequest: electronSession.webRequest,
    };
    signalWatchers.set(electronSession, watcher);
    const ownerFor = (
      webContentsId: number | undefined,
    ): BrowserSessionEntry | undefined => {
      if (typeof webContentsId !== "number") return undefined;
      const tab = tabsByWebContentsId.get(webContentsId);
      if (!tab || tab.view.webContents.session !== electronSession)
        return undefined;
      const owner = entries.get(tab.state.viewId);
      if (
        !owner ||
        !watcher.viewIds.has(owner.viewId) ||
        owner.tabs.get(tab.tabId) !== tab
      )
        return undefined;
      return owner;
    };
    const filter = { urls: ["*://*/*"] };
    electronSession.webRequest.onCompleted(filter, (details) => {
      if (details.resourceType !== "xhr" || details.statusCode < 400) return;
      const owner = ownerFor(details.webContentsId);
      if (!owner) return;
      recordBrowserSignal(owner, {
        kind: "network-failure",
        message: `${details.method} ${sanitizeBrowserURL(details.url)} → ${details.statusCode}`,
      });
    });
    electronSession.webRequest.onErrorOccurred(filter, (details) => {
      if (details.resourceType !== "xhr") return;
      const owner = ownerFor(details.webContentsId);
      if (!owner) return;
      recordBrowserSignal(owner, {
        kind: "network-failure",
        message: `${details.method} ${sanitizeBrowserURL(details.url)} failed: ${details.error}`,
      });
    });
  };

  const unregisterBrowserSignalWatcher = (
    session: BrowserSessionEntry,
    closedSession?: BrowserElectronSession,
  ): void => {
    const firstTab = session.tabs.values().next().value;
    const electronSession = closedSession ?? firstTab?.view.webContents.session;
    if (!electronSession) return;
    const watcher = signalWatchers.get(electronSession);
    if (!watcher) return;
    watcher.viewIds.delete(session.viewId);
    if (watcher.viewIds.size > 0) return;
    watcher.webRequest.onCompleted(null);
    watcher.webRequest.onErrorOccurred(null);
    signalWatchers.delete(electronSession);
  };

  const recordBrowserSignal = (
    session: BrowserSessionEntry,
    entry: Omit<BrowserSignalEntry, "timestamp">,
  ): void => {
    const { entries } = session.signals;
    entries.push({
      ...entry,
      message: externalText(entry.message, MAX_BROWSER_SIGNAL_BYTES),
      timestamp: new Date().toISOString(),
    });
    if (entries.length > MAX_BROWSER_SIGNALS)
      entries.splice(0, entries.length - MAX_BROWSER_SIGNALS);
  };

  return {
    queueNativeOperation,
    recordBrowserSignal,
    registerBrowserSignalWatcher,
    unregisterBrowserSignalWatcher,
    withBrowserOperation,
  };
}
