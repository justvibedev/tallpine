// When a hidden browser tab gives its renderer back.
//
// Every browser tab is a WebContentsView with its own renderer process. A tab
// nobody is using is throttled at once (Chromium then treats it as a
// background page: timers at 1 Hz, no animation frames). One that has stayed
// out of use for a while is discarded: its renderer process is ended while the
// WebContents keeps its URL, title and back/forward history, and the tab
// reloads the next time it is used, as Chrome does with discarded tabs.

export const TAB_DISCARD_AFTER_MS = 10 * 60_000;
// Hidden tabs a session keeps loaded before the least recently used go early.
export const MAX_LOADED_HIDDEN_TABS_PER_SESSION = 3;
// A tab must have been hidden this long before the cap may discard it, so
// flipping between tabs never reloads one.
export const MIN_HIDDEN_BEFORE_CAP_DISCARD_MS = 60_000;
export const TAB_LIFECYCLE_SWEEP_MS = 30_000;

export type TabLifecycleSnapshot = {
	tabId: string;
	inUse: boolean;
	discarded: boolean;
	// Whether the tab may be discarded at all (a user page, loaded, alone in
	// its process). A tab that may not still counts toward the cap.
	discardable: boolean;
	lastInUseAt: number;
};

export type TabLifecycleLimits = {
	discardAfterMs: number;
	maxLoadedHiddenTabs: number;
	minHiddenBeforeCapDiscardMs: number;
};

export const DEFAULT_TAB_LIFECYCLE_LIMITS: TabLifecycleLimits = {
	discardAfterMs: TAB_DISCARD_AFTER_MS,
	maxLoadedHiddenTabs: MAX_LOADED_HIDDEN_TABS_PER_SESSION,
	minHiddenBeforeCapDiscardMs: MIN_HIDDEN_BEFORE_CAP_DISCARD_MS,
};

/** The tabs of one session to discard now: idle past the limit, or the oldest hidden beyond the cap. */
export function tabsToDiscard(
	tabs: TabLifecycleSnapshot[],
	now: number,
	limits: TabLifecycleLimits = DEFAULT_TAB_LIFECYCLE_LIMITS,
): string[] {
	const hidden = tabs.filter((tab) => !tab.inUse && !tab.discarded);
	const discard = new Set<string>();
	for (const tab of hidden) {
		if (tab.discardable && now - tab.lastInUseAt >= limits.discardAfterMs) discard.add(tab.tabId);
	}
	let loaded = hidden.length - discard.size;
	const oldestFirst = [...hidden].sort((a, b) => a.lastInUseAt - b.lastInUseAt);
	for (const tab of oldestFirst) {
		if (loaded <= limits.maxLoadedHiddenTabs) break;
		if (discard.has(tab.tabId) || !tab.discardable) continue;
		if (now - tab.lastInUseAt < limits.minHiddenBeforeCapDiscardMs) continue;
		discard.add(tab.tabId);
		loaded -= 1;
	}
	return [...discard];
}
