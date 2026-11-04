// What a worker sent, read off a conversation item: a direct `tallpine send` (a message, or a steer when
// it arrived mid-turn), a batch of reports, or reports folded into a human message. Desktop and phone
// share this; each app's ConversationItem satisfies WorkerItem structurally.
import { SENDER_PREFIX, stripSenderPrefix } from "./sender-prefix";
import { foldedReportsEnvelope, ORCHESTRATOR_FRAME, parseReports, stripFrames, type WorkerUpdate } from "./worker-reports";

/** The parts of a conversation item these readers look at. */
export interface WorkerItem {
	kind: string;
	id: string;
	createdAt: string;
	role?: string;
	origin?: string;
	text?: string;
	summary?: string;
	senderSessionId?: string;
	senderProjectId?: string;
	senderDisplayName?: string;
	detail?: {
		event?: string;
		text?: string;
		senderSessionId?: string;
		senderProjectId?: string;
		senderDisplayName?: string;
	} | null;
}

/** Items never change in place, so each is parsed once however often the timeline renders. */
export function cachedBy<I extends object, T>(read: (item: I) => T): (item: I) => T {
	const cache = new WeakMap<I, T>();
	return (item) => {
		if (!cache.has(item)) cache.set(item, read(item));
		return cache.get(item)!;
	};
}

/** What a worker sent, when this item is one. */
export const workerUpdatesOf = cachedBy((item: WorkerItem): WorkerUpdate[] => {
	// Another agent's message that arrived mid-turn is a steer; the daemon names its sender.
	if (item.kind === "activity") {
		const sender = item.detail?.event === "steer" ? item.detail.senderSessionId?.trim() : undefined;
		if (!sender) return [];
		return [{
			key: item.id,
			name: item.detail?.senderDisplayName?.trim() || sender,
			sessionId: sender,
			projectId: item.detail?.senderProjectId,
			body: stripSenderPrefix(item.detail?.text ?? item.summary ?? "", sender).trim(),
			createdAt: item.createdAt,
		}];
	}
	const text = item.text ?? "";
	if (item.role !== "user" || item.origin === "human") return [];
	// Messages from before Tallpine stored the sender alongside carry only the [from ...] prefix.
	const prefix = SENDER_PREFIX.exec(text);
	const framed = ORCHESTRATOR_FRAME.exec(text);
	const sender = item.senderSessionId?.trim() || prefix?.[1] || framed?.[1];
	if (sender) {
		return [{
			key: item.id,
			name: item.senderDisplayName?.trim() || prefix?.[2]?.trim() || sender,
			sessionId: sender,
			projectId: item.senderProjectId,
			body: stripFrames(stripSenderPrefix(text, sender)).trim(),
			fromOrchestrator: framed ? true : undefined,
			createdAt: item.createdAt,
		}];
	}
	return parseReports(text, item.createdAt, item.id);
});

/** Reports the daemon folded into a human message because the orchestrator was busy. */
export const piggybackedUpdates = cachedBy((item: WorkerItem): WorkerUpdate[] => {
	if (item.kind !== "message" || item.role !== "user" || item.origin !== "human") return [];
	const envelope = foldedReportsEnvelope(item.text ?? "");
	return envelope ? parseReports(envelope, item.createdAt, `${item.id}:reports`) : [];
});

/** Each worker's newest update key; an older "needs you" has been overtaken. */
export function latestWorkerUpdateKeys(items: readonly WorkerItem[]): ReadonlySet<string> {
	const latest = new Map<string, string>();
	for (const item of items) {
		for (const update of [...piggybackedUpdates(item), ...workerUpdatesOf(item)]) latest.set(update.sessionId ?? update.key, update.key);
	}
	return new Set(latest.values());
}
