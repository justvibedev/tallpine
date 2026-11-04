/* Pure side-chat state transitions, split out of chat/IndependentSideChats.tsx (B298, P56). */

import type { components } from "../../api/schema";
import {
	emptySideChatDraft,
	type SideChatDraft,
	type SideDeliveryReceipt,
} from "../components/chat/sideChatDraft";
import type { ChatDraftAttachment, ChatDraftExcerptReference } from "./chat-drafts";

export type Side = components["schemas"]["SideConversation"];
export type Snapshot = components["schemas"]["SideSnapshot"];
export type Drafts = Record<string, SideChatDraft>;

export const MAX_SIDE_REFERENCES = 8;

/** Keep the active side when the refreshed list still has it, else the newest. */
export function selectActiveAfterRefresh(current: string | undefined, sides: Side[]): string | undefined {
	return current && sides.some((side) => side.id === current) ? current : sides.at(-1)?.id;
}

export function upsertSide(sides: Side[], side: Side): Side[] {
	return sides.some((s) => s.id === side.id) ? sides.map((s) => (s.id === side.id ? side : s)) : [...sides, side];
}

/** The tab to select once `sideId` is closed: the neighbour at the same index, clamped. */
export function nextActiveAfterClose(sides: Side[], sideId: string): string | undefined {
	const remaining = sides.filter((side) => side.id !== sideId);
	return remaining[Math.min(sides.findIndex((side) => side.id === sideId), remaining.length - 1)]?.id;
}

/**
 * Fold a refreshed latest page into the current snapshot. The latest page moves
 * forward as turns arrive, so keep its previously loaded tail: it cannot leave
 * a gap above older pages.
 */
export function mergeLatestSnapshot(current: Snapshot | undefined, next: Snapshot): Snapshot {
	if (!current || current.side.id !== next.side.id) return next;
	return {
		...next,
		hasMore: current.hasMore,
		turns: [...new Map([...(current.turns ?? []), ...(next.turns ?? [])].map((turn) => [turn.id, turn])).values()].sort(
			(a, b) => a.createdAt.localeCompare(b.createdAt),
		),
		messages: [
			...new Map([...(current.messages ?? []), ...(next.messages ?? [])].map((message) => [message.id, message])).values(),
		],
		activities: [
			...new Map([...(current.activities ?? []), ...(next.activities ?? [])].map((activity) => [activity.id, activity])).values(),
		],
	};
}

/** `/btw question` or `/side question`: returns the question text after the command. */
export function parseSideCommand(text: string): { question: string } | undefined {
	const btw = /^\/(?:btw|side)(?:\s+|$)/i.exec(text);
	return btw ? { question: text.slice(btw[0].length).trim() } : undefined;
}

export function withDraftText(drafts: Drafts, sideId: string, text: string): Drafts {
	return { ...drafts, [sideId]: { ...(drafts[sideId] ?? emptySideChatDraft()), text } };
}

/** Add a quoted excerpt unless it is a duplicate; the limit is reported, not applied. */
export function withDraftReference(
	drafts: Drafts,
	sideId: string,
	excerpt: ChatDraftExcerptReference,
): { drafts: Drafts; outcome: "added" | "duplicate" | "limit" } {
	const draft = drafts[sideId] ?? emptySideChatDraft();
	const duplicate = draft.references.some(
		(ref) =>
			ref.conversationId === excerpt.conversationId &&
			ref.messageId === excerpt.messageId &&
			ref.revision === excerpt.revision &&
			ref.text === excerpt.text,
	);
	if (duplicate) return { drafts, outcome: "duplicate" };
	if (draft.references.length >= MAX_SIDE_REFERENCES) return { drafts, outcome: "limit" };
	return { drafts: { ...drafts, [sideId]: { ...draft, references: [...draft.references, excerpt] } }, outcome: "added" };
}

export function withDraftAttachments(drafts: Drafts, sideId: string, added: ChatDraftAttachment[]): Drafts {
	return {
		...drafts,
		[sideId]: {
			...(drafts[sideId] ?? emptySideChatDraft()),
			attachments: [...(drafts[sideId]?.attachments ?? []), ...added],
		},
	};
}

/** Drop the receipt of an undelivered request the server rejected outright (4xx). */
export function withoutPendingDelivery(drafts: Drafts, sideId: string, frozen: SideDeliveryReceipt): Drafts {
	const d = drafts[sideId];
	if (!d || d.pendingDelivery !== frozen) return drafts;
	const { pendingDelivery: _, ...rest } = d;
	return { ...drafts, [sideId]: rest };
}

/**
 * The draft after its pending delivery was accepted. An untouched draft resets
 * to empty; one edited since submit keeps its edits. Handoff deliveries stay
 * recorded so a remount can retry the exact request.
 */
export function settleDeliveredDraft(
	d: SideChatDraft,
	frozen: SideDeliveryReceipt,
	handoffClientMessageId: string | undefined,
): SideChatDraft {
	const { pendingDelivery: _, ...rest } = d;
	const unchanged =
		d.text === frozen.submitted.text &&
		JSON.stringify(d.attachments) === JSON.stringify(frozen.submitted.attachments) &&
		JSON.stringify(d.references) === JSON.stringify(frozen.submitted.references);
	return {
		...(unchanged ? emptySideChatDraft() : rest),
		lastHandoffDelivery: handoffClientMessageId ? frozen : d.lastHandoffDelivery,
	};
}

export function omitKey<T>(record: Record<string, T>, key: string): Record<string, T> {
	const next = { ...record };
	delete next[key];
	return next;
}
