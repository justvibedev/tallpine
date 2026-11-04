import { describe, expect, it } from "vitest";
import { tabsToDiscard, type TabLifecycleSnapshot } from "./browser-tab-lifecycle";

const limits = { discardAfterMs: 600_000, maxLoadedHiddenTabs: 2, minHiddenBeforeCapDiscardMs: 60_000 };
const now = 1_000_000_000;

function tab(tabId: string, hiddenForMs: number, extra: Partial<TabLifecycleSnapshot> = {}): TabLifecycleSnapshot {
	return { tabId, inUse: false, discarded: false, discardable: true, lastInUseAt: now - hiddenForMs, ...extra };
}

describe("tabsToDiscard", () => {
	it("discards hidden tabs idle past the limit, never one in use", () => {
		expect(
			tabsToDiscard([tab("idle", 600_000), tab("recent", 599_999), tab("active", 900_000, { inUse: true })], now, limits),
		).toEqual(["idle"]);
	});

	it("keeps at most the cap of hidden tabs loaded, dropping the least recently used first", () => {
		const tabs = [tab("a", 120_000), tab("b", 300_000), tab("c", 90_000), tab("d", 200_000), tab("shown", 0, { inUse: true })];
		expect(tabsToDiscard(tabs, now, limits).sort()).toEqual(["b", "d"]);
	});

	it("does not let the cap reload a tab someone just switched away from", () => {
		const tabs = [tab("a", 30_000), tab("b", 20_000), tab("c", 10_000)];
		expect(tabsToDiscard(tabs, now, limits)).toEqual([]);
	});

	it("counts tabs that cannot be discarded toward the cap without discarding them", () => {
		const tabs = [tab("agent", 900_000, { discardable: false }), tab("a", 120_000), tab("b", 100_000)];
		expect(tabsToDiscard(tabs, now, limits)).toEqual(["a"]);
	});

	it("ignores tabs already discarded", () => {
		const tabs = [tab("gone", 900_000, { discarded: true }), tab("a", 120_000), tab("b", 100_000)];
		expect(tabsToDiscard(tabs, now, limits)).toEqual([]);
	});
});
