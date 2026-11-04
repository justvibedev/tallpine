import type { HostContext } from "./context";
import { type BrowserProfileViewState } from "../../shared/browser-profiles";
import type { BrowserAgentActivityState, BrowserDevToolsState, BrowserEntry, BrowserFindState, BrowserRect, BrowserSessionEntry, BrowserViewLike } from "./types";
import { BROWSER_VIEW_BORDER_RADIUS } from "./constants";
import { chooseAgentPartition } from "./agent-partition";

// Per-session push/state helpers shared by the browser host's session code.
export function createSessionStateHelpers(ctx: HostContext) {
  const { options, shellWebContents, wakeIfIdle } = ctx;
  const { syncTabLifecycle } = ctx.fns;

  const agentPartitionFor = (session: BrowserSessionEntry): string => {
    const chosen = chooseAgentPartition({
      sessionPartition: session.profilePartition,
      agentPartition: session.agentPartition,
      granted:
        session.profileId !== null &&
        session.agentLoginsGrantedProfileId === session.profileId,
    });
    if (chosen.created) session.agentPartition = chosen.partition;
    return chosen.partition;
  };

  const setAgentBrowserActivity = (
    session: BrowserSessionEntry,
    action: string,
    active: boolean,
    commandId?: string,
    phase?: BrowserAgentActivityState["phase"],
  ): void => {
    session.agentBrowserCommands = Math.max(
      0,
      session.agentBrowserCommands + (active ? 1 : -1),
    );
    syncTabLifecycle(session);
    if (!active) wakeIfIdle(session);
    shellWebContents.send("browser:agentActivity", {
      viewId: session.viewId,
      active: session.agentBrowserCommands > 0,
      action,
      ...(phase ? { phase } : {}),
      ...(commandId ? { commandId } : {}),
    } satisfies BrowserAgentActivityState);
  };
  const liveViewportOverrides = new WeakMap<BrowserEntry, string>();
  // Entries holding an agent `viewport` emulation; the live-stream pin yields to it.
  const agentViewportOverrides = new WeakSet<BrowserEntry>();
  const applyBrowserViewBounds = (
    view: BrowserViewLike,
    bounds: BrowserRect,
    visible?: boolean,
  ): void => {
    view.setBounds(bounds);
    if (visible !== undefined) view.setVisible?.(visible);
    view.setBorderRadius?.(BROWSER_VIEW_BORDER_RADIUS);
  };
  const pushDevToolsState = (
    session: BrowserSessionEntry,
  ): BrowserDevToolsState => {
    syncTabLifecycle(session);
    const state: BrowserDevToolsState = {
      viewId: session.viewId,
      open: Boolean(session.devtools),
      activeTabId: session.activeTabId,
      placement: session.devtools?.placement ?? session.devtoolsPlacement,
    };
    shellWebContents.send("browser:devtoolsState", state);
    return state;
  };
  const profileStateForSession = (
    session: BrowserSessionEntry,
  ): BrowserProfileViewState => {
    const profile = session.profileId
      ? options.browserProfileStore?.getProfile(session.profileId)
      : undefined;
    return {
      viewId: session.viewId,
      profileId: profile ? session.profileId : null,
      ...(profile ? { profileName: profile.name } : {}),
      temporary: !profile,
    };
  };
  const pushProfileState = (
    session: BrowserSessionEntry,
  ): BrowserProfileViewState => {
    const state = profileStateForSession(session);
    shellWebContents.send("browser:profileState", state);
    return state;
  };
  const publicFindState = (entry: BrowserEntry): BrowserFindState => {
    const { requestId: _requestId, ...state } = entry.findState;
    return state;
  };
  const pushFindState = (entry: BrowserEntry): BrowserFindState => {
    const state = publicFindState(entry);
    shellWebContents.send("browser:findState", state);
    return state;
  };
  const resetFindState = (
    entry: BrowserEntry,
    notify = true,
  ): BrowserFindState => {
    entry.findState = {
      viewId: entry.state.viewId,
      tabId: entry.tabId,
      query: "",
      activeMatchOrdinal: 0,
      matches: 0,
      finalUpdate: true,
    };
    return notify ? pushFindState(entry) : publicFindState(entry);
  };

  const destroyDevTools = (session: BrowserSessionEntry): void => {
    const devtools = session.devtools;
    if (!devtools) return;
    session.devtools = undefined;
    try {
      devtools.contents.closeDevTools?.();
    } catch {
      // Chromium may already have torn down the native DevTools surface.
    }
    pushDevToolsState(session);
  };

  return { agentPartitionFor, setAgentBrowserActivity, liveViewportOverrides, agentViewportOverrides, applyBrowserViewBounds, pushDevToolsState, profileStateForSession, pushProfileState, publicFindState, pushFindState, resetFindState, destroyDevTools };
}
