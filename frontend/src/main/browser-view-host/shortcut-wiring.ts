import { assertAgentNavigationURL } from "./agent-url-policy";
import type { WebContents } from "electron";
import type { AgentBrowserTarget, AgentBrowserTargetProvider } from "../agent-browser-cdp-bridge";
import { browserError } from "./args";
import { activeEntry, isBlankBrowserEntry, shellContents } from "./entry-state";
import type { BrowserEntry, BrowserSessionEntry } from "./types";
import { browserShortcutAction, isHardReloadShortcut } from "./shortcuts";
import type { HostContext } from "./context";

export function createShortcutWiring(ctx: HostContext) {
  const { entries, fns, hostState, options, shellWebContents } = ctx;
  const { activateTab, closeTab, closeUserTab, focusFind, focusLocation, openTab, openUserTab, queueNativeOperation, reopenClosedTab } = ctx.fns;

  function attachBrowserShortcuts(
    contents: Pick<WebContents, "on">,
    getSession: () => BrowserSessionEntry | undefined,
    isNativePage: boolean,
  ): void {
    contents.on("before-input-event", (event, input) => {
      if (input.type !== "keyDown" || options.isKeybindingRecording?.()) return;
      const action = browserShortcutAction(input, Boolean(options.isMac));
      if (!action) {
        // Cmd+Shift+R (Ctrl+Shift+R) in an agent tab reloads its viewer
        // itself, not the agent's page: the way back for a viewer that is
        // stuck (an error page after the agent browser restarted). Anywhere
        // else the key stays the app menu's.
        const session =
          isNativePage && isHardReloadShortcut(input, Boolean(options.isMac))
            ? getSession()
            : undefined;
        const entry = session?.tabs.get(session.activeTabId);
        if (entry?.agentTab === undefined) return;
        event.preventDefault();
        if (!input.isAutoRepeat) entry.view.webContents.reload();
        return;
      }
      const session = getSession();
      if (!session) return;
      // Cmd+F typed in an agent tab is its viewer's: its find searches the
      // agent's page, where Tallpine's would search the viewer's picture of it.
      if (action === "find" && isNativePage && session.tabs.get(session.activeTabId)?.agentTab !== undefined) return;
      event.preventDefault();
      // Consume repeats without re-firing: a held ⌘W must not fall through
      // to the menu's Close item and kill the app window.
      if (input.isAutoRepeat) return;
      hostState.lastUsedViewId = session.viewId;
      if (action === "focus-location") {
        focusLocation(session);
        return;
      }
      if (!session.tabs.size && !["new-tab", "reopen-tab"].includes(action)) return;
      if (action === "find") {
        focusFind(session);
        return;
      }
      if (action === "reload") {
        // An agent tab reloads the agent's page, as its toolbar Reload does.
        activeEntry(session).driver().nav("reload");
        return;
      }
      if (action === "back" || action === "forward") {
        fns.invokeNav(session.viewId, action, true);
        return;
      }
      if (action === "new-tab") {
        void openUserTab(session)
          .then(() => {
            // Blank tabs have no page to focus; omnibox is correct. Keep the
            // browser shortcut target so a second ⌘T cannot open a terminal.
            hostState.lastUsedViewId = session.viewId;
            focusLocation(session);
          })
          .catch(() => undefined);
        return;
      }
      if (action === "reopen-tab") {
        void queueNativeOperation(session, async () =>
          reopenClosedTab(session),
        ).catch(() => undefined);
        return;
      }
      const closingTabId = session.activeTabId;
      if (session.tabs.size === 1) {
        // Focus the shell first so a fast second ⌘W cannot land in the hidden
        // page and fall through to the menu's Close item.
        const shell = shellContents(options);
        shell.focus();
        void closeUserTab(session, closingTabId)
          .catch(() => undefined);
        return;
      }
      if (isNativePage) {
        // The focused view is about to be destroyed asynchronously. Move OS
        // focus to its replacement synchronously — making it visible first
        // so the focus sticks — or a fast second ⌘W lands in focus limbo
        // and hits the menu's Close item, killing the app window.
        const replacementId = [...session.tabs.keys()]
          .filter((id) => id !== closingTabId)
          .at(-1);
        const replacement = replacementId
          ? session.tabs.get(replacementId)
          : undefined;
        if (replacement) {
          hostState.lastUsedViewId = session.viewId;
          hostState.lastFocusedViewId = session.viewId;
          fns.applySessionBounds(session, replacement);
          if (isBlankBrowserEntry(replacement)) focusLocation(session);
          else replacement.view.webContents.focus();
        }
      }
      void closeUserTab(session, closingTabId).catch(() => undefined);
    });
  }

  if (typeof shellWebContents.on === "function") {
    attachBrowserShortcuts(
      shellWebContents,
      () => {
        if (hostState.lastUsedViewId === null) return undefined;
        return entries.get(hostState.lastUsedViewId);
      },
      false,
    );
  }

  // The runtime binds the first provider to its CDP bridge for the session's
  // life, so who a create is for is read per call from session.nativeCommandOrigin
  // (set around agent commands by runNative; the queue serializes commands). The
  // person's own tab-new goes through the same runtime and keeps the profile and
  // today's URL rules.
  function agentBrowserTargets(
    session: BrowserSessionEntry,
  ): AgentBrowserTargetProvider {
    const target = (entry: BrowserEntry): AgentBrowserTarget => ({
      id: entry.tabId,
      url: entry.view.webContents.getURL() || "about:blank",
      title: entry.view.webContents.getTitle(),
      debugger: entry.view.webContents.debugger,
    });
    return {
      // The agent's own browser is not local automation's to drive.
      listTargets: () =>
        [...session.tabs.values()]
          .filter((entry) => entry.agentTab === undefined)
          .map(target),
      createTarget: async (url) => {
        const agentOriginated = session.nativeCommandOrigin === "agent";
        if (agentOriginated && url !== "about:blank") assertAgentNavigationURL(url);
        return target(
          await openTab(
            session,
            url === "about:blank" ? undefined : url,
            true,
            "opened",
            false,
            agentOriginated,
          ),
        );
      },
      activateTarget: async (targetId) => {
        const entry = session.tabs.get(targetId);
        if (!entry)
          throw browserError(
            "TAB_NOT_FOUND",
            `Browser tab ${targetId} does not exist`,
          );
        await entry.ready;
        activateTab(session, targetId);
      },
      closeTarget: (targetId) => {
        closeTab(session, targetId);
      },
    };
  }

  return {
    agentBrowserTargets,
    attachBrowserShortcuts,
  };
}
