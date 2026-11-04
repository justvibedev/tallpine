/** The send mutation: local echo, dispatch tracking and provider-switch handling. */

/** Commands against a conversation (send, steer, queue, retry, edit, settings). */

import { type InfiniteData, useMutation } from "@tanstack/react-query";
import type { components } from "../../../api/schema";
import { clientForSessionHost } from "../../lib/host-clients";
import type { ConversationSnapshot } from "../../types/conversation";

import { conversationDispatchTrackingQueryKey } from "./conversation-state";
import { ConversationDispatchTrackingBySession, conversationQueryKey, addConversationLocalEcho, switchEcho, newestCachedSequence, acceptConversationLocalEcho, releaseConversationDispatch, acceptConversationDispatch, releaseConversationLocalEcho } from "./conversation-state";
import { refreshAfterProviderSwitch } from "./useConversationQuery";
import { ConversationSendMutationInput, switchOutcomeUnknown, ProviderSwitchFailed } from "./conversation-types";


import type { ConversationCommandContext } from "./conversation-command-context";

export function useSendMutation({ queryClient, hostId, stateKey, refreshSessionInBackground }: ConversationCommandContext) {
	const send = useMutation({
		onMutate: (variables: ConversationSendMutationInput) => {
			const current = queryClient.getQueryData<InfiniteData<ConversationSnapshot>>(
				conversationQueryKey(variables.targetSessionId, hostId),
			);
			const backgroundWake = current?.pages.some((page) => page.controller.state === "hibernated") ?? false;
			const turnActive = current?.pages.some((page) =>
				page.turns.some((turn) => turn.state === "running" || turn.state === "queued"),
			) ?? false;
			addConversationLocalEcho(queryClient, stateKey(variables.targetSessionId), {
				clientMessageId: variables.clientMessageId,
				text: variables.input.text,
				continuation: variables.input.continuation,
				excerpts: variables.input.excerpts?.map((excerpt) => ({
					conversationId: excerpt.conversationId,
					text: excerpt.text,
					messageId: excerpt.messageId,
					revision: excerpt.revision,
				})),
				createdAt: new Date().toISOString(),
				backgroundWake,
				queued: turnActive && !backgroundWake,
				providerSwitch: switchEcho(variables.input.switchDisplay, variables.input.providerSwitch?.targetHarness, newestCachedSequence(queryClient, variables.targetSessionId, hostId)),
			});
			queryClient.setQueryData<ConversationDispatchTrackingBySession>(
				conversationDispatchTrackingQueryKey,
				(current = {}) => {
					const tracked = current[stateKey(variables.targetSessionId)];
					if (tracked && tracked.requestId !== variables.clientMessageId) return current;
					return {
						...current,
						[stateKey(variables.targetSessionId)]: {
							operation: "send",
							requestId: variables.clientMessageId,
							state: "pending",
						},
					};
				},
			);
		},
		mutationFn: async ({
			targetSessionId,
			clientMessageId,
			input,
		}: ConversationSendMutationInput) => {
			const { providerSwitch, switchDisplay: _switchDisplay, ...message } = input;
			if (providerSwitch) {
				// One request: the daemon switches, then sends, without waiting on a refetch here.
				const refresh = () => void refreshAfterProviderSwitch(queryClient, targetSessionId, hostId).catch(() => {});
				const { data, error, response } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/provider-switch", {
					params: { path: { sessionId: targetSessionId } },
					headers: input.attachments?.length ? { "X-AO-Attachment-Upload": "1" } : undefined,
					body: { ...providerSwitch, idempotencyKey: crypto.randomUUID(), message: { ...message, clientMessageId } },
				}).catch((failure: unknown) => {
					// No answer: the daemon may have switched, so what it runs is read back.
					refresh();
					throw failure;
				});
				const switched = !error || response.headers.get("X-AO-Provider-Switched") === "1";
				const refused = Boolean(error) && !switched && !switchOutcomeUnknown(response.status, error);
				if (!refused) refresh();
				if (error) throw refused ? new ProviderSwitchFailed(error) : error;
				const accepted: components["schemas"]["SendConversationMessageResponse"] = data.message ?? { duplicate: true };
				return accepted;
			}
			const { data, error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/messages",
				{
					params: { path: { sessionId: targetSessionId } },
					headers: input.attachments?.length ? { "X-AO-Attachment-Upload": "1" } : undefined,
					// A stable id per attempt makes a retry idempotent: the daemon
					// answers `duplicate` instead of opening a second provider turn.
					body: { ...message, clientMessageId },
				},
			);
			if (error) throw error;
			return data;
		},
		onSuccess: (data, variables) => {
			const acceptedTurnId = data?.turnId;
			if (acceptedTurnId) {
				acceptConversationLocalEcho(
					queryClient,
					stateKey(variables.targetSessionId),
					variables.clientMessageId,
					acceptedTurnId,
				);
				if (data.state === "queued") {
					// A queued row is already durable and did not start a new provider turn,
					// so keeping the accepted-turn safety marker would block the next queue
					// entry until a refetch observes this id — and the composer would swallow
					// further Enter presses with no error.
					releaseConversationDispatch(
						queryClient,
						stateKey(variables.targetSessionId),
						variables.clientMessageId,
					);
				} else {
					// A refetch can briefly return the pre-send snapshot. Keep the accepted
					// turn locally visible as pending until that exact durable row arrives.
					acceptConversationDispatch(
						queryClient,
						stateKey(variables.targetSessionId),
						variables.clientMessageId,
						acceptedTurnId,
					);
				}
			} else {
				// A duplicate response intentionally has no turn id: the daemon already
				// delivered this idempotency key, so there is no exact new row this
				// renderer can wait to observe. Release only this request's sentinel.
				releaseConversationDispatch(
					queryClient,
					stateKey(variables.targetSessionId),
					variables.clientMessageId,
				);
				releaseConversationLocalEcho(
					queryClient,
					stateKey(variables.targetSessionId),
					variables.clientMessageId,
				);
			}
			// Delivery is already authoritative at this point. Refresh in the
			// background so a slow conversation refetch cannot keep send.isPending
			// true and leave the composer disabled or spinning after the daemon
			// accepted the message.
			void refreshSessionInBackground(variables.targetSessionId);
		},
		onError: (_error, variables) => {
			releaseConversationDispatch(
				queryClient,
				stateKey(variables.targetSessionId),
				variables.clientMessageId,
			);
			releaseConversationLocalEcho(
				queryClient,
				stateKey(variables.targetSessionId),
				variables.clientMessageId,
			);
		},
	});
	return { send };
}
