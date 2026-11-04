/** Resume, compact, settings and steer mutations. */

import { type InfiniteData, useIsMutating, useMutation } from "@tanstack/react-query";
import { apiErrorMessage } from "../../lib/api-client";
import { clientForSessionHost } from "../../lib/host-clients";
import { workspaceQueryKeyForHost } from "../useWorkspaceQuery";
import type { ConversationSnapshot, TurnSettings } from "../../types/conversation";
import { conversationQueryKey } from "./conversation-state";
import type { WireImageContent } from "./conversation-types";
import type { ConversationCommandContext } from "./conversation-command-context";

export function useConversationAgentMutations({ queryClient, sessionId, hostId, invalidate, refreshSessionInBackground }: ConversationCommandContext) {
	const resumingAgent = useIsMutating({ mutationKey: ["resume-agent", hostId ?? "local", sessionId] }) > 0;
	const resume = useMutation({
		mutationKey: ["resume-agent", hostId ?? "local", sessionId],
		mutationFn: async () => {
			const { data, error, response } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/resume-agent",
				{
					params: { path: { sessionId: sessionId as string } },
				},
			);
			if (error)
				throw new Error(apiErrorMessage(error, `Failed to resume agent (${response.status})`));
			return data;
		},
		onSuccess: () => {
			invalidate();
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
		},
	});

	/**
	 * Summarize earlier history to reclaim context.
	 *
	 * Without this a long conversation eventually cannot accept another turn at all:
	 * every turn re-sends the history, so context fills on its own. It is the
	 * difference between a session that works for an hour and one that works for a
	 * day.
	 *
	 * The daemon answers as soon as the provider accepts, and the provider then does
	 * the work over the next several seconds. So there is nothing to show on success
	 * beyond the refetch — the reclaim arrives on the timeline as its own entry,
	 * which is where it belongs, since it is durable history rather than the answer
	 * to one request.
	 */
	const compact = useMutation({
		mutationFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/compact",
				{
					params: { path: { sessionId: sessionId as string } },
				},
			);
			if (error) throw error;
			return data;
		},
		onSuccess: invalidate,
	});

	const chooseSettings = useMutation({
		mutationFn: async ({ targetSessionId, settings }: { targetSessionId: string; settings: TurnSettings }) => {
			const { data, error } = await clientForSessionHost(hostId).PATCH(
				"/api/v1/sessions/{sessionId}/conversation/settings",
				{
					params: { path: { sessionId: targetSessionId } },
					body: settings,
				},
			);
			if (error) throw error;
			return data;
		},
		// Confirm from the daemon's response before enabling Remember. A background
		// snapshot refetch can be slow; it must not expose the previous permission.
		onSuccess: async (settings, { targetSessionId }) => {
			const queryKey = conversationQueryKey(targetSessionId, hostId);
			await queryClient.cancelQueries({ queryKey });
			if (settings) {
				queryClient.setQueryData<InfiniteData<ConversationSnapshot>>(queryKey, (current) =>
					current ? {
						...current,
						pages: current.pages.map((page, index) => index === 0
							? { ...page, settings: settings as TurnSettings } : page),
					} : current,
				);
			}
			refreshSessionInBackground(targetSessionId);
		},
	});

	/**
	 * Undo back to a turn. This asks the agent to forget, not the timeline to hide:
	 * the daemon discards the history provider-side and marks its own rows to match,
	 * so a success has to be followed by a refetch rather than an optimistic edit —
	 * how much was discarded is the daemon's answer, not the client's guess.
	 */
	/**
	 * Guidance delivered INTO the running turn.
	 *
	 * Not interrupt-then-resend. An interrupt throws the turn away — its reasoning,
	 * its half-finished tool calls, the command it has running — and the resend starts
	 * from a cold start. A steer leaves all of it in place: the turn keeps its id, its
	 * context and its in-flight work, and settles `completed`.
	 *
	 * Refusals are ordinary outcomes here, not failures, and they are kept apart
	 * because the advice differs: one means "send this as a new message instead", one
	 * means "wait and try again", and one means this harness cannot do it at all.
	 */
	const steer = useMutation({
		mutationFn: async (input: { text: string; attachments?: WireImageContent[]; clientMessageId?: string; recoverOnly?: boolean }) => {
			const { data, error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/steer",
				{
					params: { path: { sessionId: sessionId as string } },
					headers: input.attachments?.length ? { "X-AO-Attachment-Upload": "1" } : undefined,
					body: { ...input, clientMessageId: input.clientMessageId ?? crypto.randomUUID() },
				},
			);
			if (error) throw error;
			return data;
		},
		onSuccess: invalidate,
	});

	return { resumingAgent, resume, compact, chooseSettings, steer };
}
