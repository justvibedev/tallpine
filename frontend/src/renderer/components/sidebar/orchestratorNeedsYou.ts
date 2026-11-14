import { useQuery } from "@tanstack/react-query";
import { listProjectAsks, projectAsksKey } from "../../lib/ask-api";
import { listProjectPlans, projectPlansKey } from "../../lib/plan-api";
import { waitingCount } from "../ask/askModel";
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

/**
 * Open asks plus plans waiting on the owner in one chat (a project's main chat,
 * or a lane's), for the sidebar badge. Shares its queries, and so its refetches
 * on backlog events, with the strip and the plan cards. 0 while disabled.
 */
export function useWaitingOnYouCount(projectId: string, chat: { lane?: string; sessionId?: string }, enabled: boolean): number {
	const asks = useQuery({ queryKey: projectAsksKey(projectId), queryFn: () => listProjectAsks(projectId), enabled, retry: false });
	const plans = useQuery({ queryKey: projectPlansKey(projectId), queryFn: () => listProjectPlans(projectId), enabled, retry: false });
	if (!enabled) return 0;
	return waitingCount(asks.data ?? [], plans.data ?? [], chat);
}
