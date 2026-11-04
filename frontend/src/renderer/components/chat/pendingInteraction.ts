import {
	type ConversationActivity,
	type ConversationItem,
	type ConversationTurn,
} from "../../types/conversation";


/**
 * The newest pending approval or question the live turn is waiting on.
 *
 * A request with no turn id belongs to the session rather than a turn, so a
 * turn-scoped one always wins; between equals the later sequence is the live one.
 */
export function latestPendingInteraction(
	items: ConversationItem[],
	activityKind: "approval" | "user_input",
	turn: ConversationTurn | undefined,
): ConversationActivity | undefined {
	return items.reduce<ConversationActivity | undefined>((latest, item) => {
		if (
			item.kind !== "activity" ||
			item.activityKind !== activityKind ||
			item.status !== "pending" ||
			(item.turnId ? item.turnId !== turn?.id : !turn)
		) {
			return latest;
		}
		if (latest?.turnId && !item.turnId) return latest;
		if (item.turnId && !latest?.turnId) return item;
		return !latest || item.sequence > latest.sequence ? item : latest;
	}, undefined);
}
