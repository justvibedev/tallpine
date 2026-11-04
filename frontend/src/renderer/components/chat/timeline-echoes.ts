import type { ConversationLocalEcho } from "../../hooks/conversation";
import { humanVisibleText } from "./ChatTimelineItems";
import { stagedAttachmentParts } from "@tallpine/shared/chat/attachments";
import {
	isSteer,
	hiddenTimelineTurnIds,
	type ConversationSnapshot,
	type ConversationActivity,
	type ConversationItem,
	type ConversationMessage,
} from "../../types/conversation";


/**
 * A human message keeps one key from the moment it is sent. The local echo carries the
 * sender's `clientMessageId` and the daemon row echoes it back, so swapping one for the
 * other keeps the same React node instead of unmounting and replaying the entrance.
 */
export const itemKey = (item: ConversationItem): string =>
	item.kind === "message" && item.role === "user" && item.clientMessageId
		? `client:${item.clientMessageId}`
		: item.id;

/**
 * The daemon's "Switched" line takes the id of the optimistic one above the same message,
 * so React keeps the node and its label morphs from "Switching…" to "Switched". The line
 * can arrive before its message does; it then takes the id of the echo still showing.
 */
export function withSwitchMarkerKeys(items: ConversationItem[], echoes: readonly ConversationLocalEcho[]): ConversationItem[] {
	const nextMessageId = (marker: ConversationItem) => {
		let next: ConversationMessage | undefined;
		for (const candidate of items) {
			if (candidate.kind !== "message" || candidate.role !== "user" || !candidate.clientMessageId) continue;
			if (candidate.sequence > marker.sequence && (!next || candidate.sequence < next.sequence)) next = candidate;
		}
		if (next?.clientMessageId || marker.kind !== "activity") return next?.clientMessageId;
		return echoes.find(({ providerSwitch: sent }) =>
			sent !== undefined && sent.targetHarness === marker.detail?.targetHarness && marker.sequence > sent.afterSequence,
		)?.clientMessageId;
	};
	const isMarker = (item: ConversationItem) => item.kind === "activity" && item.detail?.event === "provider_switch";
	// Only the marker nearest before a message was drawn with it (a failed switch, then another, leaves two).
	const nearest = new Map<string, number>();
	for (const item of items) {
		const id = isMarker(item) ? nextMessageId(item) : undefined;
		if (id && item.sequence > (nearest.get(id) ?? -Infinity)) nearest.set(id, item.sequence);
	}
	return items.map((item) => {
		const id = isMarker(item) ? nextMessageId(item) : undefined;
		return id && nearest.get(id) === item.sequence ? { ...item, id: `local-switch:${id}` } : item;
	});
}

/** A message sent with a switch shows the "Switched" line above it at once, until the daemon's arrives. */
export function withLocalSwitchMarker(echo: ConversationLocalEcho, items: ConversationItem[], message: ConversationMessage): ConversationItem[] {
	const sent = echo.providerSwitch;
	const switchArrived = sent && items.some(
		(item) =>
			item.kind === "activity" &&
			item.detail?.event === "provider_switch" &&
			item.detail.targetHarness === sent.targetHarness &&
			item.sequence > sent.afterSequence,
	);
	if (!sent || switchArrived) return [message];
	const { afterSequence: _afterSequence, ...display } = sent;
	const marker: ConversationActivity = {
		kind: "activity",
		id: `local-switch:${echo.clientMessageId}`,
		sequence: message.sequence - 0.005,
		revision: 0,
		activityKind: "system",
		status: "completed",
		summary: "Switched agent",
		createdAt: echo.createdAt,
		detail: { event: "provider_switch", ...display, switching: true },
	};
	return [marker, message];
}

/** Whether a durable row is the one a local echo stands in for. */
export function echoMatchesItem(
	echo: ConversationLocalEcho,
	item: ConversationItem,
	backgroundQueuedTurnIds: ReadonlySet<string>,
): boolean {
	if (item.kind !== "message" || item.role !== "user" || item.origin !== "human") return false;
	// A background wake keeps its echo in the timeline while the durable turn sits queued.
	if (echo.turnId && backgroundQueuedTurnIds.has(echo.turnId) && item.turnId === echo.turnId) return false;
	if (item.clientMessageId) return item.clientMessageId === echo.clientMessageId;
	// Rows from a sender that supplied no key fall back to the turn id, then to text and time.
	if (echo.turnId) return item.turnId === echo.turnId;
	return item.text === echo.text && item.createdAt >= echo.createdAt;
}

/** Guidance the user typed into a running turn, not a steer from another Tallpine session. */
function isHumanSteer(item: ConversationItem): item is ConversationActivity {
	return item.kind === "activity" && isSteer(item) && !item.detail?.senderSessionId &&
		(item.detail?.origin ?? "human") === "human";
}

/**
 * The messages Up and Down recall into the composer, oldest first: the human prompts
 * and steers the timeline shows as sent, then local echoes the daemon has not
 * returned yet. The daemon already leaves other branches out; rolled-back turns and
 * queued prompts, which the dock edits, are skipped here. A repeat of the message
 * before it is one stop, not two.
 */
export function sentMessageHistory(
	snapshot: ConversationSnapshot,
	localEchos: readonly ConversationLocalEcho[],
	openingTurnId: string | undefined,
	backgroundQueuedTurnIds: ReadonlySet<string>,
): string[] {
	const skipped = hiddenTimelineTurnIds(snapshot);
	if (openingTurnId) skipped.delete(openingTurnId);
	for (const turn of snapshot.turns) if (turn.rolledBack) skipped.add(turn.id);
	const human = snapshot.items.filter(
		(item): item is ConversationMessage => item.kind === "message" && item.role === "user" && item.origin === "human",
	);
	const sent = [...human, ...snapshot.items.filter(isHumanSteer)]
		.filter((item) => !item.turnId || !skipped.has(item.turnId))
		.sort((a, b) => a.sequence - b.sequence);
	const texts = [
		...sent.map((item) => item.kind === "message" ? item.text : item.detail?.text ?? item.summary),
		...localEchos
			.filter((echo) => !echo.queued && !human.some((item) => echoMatchesItem(echo, item, backgroundQueuedTurnIds)))
			.map((echo) => echo.text),
	];
	return texts.reduce<string[]>((history, text) => {
		// The body the bubble shows; Tallpine's attachment and report footers are not the user's words.
		const body = stagedAttachmentParts(humanVisibleText(text)).body;
		if (body.trim() && body !== history.at(-1)) history.push(body);
		return history;
	}, []);
}
