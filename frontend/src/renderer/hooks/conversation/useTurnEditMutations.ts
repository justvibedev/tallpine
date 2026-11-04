/** Rollback, retry, auto-resume, edit and branch activation mutations. */

/** Commands against a conversation (send, steer, queue, retry, edit, settings). */

import { useMutation } from "@tanstack/react-query";
import { clientForSessionHost } from "../../lib/host-clients";

import { releaseConversationDispatch, acceptConversationDispatch } from "./conversation-state";
import { ConversationSessionMutationInput, ConversationRetryMutationInput, ConversationEditMutationInput } from "./conversation-types";


import type { ConversationCommandContext } from "./conversation-command-context";

export function useTurnEditMutations({ queryClient, sessionId, hostId, stateKey, invalidate, refreshSessionInBackground }: ConversationCommandContext) {
	const rollback = useMutation({
		mutationFn: async (turnId: string) => {
			const { data, error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/rollback",
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

	const retryTurn = useMutation({
		mutationFn: async ({ targetSessionId, sourceTurnId }: ConversationRetryMutationInput) => {
			const { data, error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/retry",
				{
					params: {
						path: { sessionId: targetSessionId, turnId: sourceTurnId },
					},
				},
			);
			if (error) throw error;
			return data;
		},
		onSuccess: (data, variables) => {
			if (data?.turnId) {
				acceptConversationDispatch(
					queryClient,
					stateKey(variables.targetSessionId),
					variables.requestId,
					data.turnId,
				);
			} else {
				releaseConversationDispatch(queryClient, stateKey(variables.targetSessionId), variables.requestId);
			}
			void refreshSessionInBackground(variables.targetSessionId);
		},
		onError: (_error, variables) => {
			releaseConversationDispatch(queryClient, stateKey(variables.targetSessionId), variables.requestId);
		},
	});

	const autoResume = useMutation({
		mutationFn: async ({ targetSessionId, sourceTurnId, enabled }: ConversationSessionMutationInput & { sourceTurnId: string; enabled: boolean }) => {
			const { error } = await clientForSessionHost(hostId).PATCH(
				"/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/auto-resume",
				{
					params: { path: { sessionId: targetSessionId, turnId: sourceTurnId } },
					body: { enabled },
				},
			);
			if (error) throw error;
		},
		onSuccess: (_data, variables) => refreshSessionInBackground(variables.targetSessionId),
	});

	const editMessage = useMutation({
		mutationFn: async ({
			targetSessionId,
			sourceTurnId,
			requestId,
			text,
		}: ConversationEditMutationInput) => {
			const { data, error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/edit",
				{
					params: {
						path: { sessionId: targetSessionId, turnId: sourceTurnId },
					},
					body: { text, clientMessageId: requestId },
				},
			);
			if (error) throw error;
			return data;
		},
		onSuccess: (data, variables) => {
			if (data?.turnId) {
				acceptConversationDispatch(
					queryClient,
					stateKey(variables.targetSessionId),
					variables.requestId,
					data.turnId,
				);
			} else {
				releaseConversationDispatch(queryClient, stateKey(variables.targetSessionId), variables.requestId);
			}
			void refreshSessionInBackground(variables.targetSessionId);
		},
		onError: (_error, variables) => {
			releaseConversationDispatch(queryClient, stateKey(variables.targetSessionId), variables.requestId);
		},
	});

	const activateBranch = useMutation({
		mutationFn: async (branchId: string) => {
			const { data, error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/branches/{branchId}/activate",
				{
					params: {
						path: { sessionId: sessionId as string, branchId },
					},
				},
			);
			if (error) throw error;
			return data;
		},
		onSettled: invalidate,
	});
	return { rollback, retryTurn, autoResume, editMessage, activateBranch };
}
