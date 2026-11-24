import { browserError, throwIfAborted } from "./args";
import type { HostContext } from "./context";
import type { BrowserViewHost } from "./types";

/**
 * The in-app browser's side of a handoff (B858): the session's front tab,
 * which is the one the agent drives and the one the panel shows, and its URL
 * for --until-url. The renderer opens the Browser panel on the banner.
 */
export function createHandoffPage(ctx: HostContext): BrowserViewHost["handoffPage"] {
  return async (sessionId, signal) => {
    throwIfAborted(signal);
    const session = await ctx.fns.ensureSessionReady(sessionId, undefined, undefined, true, true);
    if (!session.tabs.size)
      throw browserError("INVALID_ARGUMENT", "Open the page the person should use before handing off");
    return {
      url: () => {
        const contents = session.tabs.get(session.activeTabId)?.view.webContents;
        return contents && !contents.isDestroyed?.() ? contents.getURL() : undefined;
      },
    };
  };
}
