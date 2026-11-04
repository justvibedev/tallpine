import { describe, expect, it } from "vitest";
import { resolveContextView } from "./context-view";
import type { ConversationUsage } from "../types/conversation";

// Claude Code reports Tallpine's own 400k compaction limit as its window.
const reported: ConversationUsage = { contextUsed: 123_700, contextWindow: 400_000, inputTokens: 0, outputTokens: 0, cachedTokens: 0, totalTokens: 0 };

describe("resolveContextView", () => {
	it("uses the resolved capacity and threshold, never the reported window", () => {
		const view = resolveContextView(reported, { autoCompact: "40%", capacity: 1_000_000, compactThreshold: 400_000 });
		expect(view).toMatchObject({ capacity: 1_000_000, percent: 12, threshold: 400_000, left: 276_300, compactsAt: { kind: "tokens", tokens: 400_000, share: 40 } });
	});
	it("reads the window as unknown when a choice is made but no capacity was sent", () => {
		const view = resolveContextView(reported, { autoCompact: "40%" });
		expect(view.windowKnown).toBe(false);
		expect(view.compactsAt).toEqual({ kind: "needsWindow", choice: "40%" });
	});
	it("falls back to the reported window under Auto", () => {
		expect(resolveContextView(reported, { projectTokens: 250_000 })).toMatchObject({ capacity: 400_000, compactsAt: { kind: "project", tokens: 250_000 } });
		expect(resolveContextView(reported, {}).compactsAt).toEqual({ kind: "agentDefault" });
		expect(resolveContextView(reported, { autoCompactUnsupported: true }).compactsAt).toEqual({ kind: "unsupported" });
	});
});
