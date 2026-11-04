import { browserError } from "./args";
import type { BrowserSessionEntry } from "./types";
import { isAgentBrowserCommandFailure } from "./agent-results";
import type { HostContext } from "./context";

export function createEnsureNativeActiveTab(ctx: HostContext) {
  const { fns, options } = ctx;

  const ensureNativeActiveTab = async (
    session: BrowserSessionEntry,
    signal?: AbortSignal,
    // Opening, selecting or closing a tab by its ID: no page in front needed.
    tabCommand = false,
  ): Promise<void> => {
    if (!options.agentBrowserRuntime) return;
    while (session.nativeActiveTabId !== session.activeTabId) {
      const tabId = session.activeTabId;
      const entry = session.tabs.get(tabId);
      if (!entry)
        throw browserError(
          "BROWSER_TARGET_UNAVAILABLE",
          "Active browser tab is unavailable",
        );
      // agent-browser never sees agent tabs (agentBrowserTargets): with one in
      // front it stays on the person's tab it was on. Only tab commands run
      // then; anything else would act on a page the person is not looking at.
      if (entry.agentTab !== undefined) {
        if (tabCommand) return;
        throw browserError(
          "BROWSER_TARGET_MISMATCH",
          `Browser automation could not target Tallpine tab ${tabId}`,
        );
      }
      await entry.ready;
      // The human-facing BrowserView state is authoritative. Selecting through
      // agent-browser updates its independent active_page_index before another
      // native command is allowed to run.
      try {
        await options.agentBrowserRuntime.runAction(
          session.sessionId,
          "tab-select",
          { tabId },
          fns.agentBrowserTargets(session),
          signal,
        );
      } catch (error) {
        // The runtime keeps its own tab registry (a real, separate process —
        // not derived live from session.tabs), which can drift after enough
        // tab churn: observed live as "Tab tX not found; run `agent-browser
        // tab` to list open tabs" for a tabId session.tabs still has. Retrying
        // the exact same tab-select would fail identically forever, and every
        // native browser operation for this session (select, close, click,
        // snapshot, ...) routes through this loop — so a bare retry
        // permanently wedges the whole session, not just this one call.
        // A fresh command reconciles Tallpine's targets and gives the native daemon
        // another chance to consume discovery events before one bounded retry.
        if (!isAgentBrowserCommandFailure(error)) throw error;
        try {
          await options.agentBrowserRuntime.runAction(
            session.sessionId,
            "tabs",
            {},
            fns.agentBrowserTargets(session),
            signal,
          );
          await options.agentBrowserRuntime.runAction(
            session.sessionId,
            "tab-select",
            { tabId },
            fns.agentBrowserTargets(session),
            signal,
          );
        } catch (resyncError) {
          if (!isAgentBrowserCommandFailure(resyncError)) throw resyncError;
          // A successful Tallpine command must never mean "agent-browser probably
          // stayed on some other authenticated tab". Do not expose the native
          // process's recovery text across the daemon boundary; callers get one
          // stable Tallpine error and the next command may make a fresh bounded attempt.
          throw browserError(
            "BROWSER_TARGET_MISMATCH",
            `Browser automation could not target Tallpine tab ${tabId}`,
          );
        }
      }
      session.nativeActiveTabId = tabId;
    }
  };

  return ensureNativeActiveTab;
}
