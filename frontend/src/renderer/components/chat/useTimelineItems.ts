import type { ChatDraftInlineEdit } from "../../lib/chat-drafts";
import { useLayoutEffect, useMemo, useRef, useState } from "react";
import { sameContent, useStableList } from "../../lib/stable-list";
import { echoMatchesItem, itemKey, withLocalSwitchMarker, withSwitchMarkerKeys } from "./timeline-echoes";
import { groupByTurn, groupKey, readableItems, sameGroup } from "./timeline-groups";
import type { ConversationLocalEcho } from "../../hooks/conversation";
import { useWorkerUpdateIndex } from "./WorkerUpdates";
import {
	hiddenTimelineTurnIds,
	type ConversationSnapshot,
	type ConversationMessage,
} from "../../types/conversation";

/**
 * The readable, echo-merged items and the turn groups the transcript renders.
 */
export function useTimelineItems({
	snapshot,
	messageEdit,
	localEchos,
	requestGlideToEnd,
	openingTurnId,
	hasPlanTab,
	workerUpdates,
}: {
	snapshot: ConversationSnapshot;
	messageEdit: ChatDraftInlineEdit | undefined;
	localEchos: ConversationLocalEcho[];
	requestGlideToEnd: () => void;
	openingTurnId: string | undefined;
	hasPlanTab: boolean | undefined;
	workerUpdates: boolean;
}) {
	const readable = useMemo(() => readableItems(snapshot), [snapshot]);

	const items = useStableList(readable, itemKey, sameContent);

	const seenHumanMessageIds = useRef<Set<string> | undefined>(undefined);

	const lastSeenLatestSequence = useRef<number | undefined>(undefined);

	const [newHumanMessageIds, setNewHumanMessageIds] = useState<ReadonlySet<string>>(new Set());

	const editedMessageVisible = Boolean(
		messageEdit &&
		items.some(
			(item) =>
				item.kind === "message" && item.role === "user" && item.turnId === messageEdit.turnId,
		),
	);

	const localItems = useMemo(() => {
		const backgroundQueuedTurnIds = new Set(
			localEchos
				.filter((echo) => echo.backgroundWake && echo.turnId)
				.map((echo) => echo.turnId as string)
				.filter((turnId) => snapshot.turns.some((turn) => turn.id === turnId && turn.state === "queued")),
		);
		return localEchos
			.filter(
				(echo) =>
					// A send made behind a running turn lives in the queue dock, not the chat.
					!echo.queued &&
					!items.some(
						(item) =>
							item.kind === "message" &&
							item.role === "user" &&
							item.origin === "human" &&
							echoMatchesItem(echo, item, backgroundQueuedTurnIds),
					),
			)
			.flatMap((echo, index) => withLocalSwitchMarker(echo, items, {
				kind: "message",
				id: `local:${echo.clientMessageId}`,
				turnId: `local:${echo.clientMessageId}`,
				clientMessageId: echo.clientMessageId,
				sequence: snapshot.latestSequence + index + 0.01,
				revision: 0,
				role: "user",
				origin: "human",
				text: echo.text,
				continuation: echo.continuation,
				content: echo.excerpts?.map((excerpt) => ({
					type: "excerpt",
					text: excerpt.text,
					sourceConversationId: excerpt.conversationId,
					sourceMessageId: excerpt.messageId,
					sourceRevision: excerpt.revision,
				})),
				streaming: false,
				delivery: echo.backgroundWake || echo.turnId ? "accepted" : "sending",
				createdAt: echo.createdAt,
			}));
	}, [items, localEchos, snapshot.latestSequence, snapshot.turns]);

	const backgroundQueuedTurnIds = useMemo(
		() => new Set(
			localEchos
				.filter((echo) => echo.backgroundWake && echo.turnId)
				.map((echo) => echo.turnId as string)
				.filter((turnId) => snapshot.turns.some((turn) => turn.id === turnId && turn.state === "queued")),
		),
		[localEchos, snapshot.turns],
	);

	const timelineItems = useStableList(
		[
			...withSwitchMarkerKeys(items.filter(
				(item) => !(item.kind === "message" && item.role === "user" && (
					(item.turnId && backgroundQueuedTurnIds.has(item.turnId)) ||
					// A terminal switch hands the note over as the first prompt; it is not the user's.
					item.text.trimStart().startsWith("<ao-handoff-request")
				)),
			), localEchos),
			...localItems,
		],
		itemKey,
		sameContent,
	);

	// Keyed by identity, not row id: the local echo already played the entrance, so the
	// durable row that replaces it must not announce itself a second time. A layout
	// effect, so the flag lands before the bubble's first paint: set after it, the
	// entrance would restart from opacity 0 and the bubble would blink.
	useLayoutEffect(() => {
		const humanMessages = timelineItems.filter(
			(item): item is ConversationMessage =>
				item.kind === "message" && item.role === "user" && item.origin === "human",
		);
		const humanMessageKeys = new Set(humanMessages.map(itemKey));
		if (!seenHumanMessageIds.current) {
			seenHumanMessageIds.current = humanMessageKeys;
			lastSeenLatestSequence.current = snapshot.latestSequence;
			return;
		}
		const added = new Set(
			humanMessages
				.filter(
					(item) =>
						!seenHumanMessageIds.current?.has(itemKey(item)) &&
						item.sequence > (lastSeenLatestSequence.current ?? -Infinity),
				)
				.map(itemKey),
		);
		seenHumanMessageIds.current = humanMessageKeys;
		lastSeenLatestSequence.current = snapshot.latestSequence;
		if (added.size > 0) setNewHumanMessageIds(added);
	}, [timelineItems, snapshot.latestSequence]);

	const previousEchoIds = useRef<ReadonlySet<string>>(new Set(localEchos.map((echo) => echo.clientMessageId)));

	useLayoutEffect(() => {
		const ids = new Set(localEchos.map((echo) => echo.clientMessageId));
		const sent = [...ids].some((id) => !previousEchoIds.current.has(id));
		previousEchoIds.current = ids;
		if (!sent) return;
		// The reader just sent: bring them to their prompt even if they had scrolled away.
		requestGlideToEnd();
	}, [localEchos, requestGlideToEnd]);

	const grouped = useMemo(() => {
		const hiddenTurns = hiddenTimelineTurnIds(snapshot);
		if (openingTurnId) hiddenTurns.delete(openingTurnId);
		return groupByTurn({ ...snapshot, items: timelineItems }, Boolean(hasPlanTab)).filter(
			(group) => !group.turnId || !hiddenTurns.has(group.turnId),
		);
	}, [hasPlanTab, openingTurnId, snapshot, timelineItems]);

	const groups = useStableList(grouped, groupKey, sameGroup);

	const workerUpdateIndex = useWorkerUpdateIndex(workerUpdates ? snapshot.items : undefined);

	return {
		newHumanMessageIds,
		editedMessageVisible,
		timelineItems,
		groups,
		workerUpdateIndex,
	};
}
