import { DEFAULT_TAB_LIFECYCLE_LIMITS, TAB_LIFECYCLE_SWEEP_MS, tabsToDiscard } from "../browser-tab-lifecycle";
import type { BrowserEntry, BrowserSessionEntry } from "./types";
import type { HostContext } from "./context";

export function createTabLifecycle(ctx: HostContext) {
  const { entries, entryReadyTimeoutMs, hostState, nativeOperationsInFlight, options, shellWebContents } = ctx;

  // A tab is in use while someone can see or drive it: the active tab of a
  // session shown in the panel, streamed to a phone, or under an agent command
  // or renderer operation; the DevTools target; an agent tab (its viewer keeps
  // a live stream of the agent's page); a tab capturing network traffic or
  // annotating; and a tab playing sound. Every other tab is hidden and idle.
  // agent-browser drives only the active tab and works with throttled
  // background tabs; waking them all at the start of its work stalled it.
  const tabInUse = (session: BrowserSessionEntry, entry: BrowserEntry): boolean => {
    if (
      session.activeTabId === entry.tabId &&
      (session.visible || session.live || session.agentBrowserCommands > 0 || session.browserOperations > 0)
    ) {
      return true;
    }
    if (session.devtools && (session.devtools.targetTabId === entry.tabId || session.devtools.desiredTabId === entry.tabId)) {
      return true;
    }
    if (entry.agentTab !== undefined || entry.networkCapture || entry.annotationEnabled) return true;
    try {
      return Boolean(entry.view.webContents.isCurrentlyAudible?.());
    } catch {
      return false;
    }
  };
  const tabContentsAlive = (entry: BrowserEntry): boolean => {
    const contents = entry.view.webContents;
    return Boolean(contents) && !contents.isDestroyed?.();
  };
  const setTabThrottled = (entry: BrowserEntry, throttled: boolean): void => {
    if (entry.throttled === throttled || !tabContentsAlive(entry)) return;
    entry.throttled = throttled;
    try {
      entry.view.webContents.setBackgroundThrottling?.(throttled);
    } catch {
      // A closing WebContents keeps whatever it had.
    }
  };
  // Reloads a discarded tab. Its `ready` waits for the reload, so every path
  // that awaits a tab before driving it waits for the page to come back.
  const restoreDiscardedTab = (entry: BrowserEntry): void => {
    entry.discarded = false;
    if (!tabContentsAlive(entry)) return;
    const contents = entry.view.webContents;
    try {
      if (!contents.isCrashed?.()) return;
      entry.restoring = true;
      contents.reload();
    } catch {
      entry.restoring = false;
      return;
    }
    const deadline = Date.now() + entryReadyTimeoutMs;
    entry.ready = new Promise<void>((resolve) => {
      const poll = () => {
        let settled = true;
        try {
          settled = !tabContentsAlive(entry) || (!contents.isLoading() && !contents.isCrashed?.());
        } catch {
          // Treat a WebContents that cannot answer as settled.
        }
        if (settled || Date.now() >= deadline) resolve();
        else setTimeout(poll, 50);
      };
      setTimeout(poll, 0);
    });
  };

  // Tabs are created unthrottled (automation must work in a hidden tab), so
  // this runs whenever what is in use may have changed, and before work that
  // needs a tab awake: it wakes and reloads tabs coming into use, and
  // throttles the rest.
  const syncTabLifecycle = (session: BrowserSessionEntry): void => {
    const now = Date.now();
    for (const entry of session.tabs.values()) {
      if (tabInUse(session, entry)) {
        entry.lastInUseAt = now;
        if (entry.discarded) restoreDiscardedTab(entry);
        setTabThrottled(entry, false);
      } else {
        setTabThrottled(entry, true);
      }
    }
    scheduleTabSweep();
  };
  const tabSharesProcess = (entry: BrowserEntry, pid: number): boolean => {
    try {
      if (shellWebContents.getOSProcessId?.() === pid) return true;
    } catch {
      return true;
    }
    for (const session of entries.values()) {
      for (const other of session.tabs.values()) {
        if (other === entry || other.discarded || !tabContentsAlive(other)) continue;
        try {
          if (other.view.webContents.getOSProcessId?.() === pid) return true;
        } catch {
          // A tab mid-teardown has no process to share.
        }
      }
    }
    return false;
  };
  // Only a loaded person's page that is alone in its renderer process: ending
  // a process shared with another tab (a same-site popup) would take that tab
  // down too.
  const tabDiscardable = (entry: BrowserEntry): boolean => {
    if (entry.agentTab !== undefined || entry.pendingNavigation || !tabContentsAlive(entry)) return false;
    const contents = entry.view.webContents;
    try {
      if (contents.isCrashed?.() || contents.isLoading() || !contents.forcefullyCrashRenderer) return false;
      if (!/^https?:/i.test(contents.getURL())) return false;
      const pid = contents.getOSProcessId?.();
      return Boolean(pid) && !tabSharesProcess(entry, pid as number);
    } catch {
      return false;
    }
  };
  const discardTab = (entry: BrowserEntry): void => {
    entry.discarded = true;
    try {
      entry.view.webContents.forcefullyCrashRenderer?.();
    } catch {
      entry.discarded = false;
    }
  };
  const tabLifecycleLimits = options.tabLifecycleLimits ?? DEFAULT_TAB_LIFECYCLE_LIMITS;
  const sweepTabLifecycle = (): void => {
    const now = Date.now();
    for (const session of entries.values()) {
      if (session.profileSwitching || session.profileReplacing) continue;
      syncTabLifecycle(session);
      if (session.agentBrowserCommands > 0 || session.browserOperations > 0 || (nativeOperationsInFlight.get(session) ?? 0) > 0) {
        continue;
      }
      const tabs = [...session.tabs.values()];
      const snapshots = tabs.map((entry) => {
        const inUse = tabInUse(session, entry);
        return {
          tabId: entry.tabId,
          inUse,
          discarded: Boolean(entry.discarded),
          discardable: !inUse && !entry.discarded && tabDiscardable(entry),
          lastInUseAt: entry.lastInUseAt,
        };
      });
      for (const tabId of tabsToDiscard(snapshots, now, tabLifecycleLimits)) {
        const entry = session.tabs.get(tabId);
        if (entry) discardTab(entry);
      }
    }
  };
  // The sweep runs only while some hidden tab could still be discarded: at the
  // earliest moment one could be, and no more often than the sweep interval.
  const tabSweepIntervalMs = options.tabLifecycleSweepMs ?? TAB_LIFECYCLE_SWEEP_MS;

  function scheduleTabSweep(): void {
    let due = Number.POSITIVE_INFINITY;
    const firstDiscardAfter = Math.min(tabLifecycleLimits.discardAfterMs, tabLifecycleLimits.minHiddenBeforeCapDiscardMs);
    for (const session of entries.values()) {
      for (const entry of session.tabs.values()) {
        if (entry.discarded || !entry.throttled || !entry.view.webContents?.forcefullyCrashRenderer) continue;
        due = Math.min(due, entry.lastInUseAt + firstDiscardAfter);
      }
    }
    if (due === Number.POSITIVE_INFINITY) return;
    due = Math.max(due, Date.now() + tabSweepIntervalMs);
    if (hostState.tabSweepTimer !== undefined && hostState.tabSweepAt <= due) return;
    if (hostState.tabSweepTimer !== undefined) clearTimeout(hostState.tabSweepTimer);
    hostState.tabSweepAt = due;
    hostState.tabSweepTimer = setTimeout(() => {
      hostState.tabSweepTimer = undefined;
      hostState.tabSweepAt = Number.POSITIVE_INFINITY;
      sweepTabLifecycle();
      scheduleTabSweep();
    }, due - Date.now());
    hostState.tabSweepTimer.unref?.();
  }

  return {
    restoreDiscardedTab,
    syncTabLifecycle,
  };
}
