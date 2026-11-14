import { randomUUID } from "node:crypto";
import { browserError, throwIfAborted } from "./args";
import { normalizeAgentCommandURL } from "./agent-url-policy";
import { activeEntry } from "./entry-state";
import type { BrowserViewHost } from "./types";
import { agentTabResult, agentTabsResult } from "./agent-results";
import { createRunNative } from "./agent-run-native";
import type { HostContext } from "./context";
import type { AgentActionEnv } from "./agent-action-env";
import { actAction } from "./agent-action-act";
import { captureAction } from "./agent-action-capture";
import { inputAction } from "./agent-action-input";
import { pageAction } from "./agent-action-page";
import { openAction, snapshotAction } from "./agent-action-snapshot";
import { tabAction } from "./agent-action-tabs";

const AGENT_TAB_MANAGEMENT_ACTIONS = new Set(["tabs", "tab-new", "tab-select", "tab-close"]);

export function createAgentExecute(ctx: HostContext) {
  const { options, viewIdsBySessionId } = ctx;
  const { destroy, ensureSessionReady, openTab, queueNativeOperation, ensureAgentTab, resolveAgentLogins, setAgentBrowserActivity } = ctx.fns;

  const execute: BrowserViewHost["execute"] = async (sessionId, action, args = {}, signal) => {
      throwIfAborted(signal);
      if (!sessionId.trim())
        throw browserError("INVALID_ARGUMENT", "sessionId is required");
      if (action === "__destroy-session") {
        const viewId = viewIdsBySessionId.get(sessionId);
        await options.agentBrowserRuntime?.closeSession(sessionId);
        if (viewId) destroy(viewId);
        return { destroyed: Boolean(viewId) };
      }
      const session = await ensureSessionReady(sessionId, undefined, undefined, true, true);
      if (session.profileSwitching) {
        throw browserError(
          "BROWSER_PROFILE_SWITCHING",
          "Browser profile switching is in progress",
        );
      }
      if (!session.tabs.size && action === "tabs") return agentTabsResult(session);
      const commandId = randomUUID();
      setAgentBrowserActivity(session, action, true, commandId, "started");
      try {
        if (!session.tabs.size) {
          if (action === "tab-new") {
            const url = typeof args.url === "string" && args.url.trim()
              ? normalizeAgentCommandURL(args.url)
              : undefined;
            // Same as openUserTab's first-tab path, but agent-driven: the agent's
            // URL must not load in the person's profile partition.
            await queueNativeOperation(session, async () => {
              await openTab(session, url, true, "opened", false, true);
              session.nativeActiveTabId = undefined;
              session.snapshotDeltaBaseline = undefined;
            });
            return agentTabResult(activeEntry(session), true, session);
          }
          await openTab(session, undefined, true, "opened", false, true);
        }
        // The person's own tab may hold their logins: agent commands move to an
        // agent tab (throwaway profile unless granted) instead of driving it.
        if (!AGENT_TAB_MANAGEMENT_ACTIONS.has(action)) resolveAgentLogins(session);
        // runNative re-checks inside the native queue, where the target is chosen.
        if (!AGENT_TAB_MANAGEMENT_ACTIONS.has(action)) await ensureAgentTab(session);
        activeEntry(session);
        const runNative = createRunNative(ctx, sessionId, session, signal);
        const env: AgentActionEnv = { ctx, sessionId, session, signal, action, args, runNative };
        // `await` only where the pre-split code awaited inside this try: the busy flag
        // (setAgentBrowserActivity) must stay set for exactly the same stretch.
        switch (action) {
          case "open":
            return await openAction(env);
          case "snapshot":
            return await snapshotAction(env);
          case "act":
            return await actAction(env);
          case "click":
          case "dblclick":
          case "focus":
          case "hover":
          case "highlight":
          case "scrollintoview":
          case "check":
          case "uncheck":
          case "fill":
          case "type":
          case "press":
          case "drag":
            return inputAction(env);
          case "unhighlight":
            return await inputAction(env);
          case "scroll":
          case "select":
            return inputAction(env);
          case "tabs":
            return agentTabsResult(session);
          case "tab-new":
          case "tab-select":
          case "tab-close":
            return await tabAction(env);
          case "get":
            return await pageAction(env);
          case "wait":
          case "frame":
          case "dialog":
          case "devtools-open":
          case "devtools-close":
            return pageAction(env);
          case "viewport":
            return await pageAction(env);
          case "screenshot":
          case "network-start":
          case "network-status":
          case "network-list":
          case "network-stop":
          case "network-clear":
            return captureAction(env);
          case "console":
          case "errors":
            return await captureAction(env);
          default:
            throw browserError(
              "INVALID_ARGUMENT",
              `Unsupported browser action: ${action}`,
            );
        }
      } finally {
        setAgentBrowserActivity(session, action, false, commandId, "finished");
      }
  };

  return { execute };
}
