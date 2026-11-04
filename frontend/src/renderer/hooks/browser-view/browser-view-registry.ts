/** Empty view states and per-session registries for preview triggers and closed tabs. */
import type { BrowserDevToolsState, BrowserFindState, BrowserNavState, BrowserTabsState } from "../../../main/browser-view-host";
import type { BrowserProfileViewState } from "../../../shared/browser-profiles";
import { ClosedBrowserTab } from "./browser-view-types";

export const EMPTY_NAV_STATE: BrowserNavState = {
	viewId: "",
	url: "",
	title: "",
	canGoBack: false,
	canGoForward: false,
	isLoading: false,
};

export const EMPTY_TABS_STATE: BrowserTabsState = {
	viewId: "",
	activeTabId: "",
	tabs: [],
};

export const EMPTY_DEVTOOLS_STATE: BrowserDevToolsState = {
	viewId: "",
	open: false,
	activeTabId: "",
	placement: "undocked",
};

export const EMPTY_FIND_STATE: BrowserFindState = {
	viewId: "",
	tabId: "",
	query: "",
	activeMatchOrdinal: 0,
	matches: 0,
	finalUpdate: true,
};

export const EMPTY_PROFILE_STATE: BrowserProfileViewState = {
	viewId: "",
	profileId: null,
	temporary: true,
};

export type PreviewTrigger = { revision: number | null; target: string; origin: string };

// The native view survives React session switches, so remember which preview
// trigger was already consumed for each session. This prevents switching back
// from reasserting previewUrl over a URL the user manually navigated to.
export const consumedPreviewTriggers = new Map<string, PreviewTrigger>();

export function resetConsumedPreviewTriggersForTest(): void {
	consumedPreviewTriggers.clear();
}

// Recently Closed has no main-process backing (unlike live tabs, which
// survive a session switch because they're kept alive in the main process
// and simply re-fetched) — it's built up purely from this hook's own
// bookkeeping. Without this, switching sessions and back lost the list for
// good, even though nothing about it actually changed.
export const closedTabsBySession = new Map<string, ClosedBrowserTab[]>();

export function resetClosedTabsForTest(): void {
	closedTabsBySession.clear();
}
