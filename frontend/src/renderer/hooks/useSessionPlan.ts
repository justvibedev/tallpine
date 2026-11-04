import { useInfiniteQuery, type InfiniteData } from "@tanstack/react-query";
import { latestSpanningPlan } from "../lib/session-plan";
import type { ConversationPlan, ConversationSnapshot } from "../types/conversation";
import { conversationQueryOptions, mergeConversationPages } from "./conversation";

function selectSpanningPlan(data: InfiniteData<ConversationSnapshot>): ConversationPlan | undefined {
	const plan = latestSpanningPlan(mergeConversationPages(data.pages)?.turns ?? []);
	return plan?.steps.length ? plan : undefined;
}

/**
 * The session-wide plan the chat surface already loaded, for the inspector's Plan
 * tab. It only reads the conversation cache: the chat surface owns fetching, and a
 * session that is not in chat mode has no plan to show.
 */
export function useSessionPlan(sessionId: string | undefined, hostId?: string): ConversationPlan | undefined {
	return useInfiniteQuery({
		...conversationQueryOptions(sessionId ?? "", hostId),
		enabled: false,
		select: selectSpanningPlan,
	}).data;
}
