/** Approval, input and interrupt mutations. */

import { useMutation } from "@tanstack/react-query";
import { apiErrorCode } from "../../lib/api-client";
import { clientForSessionHost } from "../../lib/host-clients";
import type { ConversationSessionMutationInput } from "./conversation-types";
import type { ConversationCommandContext } from "./conversation-command-context";

export function useConversationControlMutations({ sessionId, hostId, invalidate, refreshSessionInBackground }: ConversationCommandContext) {
	const resolve = useMutation({
		mutationFn: async (input: { requestId: string; decisionId: string }) => {
			const { error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/approvals/{requestId}/resolve",
				{
					params: {
						path: {
							sessionId: sessionId as string,
							requestId: input.requestId,
						},
					},
					body: { decisionId: input.decisionId },
				},
			);
			if (error && !(hostId && apiErrorCode(error) === "CHAT_REQUEST_NOT_PENDING")) throw error;
		},
		onSettled: hostId ? invalidate : undefined,
		onSuccess: hostId ? undefined : invalidate,
	});

	const resolveInput = useMutation({
		mutationFn: async (input: {
			requestId: string;
			action: "accept" | "decline" | "cancel";
			content?: Record<string, unknown>;
		}) => {
			const { error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/inputs/{requestId}/resolve",
				{
					params: {
						path: {
							sessionId: sessionId as string,
							requestId: input.requestId,
						},
					},
					body: { action: input.action, content: input.content },
				},
			);
			if (error && !(hostId && apiErrorCode(error) === "CHAT_REQUEST_NOT_PENDING")) throw error;
		},
		onSettled: hostId ? invalidate : undefined,
		onSuccess: hostId ? undefined : invalidate,
	});

	const interrupt = useMutation({
		mutationFn: async ({ targetSessionId }: ConversationSessionMutationInput) => {
			const { error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/interrupt",
				{ params: { path: { sessionId: targetSessionId } } },
			);
			if (error) throw error;
		},
		onSuccess: (_data, variables) => refreshSessionInBackground(variables.targetSessionId),
		// A failed interrupt (e.g. CHAT_NO_ACTIVE_TURN) means the cached turn
		// state is wrong. Refetch so the UI discovers the real state instead of
		// keeping a Working bar the user cannot dismiss.
		onError: (_error, variables) => refreshSessionInBackground(variables.targetSessionId),
	});

	return { resolve, resolveInput, interrupt };
}
