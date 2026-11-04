/** Pure selectors over a conversation snapshot. */
import { ConversationSnapshot, McpServer } from "./settings";
import { ConversationTurn } from "./turn";
import { ConversationActivity } from "./details";

/**
 * Whether this session's provider can do something.
 *
 * Absent capabilities mean the controller has not started, so nothing is known
 * yet; a control gated on this stays hidden until the daemon says otherwise. That
 * is deliberately not the same as offering the control and withdrawing it on the
 * refusal, which is what a client has to do without this and which reads as a bug
 * rather than as a difference between harnesses.
 */
export function can(snapshot: ConversationSnapshot, capability: string): boolean {
	return (snapshot.capabilities ?? []).includes(capability);
}

/**
 * Which model actually produced the answers.
 *
 * The reroute wins over the selection, because it is a correction to a claim the
 * selection already made.
 */
export function answeringModel(snapshot: ConversationSnapshot): string | undefined {
	return snapshot.modelReroute?.toModel ?? snapshot.settings.model;
}

/** Tool servers that will not answer, so the agent silently lacks their tools. */
export function brokenMcpServers(snapshot: ConversationSnapshot): McpServer[] {
	return (snapshot.mcpServers ?? []).filter(
		(server) => server.status === "failed" || server.status === "cancelled",
	);
}

/** Whether the provider is demanding credentials the daemon does not hold. */
export function needsReauth(snapshot: ConversationSnapshot): boolean {
	return snapshot.account?.authenticationState === "required" ||
		(snapshot.account?.authenticationState === undefined && Boolean(snapshot.account?.reauthRequiredAt));
}

/**
 * The turn currently in flight, if any.
 *
 * A running turn wins over a queued one: with a send queue both exist at once,
 * and the one the agent is actually working on is what the user is waiting on.
 */
export function activeTurn(snapshot: ConversationSnapshot): ConversationTurn | undefined {
	return (
		snapshot.turns.find((turn) => turn.state === "running") ??
		snapshot.turns.find((turn) => turn.state === "queued")
	);
}

/**
 * Turn ids whose human prompt is represented in the queue dock instead.
 *
 * Human prompts for queued turns live in the dock until dispatch. Automation
 * messages have no dock row, so their queued feedback stays visible in the
 * timeline. Human turns cancelled from the dock must not reappear after refresh.
 */
export function hiddenTimelineTurnIds(snapshot: ConversationSnapshot): Set<string> {
	const humanPromptTurnIds = new Set<string>();
	for (const item of snapshot.items) {
		if (
			item.kind === "message" &&
			item.role === "user" &&
			item.origin === "human" &&
			item.turnId
		) {
			humanPromptTurnIds.add(item.turnId);
		}
	}
	return new Set(
		snapshot.turns
			.filter(
				(turn) =>
					(turn.state === "queued" || turn.state === "cancelled") &&
					humanPromptTurnIds.has(turn.id),
			)
			.map((turn) => turn.id),
	);
}

/**
 * Turn ids for messages recorded but not yet sent.
 *
 * The daemon holds a mid-turn message instead of pushing it at a busy agent, so
 * the timeline has to distinguish "waiting to be sent" from "sent".
 */
export function queuedTurnIds(snapshot: ConversationSnapshot): Set<string> {
	return new Set(snapshot.turns.filter((turn) => turn.state === "queued").map((turn) => turn.id));
}

/** The pending approval a user must answer, if any. */
export function pendingApproval(snapshot: ConversationSnapshot): ConversationActivity | undefined {
	return snapshot.items.find(
		(item): item is ConversationActivity =>
			item.kind === "activity" && item.activityKind === "approval" && item.status === "pending",
	);
}

/** The structured question currently blocking the agent, if any. */
export function pendingUserInput(snapshot: ConversationSnapshot): ConversationActivity | undefined {
	return snapshot.items.find(
		(item): item is ConversationActivity =>
			item.kind === "activity" && item.activityKind === "user_input" && item.status === "pending",
	);
}

