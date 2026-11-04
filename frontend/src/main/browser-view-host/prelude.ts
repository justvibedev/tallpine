import type { Session } from "electron";
import type { BrowserProfileId } from "../../shared/browser-profiles";
import { DEFAULT_ENTRY_READY_TIMEOUT_MS } from "./load-entry";
import type { BrowserElectronSession, BrowserEntry, BrowserSessionEntry, BrowserViewHostOptions } from "./types";

export function createHostPrelude(options: BrowserViewHostOptions) {
  // Mutable cross-slice values: `let`s in the original closure.
  const hostState = {
    disposePromise: null as Promise<void> | null,
  // viewId of the panel that most recently held native focus; cleared when the
  // native surface is hidden or destroyed (menu Edit/DevTools targeting).
    lastFocusedViewId: null as string | null,
  // Separate from native focus: the address bar and tab strip live in the shell
  // renderer, but browser shortcuts must continue to target their panel. Do NOT
  // clear this when the native page is merely hidden (blank tab / measure blip) —
  // that is exactly when ⌘T/⌘W must keep creating/closing browser tabs instead
  // of falling through to new/close terminal.
    lastUsedViewId: null as string | null,
    tabSweepTimer: undefined as ReturnType<typeof setTimeout> | undefined,
    tabSweepAt: Number.POSITIVE_INFINITY,
  };
  const entryReadyTimeoutMs =
    options.entryReadyTimeoutMs ?? DEFAULT_ENTRY_READY_TIMEOUT_MS;
  const entries = new Map<string, BrowserSessionEntry>();
  const historyFaviconCache = new WeakMap<
    Session,
    Map<string, Promise<string | undefined>>
  >();
  const signalWatchers = new Map<
    BrowserElectronSession,
    { viewIds: Set<string>; webRequest: BrowserElectronSession["webRequest"] }
  >();
  const shellWebContents =
    options.shellWebContents ?? options.mainWindow.webContents;
  if (!shellWebContents)
    throw new Error("Browser view host requires shell WebContents");
  const viewIdsBySessionId = new Map<string, string>();
  const rendererOwnersByViewId = new Map<string, Set<number>>();
  const tabsByWebContentsId = new Map<number, BrowserEntry>();
  const pendingTemporaryPartitionClears = new Set<Promise<void>>();
  // Workers whose human explicitly picked the temporary profile this run.
  const explicitTemporarySessionIds = new Set<string>();

  // A worker without a durable binding starts in the first configured profile;
  // temporary is used only when no profile exists or the human picked it.
  const initialProfileIdForSession = (
    sessionId: string,
  ): BrowserProfileId | null => {
    const store = options.browserProfileStore;
    if (!store || explicitTemporarySessionIds.has(sessionId)) return null;
    const sessionProfileId = store.getSessionProfileId(sessionId);
    if (sessionProfileId === null) return null;
    if (sessionProfileId && store.getProfile(sessionProfileId))
      return sessionProfileId;
    const defaultProfileId = store.getDefaultProfileId();
    if (defaultProfileId && store.getProfile(defaultProfileId))
      return defaultProfileId;
    return null;
  };
  const ipcDisposers: Array<() => void> = [];

  const forgetNativeFocus = (viewId: string): void => {
    if (hostState.lastFocusedViewId === viewId) hostState.lastFocusedViewId = null;
  };
  const forgetBrowserShortcutTarget = (viewId: string): void => {
    if (hostState.lastUsedViewId === viewId) hostState.lastUsedViewId = null;
  };
  const isBrowserIdle = (session: BrowserSessionEntry): boolean =>
    session.agentBrowserCommands === 0 && session.browserOperations === 0;
  const wakeIfIdle = (session: BrowserSessionEntry): void => {
    if (isBrowserIdle(session))
      session.idleWaiters.splice(0).forEach((wake) => wake());
  };
  const waitForBrowserIdle = (
    session: BrowserSessionEntry,
    timeoutMs: number,
  ): Promise<void> =>
    isBrowserIdle(session)
      ? Promise.resolve()
      : new Promise((resolve) => {
          const timer = setTimeout(resolve, timeoutMs);
          session.idleWaiters.push(() => {
            clearTimeout(timer);
            resolve();
          });
        });
  const settlesWithin = (
    promise: Promise<unknown>,
    timeoutMs: number,
  ): Promise<boolean> =>
    new Promise((resolve) => {
      const timer = setTimeout(() => resolve(false), Math.max(0, timeoutMs));
      const settle = () => {
        clearTimeout(timer);
        resolve(true);
      };
      void promise.then(settle, settle);
    });
  const nativeOperationsInFlight = new WeakMap<BrowserSessionEntry, number>();

  return {
    hostState,
    entries,
    entryReadyTimeoutMs,
    explicitTemporarySessionIds,
    forgetBrowserShortcutTarget,
    forgetNativeFocus,
    historyFaviconCache,
    initialProfileIdForSession,
    ipcDisposers,
    isBrowserIdle,
    nativeOperationsInFlight,
    pendingTemporaryPartitionClears,
    rendererOwnersByViewId,
    settlesWithin,
    shellWebContents,
    signalWatchers,
    tabsByWebContentsId,
    viewIdsBySessionId,
    waitForBrowserIdle,
    wakeIfIdle,
  };
}
