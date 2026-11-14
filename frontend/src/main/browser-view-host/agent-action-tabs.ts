import { browserError, stringArg } from "./args";
import { normalizeAgentCommandURL } from "./agent-url-policy";
import { needsAgentTab } from "./agent-partition";
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
      return agentTabResult(activeEntry(session), true, session);
    }
    case "tab-select": {
      const tabId = stringArg(args, "tabId", "TAB_ID_REQUIRED", "tabId is required");
      const wanted = session.tabs.get(tabId);
      // The person's own tab may hold their logins. A temporary session has no
      // logins to grant, so its agent must use its own tab. In a named profile,
      // raise the grant dialog (asked once) and refuse; after a grant the same
      // select attaches to their page.
      if (wanted && needsAgentTab(wanted, session)) {
        if (session.profileId === null) {
          throw browserError(
            "TAB_NOT_AGENT_DRIVABLE",
            `Tab ${tabId} is the person's own in a temporary session and cannot be selected by the agent; use tab-new or open for a tab of your own. tabs shows agentDrivable per tab.`,
          );
        }
        env.ctx.fns.resolveAgentLogins(session);
        throw browserError(
          "AGENT_LOGINS_NOT_GRANTED",
          `Tab ${tabId} is the person's own and holds their logins. Logins must be granted to the agent first (ask the person to allow agent logins; a dialog is raised if not already asked). tabs shows agentDrivable per tab.`,
        );
      }
      await runNative(action, { tabId });
      return agentTabResult(activeEntry(session), true, session);
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
