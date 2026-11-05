import { useQuery } from "@tanstack/react-query";
import { fetchConversationPage } from "../../hooks/conversation/useConversationQuery";
import type { ConversationItem } from "../../types/conversation";
import type { WorkspaceSession } from "../../types/workspace";
import { hasNeedsYouMarker } from "../chat/WorkerUpdates";

/** Recent items are enough: only the newest reply and anything after it matter. */
const TAIL_LIMIT = 30;

/**
 * True while the newest assistant reply carries the "Needs you:" marker and the
 * owner has not written since. A human message after it answers it.
 */
export function latestReplyUnanswered(items: readonly ConversationItem[]): boolean {
	for (let index = items.length - 1; index >= 0; index -= 1) {
		const item = items[index];
		if (item?.kind !== "message") continue;
		if (item.role === "user" && item.origin === "human" && !item.continuation) return false;
		if (item.role === "assistant" && item.text.trim() !== "") return hasNeedsYouMarker(item.text);
	}
	return false;
}

/**
 * Whether a project's orchestrator waits on an owner reply. Refetches when the
 * session's activity moves (it only changes when a turn lands), so an idle
 * sidebar costs nothing.
 */
export function useOrchestratorNeedsYou(session: WorkspaceSession | undefined, enabled: boolean): boolean {
	const query = useQuery({
		queryKey: ["orchestrator-needs-you", session?.hostId ?? "", session?.id ?? "", session?.status, session?.updatedAt, session?.lastInteractionAt, session?.lastUserMessageAt],
		queryFn: async () => latestReplyUnanswered((await fetchConversationPage(session?.id ?? "", session?.hostId, undefined, TAIL_LIMIT)).items),
		enabled: enabled && Boolean(session) && !session?.isTerminated,
		staleTime: Infinity,
		retry: false,
	});
	// `enabled: false` keeps cached data, so gate the result too.
	return enabled && (query.data ?? false);
}
