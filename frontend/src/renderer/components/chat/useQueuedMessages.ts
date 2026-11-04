import { useMemo, useRef } from "react";
import { sameContent } from "../../lib/stable-list";
import { type QueuedMessage } from "./QueuedMessageDock";
import { type ConversationSnapshot, type ConversationMessage } from "../../types/conversation";


/**
 * A stream refresh replaces the complete snapshot object. Queue prompts do not
 * change while a running turn emits output, so retain the previous list when its
 * contents are equal and avoid redrawing the dock and composer subtree.
 */
export function useQueuedMessages(snapshot: ConversationSnapshot, excludeTurnId?: string): QueuedMessage[] {
	const previous = useRef<QueuedMessage[]>([]);
	return useMemo(() => {
		const messagesByTurn = new Map(
			snapshot.items
				.filter(
					(item): item is ConversationMessage =>
						item.kind === "message" &&
						item.role === "user" &&
						item.origin === "human" &&
						Boolean(item.turnId),
				)
				.map((message) => [message.turnId as string, message]),
		);
		const next = snapshot.turns.flatMap((queuedTurn) => {
			if (queuedTurn.state !== "queued" || queuedTurn.id === excludeTurnId) return [];
			const message = messagesByTurn.get(queuedTurn.id);
			return message ? [{ turnId: queuedTurn.id, message }] : [];
		});
		const current = previous.current;
		if (
			current.length === next.length &&
			current.every(
				(entry, index) =>
					entry.turnId === next[index]?.turnId && sameContent(entry.message, next[index]?.message),
			)
		) {
			return current;
		}
		previous.current = next;
		return next;
	}, [excludeTurnId, snapshot.items, snapshot.turns]);
}
