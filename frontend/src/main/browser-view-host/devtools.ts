import { browserError } from "./args";
import { activeEntry, isBlankBrowserEntry } from "./entry-state";
import type { BrowserDevToolsPlacement, BrowserDevToolsState, BrowserSessionEntry, InternalBrowserDevToolsOperation } from "./types";
import type { HostContext } from "./context";

export function createDevTools(ctx: HostContext) {
  const { fns } = ctx;
  const { destroyDevTools, pushDevToolsState } = ctx.fns;

  const retargetDevTools = async (
    session: BrowserSessionEntry,
    tabId = session.activeTabId,
    reveal = false,
  ): Promise<BrowserDevToolsState> => {
    const devtools = session.devtools;
    if (!devtools) return pushDevToolsState(session);
    devtools.desiredTabId = tabId;
    if (reveal) devtools.revealRequested = true;
    const generation = ++devtools.retargetGeneration;
    const retarget = async (): Promise<BrowserDevToolsState> => {
      if (
        session.devtools !== devtools ||
        generation !== devtools.retargetGeneration
      ) {
        return pushDevToolsState(session);
      }
      const entry = session.tabs.get(tabId);
      if (!entry)
        throw browserError(
          "TAB_NOT_FOUND",
          `Browser tab ${tabId} does not exist`,
        );
      await entry.ready;
      if (
        session.devtools !== devtools ||
        generation !== devtools.retargetGeneration
      ) {
        return pushDevToolsState(session);
      }
      const contents = entry.view.webContents;
      if (!contents.openDevTools) {
        throw browserError(
          "BROWSER_DEVTOOLS_UNAVAILABLE",
          "Browser DevTools are unavailable",
        );
      }
      const targetChanged =
        devtools.contents !== contents || devtools.targetTabId !== entry.tabId;
      if (targetChanged) {
        const previousContents = devtools.contents;
        devtools.contents = contents;
        if (devtools.targetTabId || previousContents !== contents) {
          if (previousContents === contents)
            devtools.nativeCloseForReopen = true;
          try {
            previousContents.closeDevTools?.();
          } catch {
            // Chromium may already have closed the previous native surface.
          }
        }
      }
      if (targetChanged || devtools.revealRequested) {
        contents.openDevTools({
          mode: devtools.placement,
          activate: devtools.revealRequested,
        });
      }
      devtools.targetTabId = entry.tabId;
      devtools.revealRequested = false;
      fns.applySessionBounds(session, entry);
      return pushDevToolsState(session);
    };
    const result = devtools.retargetQueue.then(retarget, retarget);
    devtools.retargetQueue = result.then(
      () => undefined,
      () => undefined,
    );
    return result;
  };

  const openDevTools = async (
    session: BrowserSessionEntry,
  ): Promise<BrowserDevToolsState> => {
    if (!session.tabs.size) return pushDevToolsState(session);
    const entry = activeEntry(session);
    if (isBlankBrowserEntry(entry)) {
      if (session.devtools) destroyDevTools(session);
      return pushDevToolsState(session);
    }
    if (!entry.view.webContents.openDevTools) {
      throw browserError(
        "BROWSER_DEVTOOLS_UNAVAILABLE",
        "Browser DevTools are unavailable",
      );
    }
    if (!session.devtools) {
      session.devtools = {
        contents: entry.view.webContents,
        placement: session.devtoolsPlacement,
        targetTabId: "",
        desiredTabId: entry.tabId,
        retargetGeneration: 0,
        retargetQueue: Promise.resolve(),
        revealRequested: false,
      };
    }
    return retargetDevTools(session, entry.tabId, true);
  };

  const devtoolsAction = async (
    session: BrowserSessionEntry,
    operation: InternalBrowserDevToolsOperation,
    placement?: BrowserDevToolsPlacement,
  ): Promise<BrowserDevToolsState> => {
    fns.assertProfileStable(session);
    switch (operation) {
      case "open":
        return openDevTools(session);
      case "toggle":
        if (session.devtools) {
          destroyDevTools(session);
          return pushDevToolsState(session);
        }
        return openDevTools(session);
      case "close":
        destroyDevTools(session);
        return pushDevToolsState(session);
      case "setPlacement":
        if (!placement)
          throw browserError(
            "INVALID_ARGUMENT",
            "DevTools placement is required",
          );
        return setDevToolsPlacement(session, placement);
    }
  };

  const setDevToolsPlacement = (
    session: BrowserSessionEntry,
    placement: BrowserDevToolsPlacement,
  ): BrowserDevToolsState => {
    if (
      !Object.hasOwn(
        { right: true, bottom: true, left: true, undocked: true },
        placement,
      )
    ) {
      throw browserError(
        "INVALID_ARGUMENT",
        "Unsupported browser DevTools placement",
      );
    }
    session.devtoolsPlacement = placement;
    const devtools = session.devtools;
    if (!devtools) return pushDevToolsState(session);
    devtools.placement = placement;
    const contents = activeEntry(session).view.webContents;
    if (!contents.openDevTools) {
      throw browserError(
        "BROWSER_DEVTOOLS_UNAVAILABLE",
        "Browser DevTools are unavailable",
      );
    }
    const previousContents = devtools.contents;
    if (previousContents === contents) devtools.nativeCloseForReopen = true;
    devtools.contents = contents;
    try {
      previousContents.closeDevTools?.();
    } catch {
      // Chromium may already have closed the native surface.
      devtools.nativeCloseForReopen = false;
    }
    devtools.targetTabId = session.activeTabId;
    try {
      contents.openDevTools({
        mode: placement,
        activate: placement === "undocked",
      });
    } catch (error) {
      devtools.nativeCloseForReopen = false;
      throw error;
    }
    return pushDevToolsState(session);
  };

  return {
    devtoolsAction,
    retargetDevTools,
  };
}
