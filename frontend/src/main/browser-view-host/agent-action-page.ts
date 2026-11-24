import { getAction } from "./agent-action-neutral";
import { activeEntry } from "./entry-state";
import type { BrowserDevToolsInput } from "./input-types";
import { agentActionTarget } from "./agent-results";
import { applyViewport } from "./agent-action-cdp";
import type { AgentActionEnv } from "./agent-action-env";
import { pageCdpAction } from "./agent-action-page-cdp";
import { needsAgentTab } from "./agent-partition";
import { browserError } from "./args";

export async function pageAction(env: AgentActionEnv) {
  const { ctx, session, action, args, runNative } = env;
  const { agentViewportOverrides, liveViewportOverrides, devtoolsAction } = ctx.fns;
  const entry = activeEntry(session);
  switch (action) {
    case "get":
      return getAction(args, runNative);
    case "wait":
      return runNative(action, args);
    case "frame":
    case "dialog":
      return runNative(action, args);
    case "devtools-open":
    case "devtools-close": {
      const operation = action.slice(
        "devtools-".length,
      ) as BrowserDevToolsInput["operation"];
      return devtoolsAction(session, operation);
    }
    case "viewport": {
      const result = await applyViewport(async () => {
        await entry.ready;
        return entry.view.webContents.debugger;
      }, args);
      if ("reset" in result) {
        agentViewportOverrides.delete(entry);
        liveViewportOverrides.delete(entry);
        // Hand the slot back to the live-stream pin if a parked stream needs it.
        ctx.fns.syncLiveViewport(session, entry);
        return { reset: true, target: agentActionTarget(entry) };
      }
      agentViewportOverrides.add(entry);
      return { ...result, target: agentActionTarget(entry) };
    }
    case "back":
    case "forward":
    case "reload":
    case "eval":
    case "upload":
      return pageCdpAction(action, args, {
        tab: async () => {
          await entry.ready;
          // eval reads whatever the page holds: never in the person's own tab
          // (their logins) unless they granted the agent those logins, nor in
          // a viewer of a remote agent browser.
          if (action === "eval" && (entry.agentTab !== undefined || needsAgentTab(entry, session))) {
            throw browserError(
              "BROWSER_EVAL_NOT_GRANTED",
              "eval runs only in agent tabs, or in your own tabs after you grant the agent your logins",
            );
          }
          return { tabId: entry.tabId, debug: entry.view.webContents.debugger };
        },
        runNative,
        target: () => agentActionTarget(entry),
      });
  }
  throw new Error(`Unhandled page action: ${action}`);
}
