import type { ConversationItem, ConversationSnapshot, ConversationTurn } from "../../types/conversation";

/**
 * Which turn's transcript block an item belongs in, when that is not simply
 * `item.turnId`. Both answers come from daemon facts, not from text:
 *
 * - A queued message batched into the input of a turn that then started keeps
 *   its own turn row (`deliveredInTurnId` points at the carrier). The carrier
 *   answers it, so the message sits inside the carrier's block. As a block of its
 *   own it was anchored at its own sequence, below the carrier's first message,
 *   and the carrier's whole reply rendered above it (B812).
 * - A Tallpine action (spawn, kill, ...) is recorded by the daemon with no turn.
 *   The agent's tool call caused it, so it belongs to the turn that was running
 *   when the daemon recorded it. As a loose item it sat at its own sequence
 *   between other turns' blocks: below an unrelated turn, or at the very bottom
 *   while the turn that caused it kept running above (B812).
 */
export function timelineTurnOwner(snapshot: ConversationSnapshot): (item: ConversationItem) => string | undefined {
	const known = new Set(snapshot.turns.map((turn) => turn.id));
	const carrier = new Map<string, string>();
	for (const turn of snapshot.turns) {
		const target = turn.deliveredInTurnId;
		if (target && target !== turn.id && known.has(target)) carrier.set(turn.id, target);
	}
	const spans = runningSpans(snapshot.turns);
	return (item) => {
		if (item.turnId !== undefined) return carrier.get(item.turnId) ?? item.turnId;
		if (item.kind !== "activity" || item.activityKind !== "ao_action") return undefined;
		return turnRunningAt(spans, Date.parse(item.createdAt));
	};
}

type RunningSpan = { id: string; start: number; end: number };

function runningSpans(turns: ConversationTurn[]): RunningSpan[] {
	const spans: RunningSpan[] = [];
	for (const turn of turns) {
		if (turn.deliveredInTurnId || !turn.startedAt) continue;
		const start = Date.parse(turn.startedAt);
		const end = turn.completedAt
			? Date.parse(turn.completedAt)
			: turn.state === "running"
				? Number.POSITIVE_INFINITY
				: Number.NaN;
		if (Number.isFinite(start) && !Number.isNaN(end)) spans.push({ id: turn.id, start, end });
	}
	return spans;
}

function turnRunningAt(spans: RunningSpan[], at: number): string | undefined {
	if (!Number.isFinite(at)) return undefined;
	// Timestamps are whole seconds, so a turn that ended and the next that started
	// can share one. The earlier turn wins: a turn that just started has not run a
	// tool yet.
	let owner: RunningSpan | undefined;
	for (const span of spans) {
		if (span.start <= at && at <= span.end && (!owner || span.start < owner.start)) owner = span;
	}
	return owner?.id;
}
