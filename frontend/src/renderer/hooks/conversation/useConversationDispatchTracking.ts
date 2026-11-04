/** Dispatch tracking and local echoes for one conversation session. */

import { useQuery, type QueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { sessionUiKey } from "../../lib/hosts";
import { conversationDispatchTrackingQueryKey, ConversationDispatchTrackingBySession, conversationLocalEchosQueryKey, ConversationLocalEchosBySession, releaseConversationLocalEcho } from "./conversation-state";

export function useConversationDispatchTracking(queryClient: QueryClient, sessionId: string | undefined, hostId: string | undefined) {
	const stateSessionId = sessionId ? sessionUiKey(sessionId, hostId) : undefined;
	const stateKey = (targetSessionId: string) => sessionUiKey(targetSessionId, hostId);
	const trackedDispatches = useQuery({
		queryKey: conversationDispatchTrackingQueryKey,
		queryFn: async (): Promise<ConversationDispatchTrackingBySession> => ({}),
		initialData: {} as ConversationDispatchTrackingBySession,
		enabled: false,
		// Dispatched work is a safety boundary for Chat -> Terminal. Keep both the
		// in-flight request and its accepted turn until the exact durable row is
		// observed, even if the Chat surface is unmounted.
		//
		// This cache is intentionally renderer-lifetime, not persisted. A full reload
		// starts with work unknown (which already requires a policy choice) and then a
		// fresh daemon snapshot; any later direct switch is drain-only, whose daemon
		// gate atomically rejects new intake or drains work admitted before it. Without
		// a durable request-reconciliation API, persisting an unresolved HTTP sentinel
		// would instead leave sessions permanently and incorrectly busy.
		gcTime: Number.POSITIVE_INFINITY,
		staleTime: Number.POSITIVE_INFINITY,
	}).data;
	const localEchosBySession = useQuery({
		queryKey: conversationLocalEchosQueryKey,
		queryFn: async (): Promise<ConversationLocalEchosBySession> => ({}),
		initialData: {} as ConversationLocalEchosBySession,
		enabled: false,
		gcTime: Number.POSITIVE_INFINITY,
		staleTime: Number.POSITIVE_INFINITY,
	}).data;
	const trackedDispatch = stateSessionId ? trackedDispatches[stateSessionId] : undefined;

	const acknowledgeAcceptedTurn = useCallback(
		(turnId: string) => {
			if (!sessionId) return;
			queryClient.setQueryData<ConversationDispatchTrackingBySession>(
				conversationDispatchTrackingQueryKey,
				(current = {}) => {
					const tracked = current[stateSessionId as string];
					if (tracked?.state !== "accepted" || tracked.turnId !== turnId) return current;
					const next = { ...current };
					delete next[stateSessionId as string];
					return next;
				},
			);
		},
		[queryClient, sessionId, stateSessionId],
	);
	const acknowledgeLocalEcho = useCallback(
		(turnId: string) => {
			if (!sessionId) return;
			releaseConversationLocalEcho(queryClient, stateSessionId as string, undefined, turnId);
		},
		[queryClient, sessionId, stateSessionId],
	);

	return { stateSessionId, stateKey, localEchosBySession, trackedDispatch, acknowledgeAcceptedTurn, acknowledgeLocalEcho };
}
