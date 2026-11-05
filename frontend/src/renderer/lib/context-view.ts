import type { ConversationCompaction, ConversationRateLimits, ConversationUsage } from "../types/conversation";
import type { CompactionControlProps } from "../components/chat/CompactionControl";

export interface ContextDetails {
	harness?: string;
	model?: string;
	/** The effective compaction choice: the session's own, else the Settings default. */
	autoCompact?: string;
	/** The window the project's own environment sets, which Auto leaves in force. */
	projectTokens?: number;
	/** The model's real window, resolved by the daemon; the agent's reported window is not capacity. */
	capacity?: number;
	/** Where the chosen setting compacts, resolved by the daemon; absent for Auto or an unknown window. */
	compactThreshold?: number;
	/** The agent cannot be told where to compact, as Codex chats. */
	autoCompactUnsupported?: boolean;
	/** ISO time of the last compaction, absent when none is known. */
	compactedAt?: string;
	rateLimits?: ConversationRateLimits;
	/** The provider's latest compaction report: running, finished (with sizes), or failed. */
	lastCompaction?: ConversationCompaction;
	/** The session the breakdown is asked about; absent where there is nothing to ask. */
	breakdown?: { sessionId: string; hostId?: string };
	/** The session whose per-model token usage is listed. */
	modelUsage?: { sessionId: string; hostId?: string };
	/** Change this session's compaction window; absent where the agent cannot take one. */
	compaction?: CompactionControlProps;
}

/** What the "Compacts at" row says, before it is turned into text. */
export type CompactsAt =
	| { kind: "unsupported" }
	| { kind: "project"; tokens: number }
	| { kind: "agentDefault" }
	| { kind: "needsWindow"; choice: string }
	| { kind: "tokens"; tokens: number; share?: number };

export interface ContextView {
	capacity: number;
	windowKnown: boolean;
	percent?: number;
	threshold?: number;
	left?: number;
	compactsAt: CompactsAt;
}

/**
 * The figures the details popover shows, from the daemon's resolved values.
 * Once Tallpine sets a compaction limit the agent reports that limit as its window, so
 * the resolved capacity wins. With a choice made and no capacity sent, the
 * reported window may be that limit: the window reads as unknown, not as it.
 */
export function resolveContextView(usage: ConversationUsage, details?: ContextDetails): ContextView {
	const { contextUsed, contextWindow } = usage;
	const choice = details?.autoCompact;
	const capacity = details?.capacity && details.capacity > 0 ? details.capacity : choice ? 0 : contextWindow;
	const windowKnown = capacity > 0;
	const percent = windowKnown ? Math.round(Math.min(1, Math.max(0, contextUsed / capacity)) * 100) : undefined;
	const threshold = details?.compactThreshold && details.compactThreshold > 0 ? details.compactThreshold : undefined;

	let compactsAt: CompactsAt;
	if (details?.autoCompactUnsupported) compactsAt = { kind: "unsupported" };
	else if (!choice && details?.projectTokens) compactsAt = { kind: "project", tokens: details.projectTokens };
	else if (!choice) compactsAt = { kind: "agentDefault" };
	else if (threshold === undefined) compactsAt = { kind: "needsWindow", choice };
	else compactsAt = { kind: "tokens", tokens: threshold, share: windowKnown ? Math.round((threshold / capacity) * 100) : undefined };

	return { capacity, windowKnown, percent, threshold, left: threshold === undefined ? undefined : threshold - contextUsed, compactsAt };
}

/**
 * Kiro's ACP reports context as a percentage only; its driver sends it as
 * used=<pct> of a window of 100, which is not a token count.
 */
export function isPercentContextHarness(harness?: string): boolean {
	return harness === "kiro";
}
