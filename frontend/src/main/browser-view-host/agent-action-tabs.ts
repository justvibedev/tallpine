import { browserError, stringArg } from "./args";
import { normalizeAgentCommandURL } from "./agent-url-policy";
import { activeEntry } from "./entry-state";
import { agentTabResult, agentTabsResult } from "./agent-results";
import type { AgentActionEnv } from "./agent-action-env";

export async function tabAction(env: AgentActionEnv) {
  const { session, signal, action, args, runNative } = env;
  switch (action) {
    case "tab-new": {
      const url =
        typeof args.url === "string" && args.url.trim()
          ? normalizeAgentCommandURL(args.url)
          : undefined;
      await runNative(action, { url });
      return agentTabResult(activeEntry(session), true);
    }
    case "tab-select": {
      await runNative(action, {
        tabId: stringArg(
          args,
          "tabId",
          "TAB_ID_REQUIRED",
          "tabId is required",
        ),
      });
      return agentTabResult(activeEntry(session), true);
    }
    case "tab-close": {
      const tabId =
        typeof args.tabId === "string" && args.tabId.trim()
          ? args.tabId.trim()
          : session.activeTabId;
      if (session.tabs.size === 1) {
        throw browserError(
          "CANNOT_CLOSE_LAST_TAB",
          "The only browser tab cannot be closed",
        );
      }
      const closing = session.tabs.get(tabId);
      if (!closing)
        throw browserError(
          "TAB_NOT_FOUND",
          `Browser tab ${tabId} does not exist`,
        );
      await closing.driver().close(signal);
      return { closedTabId: tabId, ...agentTabsResult(session) };
    }
  }
  throw new Error(`Unhandled tab action: ${action}`);
}
