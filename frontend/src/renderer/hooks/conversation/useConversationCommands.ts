/** Commands against a conversation (send, steer, queue, retry, edit, settings). */

import { useSendMutation } from "./useSendMutation";
import { useQueuedTurnMutations } from "./useQueuedTurnMutations";
import { useTurnEditMutations } from "./useTurnEditMutations";
import { useConversationControlMutations } from "./useConversationControlMutations";
import { useConversationDispatchTracking } from "./useConversationDispatchTracking";
import { useConversationAgentMutations } from "./useConversationAgentMutations";
import { useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { apiErrorCode, apiErrorMessage } from "../../lib/api-client";
import type { ChatEditOutcome, ChatSteerOutcome, QueuedMessageEditOptions, TurnSettings } from "../../types/conversation";

import { conversationQueryKey, ConversationLocalEcho } from "./conversation-state";
import { sendConversationMessage, echoConversationBeforeSend, dropConversationEcho, chooseConversationStartSettings, buildRetryControl, compactUnavailableMessage, editConversationMessage, steerConversation } from "./conversation-command-actions";
import { refreshRemoteConversation } from "./useConversationQuery";
import { ProviderSwitchFailed, WireImageContent, ConversationSendInput, ConversationSwitchDisplay } from "./conversation-types";
import { steerRefusal } from "./conversation-errors";



/** Commands against a conversation. Each refetches the snapshot on success. */
export function useConversationCommands(sessionId: string | undefined, hostId?: string) {
	const queryClient = useQueryClient();
	const { stateSessionId, stateKey, localEchosBySession, trackedDispatch, acknowledgeAcceptedTurn, acknowledgeLocalEcho } = useConversationDispatchTracking(queryClient, sessionId, hostId);
	const invalidateSession = useCallback(
		async (targetSessionId: string) => {
			if (hostId) await refreshRemoteConversation(queryClient, targetSessionId, hostId);
			else await queryClient.invalidateQueries({ queryKey: conversationQueryKey(targetSessionId) });
		},
		[hostId, queryClient],
	);
	const invalidate = useCallback(() => {
		if (sessionId) {
			// Refresh in the background. Awaiting the refetch in mutation onSuccess kept
			// steer, queue, and approval mutations pending after the daemon had already
			// answered, which left the composer spinner stuck and blocked further sends.
			void invalidateSession(sessionId).catch(() => {});
		}
	}, [invalidateSession, sessionId]);
	const refreshSessionInBackground = useCallback(
		(targetSessionId: string) => {
			void invalidateSession(targetSessionId).catch(() => {});
		},
		[invalidateSession],
	);

	const ctx = { queryClient, sessionId, hostId, stateKey, invalidate, refreshSessionInBackground };
	const { send } = useSendMutation(ctx);
	const { promoteQueuedTurn, cancelQueuedTurn, editQueuedTurn, reorderQueuedTurns } = useQueuedTurnMutations(ctx);
	const { rollback, retryTurn, autoResume, editMessage, activateBranch } = useTurnEditMutations(ctx);
	const { resolve, resolveInput, interrupt } = useConversationControlMutations(ctx);
	const { resumingAgent, resume, compact, chooseSettings, steer } = useConversationAgentMutations(ctx);




	const sendTargetsCurrentSession = send.variables?.targetSessionId === sessionId;
	// A refused agent switch shows in the composer's own switch notice, not again as a send error.
	const sendError = sendTargetsCurrentSession && !(send.error instanceof ProviderSwitchFailed) ? send.error : undefined;
	const interruptTargetsCurrentSession = interrupt.variables?.targetSessionId === sessionId;
	const editTargetsCurrentSession = editMessage.variables?.targetSessionId === sessionId;

	const scope = { queryClient, sessionId, hostId, stateSessionId };

	return {
		send: (input: string | ConversationSendInput) => sendConversationMessage(scope, send, input),
		pendingAcceptedTurnId:
			trackedDispatch?.state === "accepted" ? trackedDispatch.turnId : undefined,
		acknowledgeAcceptedTurn,
		localEchos: stateSessionId ? localEchosBySession[stateSessionId] ?? [] : [],
		acknowledgeLocalEcho,
		echoBeforeSend: (
			echo: Pick<ConversationLocalEcho, "clientMessageId" | "text" | "excerpts">,
			providerSwitch?: { targetHarness: string; display: ConversationSwitchDisplay },
		) => echoConversationBeforeSend(scope, echo, providerSwitch),
		dropEcho: (clientMessageId: string) => dropConversationEcho(scope, clientMessageId),
		resolve: (requestId: string, decisionId: string) => resolve.mutate({ requestId, decisionId }),
		resolveInput: (
			requestId: string,
			action: "accept" | "decline" | "cancel",
			content?: Record<string, unknown>,
		) => resolveInput.mutateAsync({ requestId, action, content }),
		interrupt: () => interrupt.mutate({ targetSessionId: sessionId as string }),
		resumeAgent: () => resume.mutateAsync(),
		resumingAgent,
		resumeError: resume.error ? apiErrorMessage(resume.error) : undefined,
		compact: () => compact.mutateAsync(),
		choosingSettings: chooseSettings.isPending && chooseSettings.variables?.targetSessionId === sessionId,
		chooseSettings: (settings: TurnSettings) => chooseSettings.mutate({ targetSessionId: sessionId as string, settings }),
		chooseStartSettings: (settings: TurnSettings) => chooseConversationStartSettings(scope, chooseSettings, resume, settings),
		/** A compaction is in flight provider-side and takes seconds, so it reads as
		 *  its own state rather than folding into the generic busy flag, which also
		 *  gates the composer. */
		compacting: compact.isPending,
		/**
		 * The daemon refuses a compaction while a turn is running, because the
		 * provider would silently discard that turn to make room. Surfaced so the
		 * control can explain itself instead of appearing to do nothing.
		 */
		compactUnavailable: compactUnavailableMessage(compact.error),
		rollback: (turnId: string) => rollback.mutateAsync(turnId),
		rollbackPending: rollback.isPending,
		rollbackError: rollback.error ? apiErrorMessage(rollback.error) : undefined,
		retryControl: buildRetryControl(scope, { retryTurn, autoResume, trackedDispatch }),
		editMessage: (turnId: string, text: string, clientMessageId?: string): Promise<ChatEditOutcome> =>
			editConversationMessage(scope, editMessage, turnId, text, clientMessageId),
		editMessagePending:
			trackedDispatch?.operation === "edit" ||
			(editMessage.isPending && editTargetsCurrentSession),
		editMessageError:
			editTargetsCurrentSession && editMessage.error
				? apiErrorMessage(editMessage.error)
				: undefined,
		activateBranch: (branchId: string) => activateBranch.mutateAsync(branchId),
		activateBranchPending: activateBranch.isPending,
		activateBranchError: activateBranch.error ? apiErrorMessage(activateBranch.error) : undefined,
		steer: (text: string, attachments?: WireImageContent[], clientMessageId?: string, recoverOnly?: boolean): Promise<ChatSteerOutcome> =>
			steerConversation(steer, text, attachments, clientMessageId, recoverOnly),
		promoteQueuedTurn: (turnId: string) => promoteQueuedTurn.mutateAsync(turnId),
		cancelQueuedTurn: (turnId: string) => cancelQueuedTurn.mutateAsync(turnId),
		editQueuedTurn: (turnId: string, text: string, options?: QueuedMessageEditOptions) => {
			if (!sessionId) return Promise.reject(new Error("No conversation session is selected."));
			return editQueuedTurn.mutateAsync({ turnId, text, ...options });
		},
		reorderQueuedTurns: (turnIds: string[]) => {
			if (!sessionId) return Promise.reject(new Error("No conversation session is selected."));
			return reorderQueuedTurns.mutateAsync(turnIds);
		},
		promoteQueuedTurnPendingTurnId: promoteQueuedTurn.isPending
			? promoteQueuedTurn.variables
			: undefined,
		cancelQueuedTurnPendingTurnId: cancelQueuedTurn.isPending ? cancelQueuedTurn.variables : undefined,
		editQueuedTurnPendingTurnId: editQueuedTurn.isPending ? editQueuedTurn.variables?.turnId : undefined,
		sendPending: send.isPending && sendTargetsCurrentSession,
		steerPending: steer.isPending,
		/**
		 * Why the last steer was refused, or undefined. Only the retryable and
		 * send-instead cases produce copy: `CHAT_STEER_UNSUPPORTED` is answered by
		 * hiding the control entirely, since the harness will never accept one.
		 */
		steerRefusal: steerRefusal(steer.error),
		/**
		 * A harness that cannot steer at all. Learned from the daemon's typed refusal
		 * rather than from the harness name, and sticky for the life of the surface: the
		 * answer is a property of the driver, not of the moment.
		 */
		steerUnsupported: apiErrorCode(steer.error) === "CHAT_STEER_UNSUPPORTED",
		busy:
			trackedDispatch?.state === "pending" ||
			(send.isPending && sendTargetsCurrentSession) ||
			resolve.isPending ||
			resolveInput.isPending ||
			(interrupt.isPending && interruptTargetsCurrentSession),
		error:
			sendError ||
			resolve.error ||
			(interruptTargetsCurrentSession && interrupt.error) ||
			chooseSettings.error
				? apiErrorMessage(
							sendError ??
							resolve.error ??
							(interruptTargetsCurrentSession ? interrupt.error : undefined) ??
							chooseSettings.error,
					)
				: undefined,
	};
}

