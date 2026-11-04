/** Promote, cancel, edit and reorder queued turns. */

/** Commands against a conversation (send, steer, queue, retry, edit, settings). */

import { type InfiniteData, useMutation } from "@tanstack/react-query";
import { apiErrorMessage } from "../../lib/api-client";
import { clientForSessionHost } from "../../lib/host-clients";
import type { ConversationSnapshot, QueuedMessageEditOptions } from "../../types/conversation";

import { conversationQueryKey } from "./conversation-state";
import { applyQueuedTurnOrderToPages } from "./conversation-reducer";


import type { ConversationCommandContext } from "./conversation-command-context";

export function useQueuedTurnMutations({ queryClient, sessionId, hostId, invalidate }: ConversationCommandContext) {
	const promoteQueuedTurn = useMutation({
		mutationFn: async (turnId: string) => {
			const { data, error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/steer",
				{
					params: {
						path: { sessionId: sessionId as string, turnId },
					},
				},
			);
			if (error) throw error;
			return data;
		},
		onSuccess: invalidate,
	});

	const cancelQueuedTurn = useMutation({
		mutationFn: async (turnId: string) => {
			const { error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/cancel",
				{
					params: {
						path: { sessionId: sessionId as string, turnId },
					},
				},
			);
			if (error) throw error;
		},
		onSuccess: invalidate,
	});

	const editQueuedTurn = useMutation({
		mutationFn: async ({ turnId, text, ...options }: { turnId: string; text: string } & QueuedMessageEditOptions) => {
			const { error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/queue/edit",
				{
					params: {
						path: { sessionId: sessionId as string, turnId },
					},
					headers: options.attachments?.length ? { "X-AO-Attachment-Upload": "1" } : undefined,
					body: { text, ...options },
				},
			);
			if (error) throw error;
		},
		onSuccess: invalidate,
	});

	const reorderQueuedTurns = useMutation({
		mutationFn: async (turnIds: string[]) => {
			const { error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/queue/reorder",
				{
					params: {
						path: { sessionId: sessionId as string },
					},
					body: { turnIds },
				},
			);
			if (error) throw new Error(apiErrorMessage(error, "Could not reorder queued messages"));
		},
		onMutate: async (turnIds) => {
			if (!sessionId) return;
			const queryKey = conversationQueryKey(sessionId, hostId);
			await queryClient.cancelQueries({ queryKey });
			const previous = queryClient.getQueryData<InfiniteData<ConversationSnapshot>>(queryKey);
			if (previous) {
				queryClient.setQueryData<InfiniteData<ConversationSnapshot>>(
					queryKey,
					applyQueuedTurnOrderToPages(previous, turnIds),
				);
			}
			return { previous };
		},
		onError: (_error, _turnIds, context) => {
			if (!sessionId || !context?.previous) return;
			queryClient.setQueryData(conversationQueryKey(sessionId, hostId), context.previous);
		},
		onSettled: () => {
			invalidate();
		},
	});
	return { promoteQueuedTurn, cancelQueuedTurn, editQueuedTurn, reorderQueuedTurns };
}
