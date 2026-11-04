import { browserError } from "./args";
import { pushTabsState } from "./nav-events";
import { isAllowedBrowserURL, normalizeBrowserURL } from "./url";
import type { BrowserEntry, BrowserSessionEntry } from "./types";
import type { HostContext } from "./context";

export function createOpenTab(ctx: HostContext, resolveAgentLogins: (session: BrowserSessionEntry) => void) {
  const { fns, options } = ctx;
  const { createTab, registerBrowserSignalWatcher, withBrowserOperation } = ctx.fns;

  const openTab = async (
    session: BrowserSessionEntry,
    url: string | undefined,
    activate: boolean,
    reason: "opened" | "popup" = "opened",
    // A popup opened mid-automation (an agent clicked a link that opens a new
    // tab) must sync the automation runtime's own active-target tracking too,
    // the same way the old direct createTab(..., true) call for popups did —
    // otherwise the runtime keeps issuing further actions against the tab the
    // agent was on before the link, not the one it's actually looking at now.
    syncNativeOnActivate = false,
    agentDriven = false,
  ): Promise<BrowserEntry> => {
    fns.assertTabsNotReplacing(session);
    return withBrowserOperation(session, async () => {
      if (agentDriven) resolveAgentLogins(session);
      let normalizedURL: string | undefined;
      if (url) {
        const normalized = normalizeBrowserURL(url);
        if (!isAllowedBrowserURL(normalized.href, options.rendererOrigin)) {
          throw browserError("NAVIGATION_FAILED", "Unsupported browser URL");
        }
        normalizedURL = normalized.href;
      }
      const entry = createTab(session, activate, syncNativeOnActivate, undefined, undefined, agentDriven);
      registerBrowserSignalWatcher(session);
      await entry.ready;
      if (normalizedURL) {
        const navigation = fns.navigateEntry(entry, normalizedURL);
        pushTabsState(options, session, { kind: reason, tabId: entry.tabId });
        // A failed load still yields a real, usable tab, exactly like
        // navigating an *existing* tab to a dead URL — navigateEntry sets
        // `.error` on the nav state (surfaced separately via
        // browser:navState) and resolves normally rather than throwing.
        // pushTabsState above already told the renderer this tab exists, so
        // rejecting afterward corrupted every caller that inferred "tab
        // exists" from "call succeeded": a dead localhost dev server (the
        // overwhelmingly common case for Recently Closed entries — that's
        // what tallpine preview produces) left reopenClosedTab's cap-failure
        // rollback treating the failed load as "nothing happened," which
        // resurrected the entry and restored it, forever, on every retry,
        // while the tab it claimed didn't exist sat open with an error page.
        await navigation;
      } else {
        pushTabsState(options, session, { kind: reason, tabId: entry.tabId });
      }
      return entry;
    });
  };

  return openTab;
}
