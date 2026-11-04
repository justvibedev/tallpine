import type { BrowserEntry, BrowserNavState, BrowserSessionEntry, BrowserTabState, BrowserTabsState } from "./types";
import { sanitizeBrowserTitle, sanitizeBrowserURL, sanitizeURLsInText } from "./url";
import { browserError } from "./args";

// agent-browser-runtime.ts's parseAgentBrowserJSON throws this exact code for
// any command the automation runtime's own process reports success:false for
// (e.g. its internal tab registry not recognizing a tabId session.tabs still
// has) — distinguishing it from an unrelated failure (the binary missing,
// the runtime already disposed, a malformed response) that a close-tab
// fallback should NOT quietly swallow.
export function isAgentBrowserCommandFailure(error: unknown): boolean {
  return Boolean(
    error &&
    typeof error === "object" &&
    "code" in error &&
    error.code === "AGENT_BROWSER_COMMAND_FAILED",
  );
}

export function isAgentBrowserTimeout(error: unknown): boolean {
  return Boolean(
    error &&
      typeof error === "object" &&
      "code" in error &&
      error.code === "AGENT_BROWSER_TIMEOUT",
  );
}

export function isBrowserTargetMismatch(error: unknown): boolean {
  return Boolean(
    error &&
    typeof error === "object" &&
    "code" in error &&
    error.code === "BROWSER_TARGET_MISMATCH",
  );
}

export function isAgentBrowserWaitTimeout(error: unknown): boolean {
  return Boolean(
    error &&
    typeof error === "object" &&
    "code" in error &&
    error.code === "AGENT_BROWSER_WAIT_TIMEOUT",
  );
}

export function isStaleReferenceError(error: unknown): boolean {
  return Boolean(
    error &&
    typeof error === "object" &&
    "code" in error &&
    error.code === "STALE_REFERENCE",
  );
}

export function tabResult(
  entry: BrowserEntry,
  active: boolean,
): BrowserTabState & { untrustedExternalContent: true } {
  const { url, title } = entry.driver().page();
  return {
    id: entry.tabId,
    url,
    title,
    active,
    ...(entry.favicon ? { favicon: entry.favicon } : {}),
    ...(entry.agentTab !== undefined ? { remote: { tabId: entry.agentTab } } : {}),
    untrustedExternalContent: true,
  };
}

// Renderer IPC deliberately keeps the real URL for the address bar and tab
// navigation. Agent/daemon responses use this separate projection because they
// are retained in tool output and transcripts.
export function agentTabResult(
  entry: BrowserEntry,
  active: boolean,
): BrowserTabState & { untrustedExternalContent: true } {
  const tab = tabResult(entry, active);
  return {
    ...tab,
    url: sanitizeBrowserURL(tab.url),
    title: sanitizeBrowserTitle(tab.title),
  };
}

export function agentTabsResult(
  session: BrowserSessionEntry,
): BrowserTabsState & { untrustedExternalContent: true } {
  return {
    viewId: session.viewId,
    activeTabId: session.activeTabId,
    tabs: [...session.tabs.values()].map((entry) =>
      agentTabResult(entry, entry.tabId === session.activeTabId),
    ),
    untrustedExternalContent: true,
  };
}

export function agentActionTarget(entry: BrowserEntry): {
  tabId: string;
  url: string;
  origin: string;
} {
  const rawURL = entry.view.webContents.getURL() || "about:blank";
  let origin = "";
  try {
    const parsed = new URL(rawURL);
    if (parsed.protocol === "http:" || parsed.protocol === "https:")
      origin = parsed.origin;
  } catch {
    // The sanitized URL still provides a bounded identity for transient or
    // malformed navigation state; origin is intentionally absent in that case.
  }
  return { tabId: entry.tabId, url: sanitizeBrowserURL(rawURL), origin };
}

type SnapshotDelta =
  | { kind: "full"; revision: number; text: string; refs: unknown }
  | { kind: "unchanged"; revision: number; baseRevision: number }
  | {
      kind: "delta";
      revision: number;
      baseRevision: number;
      changes: unknown[];
      treeChange: unknown;
    };

// Curved pointer movement needs painted frames: an offscreen panel would take
// the command to the request deadline instead of failing with something the
// agent can act on.
export function assertPanelPainted(session: BrowserSessionEntry): void {
  if (session.visible) return;
  throw browserError(
    "BROWSER_PANEL_HIDDEN",
    "Human pointer movement needs the Browser panel visible on screen. Ask the user to open it, or retry without --human.",
  );
}

export function parseSnapshotDelta(value: unknown): SnapshotDelta {
  const invalid = () =>
    browserError(
      "BROWSER_AUTOMATION_INVALID_OUTPUT",
      "Browser snapshot delta output was invalid",
    );
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw invalid();
  const snapshot = value as Record<string, unknown>;
  const { kind, revision, baseRevision } = snapshot;
  if (typeof revision !== "number" || !Number.isInteger(revision))
    throw invalid();
  if (kind === "full") {
    if (typeof snapshot.tree !== "string") throw invalid();
    return { kind, revision, text: snapshot.tree, refs: snapshot.refs };
  }
  if (typeof baseRevision !== "number" || !Number.isInteger(baseRevision))
    throw invalid();
  if (kind === "unchanged") return { kind, revision, baseRevision };
  if (kind === "delta" && Array.isArray(snapshot.changes)) {
    return {
      kind,
      revision,
      baseRevision,
      changes: snapshot.changes,
      treeChange: snapshot.treeChange,
    };
  }
  throw invalid();
}

export function agentNavState(state: BrowserNavState): BrowserNavState {
  return {
    ...state,
    url: sanitizeBrowserURL(state.url),
    title: sanitizeBrowserTitle(state.title),
    ...(state.error ? { error: sanitizeURLsInText(state.error) } : {}),
  };
}

export function agentURLResult(result: unknown): unknown {
  if (!result || typeof result !== "object" || !("url" in result))
    return result;
  return {
    ...result,
    url: sanitizeBrowserURL(String(result.url ?? "")),
  };
}
