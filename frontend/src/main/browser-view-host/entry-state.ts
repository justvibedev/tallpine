import type { WebContents } from "electron";
import { randomUUID } from "node:crypto";
import type { BrowserDevToolsState, BrowserEntry, BrowserNavState, BrowserSessionEntry, BrowserTabsState, BrowserViewHostOptions } from "./types";
import { TEMPORARY_BROWSER_PARTITION_PREFIX } from "./constants";
import { browserError } from "./args";

export function temporaryBrowserPartition(): string {
  return `${TEMPORARY_BROWSER_PARTITION_PREFIX}${randomUUID()}`;
}

export function isTemporaryBrowserPartition(partition: string): boolean {
  return partition.startsWith(TEMPORARY_BROWSER_PARTITION_PREFIX);
}

export function emptyNavState(viewId: string): BrowserNavState {
  return {
    viewId,
    url: "",
    title: "",
    canGoBack: false,
    canGoForward: false,
    isLoading: false,
  };
}

export function emptyTabsState(viewId: string): BrowserTabsState {
  return { viewId, activeTabId: "", tabs: [] };
}

export function emptyDevToolsState(viewId: string): BrowserDevToolsState {
  return { viewId, open: false, activeTabId: "", placement: "undocked" };
}

export function activeEntry(session: BrowserSessionEntry): BrowserEntry {
  const entry = session.tabs.get(session.activeTabId);
  if (!entry)
    throw browserError(
      "BROWSER_TARGET_UNAVAILABLE",
      "Active browser tab is unavailable",
    );
  return entry;
}

export function isBlankBrowserEntry(entry: BrowserEntry): boolean {
  const url = entry.view.webContents.getURL();
  return !url || url === "about:blank";
}

export function shellContents(options: BrowserViewHostOptions): WebContents {
  const contents = options.shellWebContents ?? options.mainWindow.webContents;
  if (!contents)
    throw new Error("Browser view host requires shell WebContents");
  return contents;
}
