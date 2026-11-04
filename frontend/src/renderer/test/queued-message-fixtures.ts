import type { QueuedMessage } from "../components/chat/QueuedMessageDock";

/** Queued human messages queued-1..queued-N, one per text, for QueuedMessageDock tests. */
export function queuedMessages(texts: string[]): QueuedMessage[] {
	return texts.map((text, index) => {
		const turnId = `queued-${index + 1}`;
		return {
			turnId,
			message: {
				kind: "message",
				id: `${turnId}-message`,
				turnId,
				sequence: 100 + index,
				revision: 0,
				role: "user",
				origin: "human",
				text,
				streaming: false,
				createdAt: `2026-08-11T10:0${index + 1}:00Z`,
			},
		};
	});
}
