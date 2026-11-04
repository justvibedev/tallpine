import type { ConversationSnapshot } from "../../types/conversation";
import type { ConversationLocalEcho } from "../../hooks/conversation";
import { useMemo } from "react";
import { echoMatchesItem } from "./timeline-echoes";
import { useQueuedMessages } from "./useQueuedMessages";
import { useStableCallback } from "./useStableCallback";
import { type QueuedMessage } from "./QueuedMessageDock";

/**
 * The queued messages shown in the dock and the stable promote/cancel callbacks.
 */
export function useQueuedMessageList({
	snapshot,
	openingTurnId,
	localEchos,
	onPromoteQueuedTurn,
	onCancelQueuedTurn,
}: {
	snapshot: ConversationSnapshot;
	openingTurnId: string | undefined;
	localEchos: ConversationLocalEcho[] | undefined;
	onPromoteQueuedTurn: ((turnId: string) => Promise<unknown>) | undefined;
	onCancelQueuedTurn: ((turnId: string) => Promise<unknown>) | undefined;
}) {
	const queuedMessages = useQueuedMessages(snapshot, openingTurnId);

	const backgroundWakeQueuedTurnIds = useMemo(
		() => new Set(
			(localEchos ?? [])
				.filter((echo) => echo.backgroundWake && echo.turnId)
				.map((echo) => echo.turnId as string)
				.filter((turnId) => snapshot.turns.some((turn) => turn.id === turnId && turn.state === "queued")),
		),
		[localEchos, snapshot.turns],
	);

	// A send made while a turn was active is shown in the dock the moment it is sent,
	// not as a chat bubble that jumps to the dock when the daemon answers. It stays
	// until the daemon's own row for it arrives.
	const pendingQueuedMessages = useMemo((): QueuedMessage[] =>
		(localEchos ?? [])
			.filter((echo) => echo.queued && !snapshot.items.some((item) => echoMatchesItem(echo, item, backgroundWakeQueuedTurnIds)))
			.map((echo) => ({
				turnId: echo.turnId ?? `local:${echo.clientMessageId}`,
				pending: true,
				message: {
					kind: "message",
					id: `local:${echo.clientMessageId}`,
					turnId: echo.turnId ?? `local:${echo.clientMessageId}`,
					sequence: snapshot.latestSequence + 0.01,
					revision: 0,
					role: "user",
					origin: "human",
					text: echo.text,
				continuation: echo.continuation,
					streaming: false,
					delivery: "queued",
					clientMessageId: echo.clientMessageId,
					createdAt: echo.createdAt,
				},
			})),
		[backgroundWakeQueuedTurnIds, localEchos, snapshot.items, snapshot.latestSequence],
	);

	const visibleQueuedMessages = useMemo(
		() => [
			...queuedMessages.filter((message) => !backgroundWakeQueuedTurnIds.has(message.turnId)),
			...pendingQueuedMessages,
		],
		[backgroundWakeQueuedTurnIds, pendingQueuedMessages, queuedMessages],
	);

	const stablePromoteQueuedTurn = useStableCallback(onPromoteQueuedTurn);

	const stableCancelQueuedTurn = useStableCallback(onCancelQueuedTurn);

	return {
		queuedMessages,
		backgroundWakeQueuedTurnIds,
		visibleQueuedMessages,
		stablePromoteQueuedTurn,
		stableCancelQueuedTurn,
	};
}
