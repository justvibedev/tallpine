/** Imperative command bodies for useConversationCommands, kept as plain functions over the hook's own mutation objects. */

import type { QueryClient } from "@tanstack/react-query";
import type { UseMutationResult } from "@tanstack/react-query";
import { apiErrorCode, apiErrorMessage } from "../../lib/api-client";
import { clientForSessionHost } from "../../lib/host-clients";
import type { ChatEditOutcome, ChatSteerOutcome, TurnSettings } from "../../types/conversation";
import { addConversationLocalEcho, claimConversationDispatch, newestCachedSequence, releaseConversationLocalEcho, switchEcho, type ConversationDispatchTracking, type ConversationLocalEcho } from "./conversation-state";
import type { ConversationSendInput, ConversationSwitchDisplay, WireImageContent } from "./conversation-types";
import { editNonAcceptance, steerNonAcceptance } from "./conversation-errors";

// biome-ignore lint/suspicious/noExplicitAny: mutation result generics differ per call site
type AnyMutation = UseMutationResult<any, any, any, any>;

export interface ConversationActionScope {
	queryClient: QueryClient;
	sessionId: string | undefined;
	hostId: string | undefined;
	stateSessionId: string | undefined;
}

export function sendConversationMessage(scope: ConversationActionScope, send: AnyMutation, input: string | ConversationSendInput) {
	const { queryClient, sessionId, stateSessionId } = scope;
	if (!sessionId) return Promise.reject(new Error("No conversation session is selected."));
	const clientMessageId = (typeof input === "string" ? undefined : input.clientMessageId) ?? crypto.randomUUID();
	const normalizedInput: ConversationSendInput = typeof input === "string"
		? { text: input }
		: {
			...input,
			text: input.text.trim() || (input.excerpts?.length
				? `Use the attached ${input.excerpts.length} chat excerpt(s) as context`
				: input.text),
		};
	// React cannot disable the composer until its next render. Claim the
	// session in the shared registry synchronously so two Enter events in the
	// same tick cannot both cross the transport boundary.
	if (!claimConversationDispatch(queryClient, stateSessionId as string, clientMessageId, "send")) {
		return Promise.reject(new Error("Conversation work is already being sent for this session."));
	}
	return send.mutateAsync({
		targetSessionId: sessionId,
		clientMessageId,
		input: normalizedInput,
	});
}

/** Shows a message at once while the agent switch it waits for runs. */
export function echoConversationBeforeSend(
	scope: ConversationActionScope,
	echo: Pick<ConversationLocalEcho, "clientMessageId" | "text" | "excerpts">,
	providerSwitch?: { targetHarness: string; display: ConversationSwitchDisplay },
) {
	const { queryClient, sessionId, hostId, stateSessionId } = scope;
	if (!stateSessionId || !sessionId) return;
	addConversationLocalEcho(queryClient, stateSessionId, {
		...echo,
		createdAt: new Date().toISOString(),
		providerSwitch: switchEcho(providerSwitch?.display, providerSwitch?.targetHarness, newestCachedSequence(queryClient, sessionId, hostId)),
	});
}

/** Takes back a message shown before a send that never went out. */
export function dropConversationEcho(scope: ConversationActionScope, clientMessageId: string) {
	if (scope.stateSessionId) releaseConversationLocalEcho(scope.queryClient, scope.stateSessionId, clientMessageId);
}

/** Saves settings the agent only reads when it starts, then restarts it. The conversation is resumed, not replaced. */
export async function chooseConversationStartSettings(
	scope: ConversationActionScope,
	chooseSettings: AnyMutation,
	resume: AnyMutation,
	settings: TurnSettings,
) {
	const { sessionId, hostId } = scope;
	await chooseSettings.mutateAsync({ targetSessionId: sessionId as string, settings });
	const { error } = await clientForSessionHost(hostId).POST(
		"/api/v1/sessions/{sessionId}/exit-agent",
		{ params: { path: { sessionId: sessionId as string } } },
	);
	if (error) throw new Error(apiErrorMessage(error));
	await resume.mutateAsync(undefined);
}

export function retryConversationTurn(scope: ConversationActionScope, retryTurn: AnyMutation, turnId: string) {
	const { queryClient, sessionId, stateSessionId } = scope;
	if (!sessionId) return Promise.reject(new Error("No conversation session is selected."));
	const requestId = crypto.randomUUID();
	if (!claimConversationDispatch(queryClient, stateSessionId as string, requestId, "retry", turnId)) {
		return Promise.reject(new Error("Conversation work is already being sent for this session."));
	}
	return retryTurn.mutateAsync({
		requestId,
		sourceTurnId: turnId,
		targetSessionId: sessionId,
	});
}

export async function editConversationMessage(
	scope: ConversationActionScope,
	editMessage: AnyMutation,
	turnId: string,
	text: string,
	clientMessageId?: string,
): Promise<ChatEditOutcome> {
	const { queryClient, sessionId, stateSessionId } = scope;
	if (!sessionId) return Promise.reject(new Error("No conversation session is selected."));
	const requestId = clientMessageId ?? crypto.randomUUID();
	if (!claimConversationDispatch(queryClient, stateSessionId as string, requestId, "edit", turnId)) {
		return Promise.reject(new Error("Conversation work is already being sent for this session."));
	}
	try {
		await editMessage.mutateAsync({
			requestId,
			sourceTurnId: turnId,
			targetSessionId: sessionId,
			text,
		});
		return { status: "accepted" };
	} catch (error) {
		const outcome = editNonAcceptance(error);
		if (outcome) return outcome;
		throw error;
	}
}

export async function steerConversation(
	steer: AnyMutation,
	text: string,
	attachments?: WireImageContent[],
	clientMessageId?: string,
	recoverOnly?: boolean,
): Promise<ChatSteerOutcome> {
	try {
		await steer.mutateAsync({ text, attachments, clientMessageId, recoverOnly });
		return { status: "accepted" };
	} catch (error) {
		const outcome = steerNonAcceptance(error);
		if (outcome) return outcome;
		throw error;
	}
}

/** The composer's retry and auto-resume surface for the current session. */
export function buildRetryControl(
	scope: ConversationActionScope,
	{ retryTurn, autoResume, trackedDispatch }: { retryTurn: AnyMutation; autoResume: AnyMutation; trackedDispatch: ConversationDispatchTracking | undefined },
) {
	const { sessionId } = scope;
	const retryTargetsCurrentSession = retryTurn.variables?.targetSessionId === sessionId;
	const autoResumeTargetsCurrentSession = autoResume.variables?.targetSessionId === sessionId;
	return {
		setAutoResume: (turnId: string, enabled: boolean) => {
			if (!sessionId) return Promise.reject(new Error("No conversation session is selected."));
			return autoResume.mutateAsync({ targetSessionId: sessionId, sourceTurnId: turnId, enabled });
		},
		autoResumePending: autoResume.isPending && autoResumeTargetsCurrentSession,
		autoResumeError: autoResumeTargetsCurrentSession && autoResume.error
			? apiErrorMessage(autoResume.error) : undefined,
		autoResumeTurnId: autoResumeTargetsCurrentSession ? autoResume.variables?.sourceTurnId : undefined,
		retry: (turnId: string) => retryConversationTurn(scope, retryTurn, turnId),
		pending:
			trackedDispatch?.operation === "retry" ||
			(retryTurn.isPending && retryTargetsCurrentSession),
		error:
			retryTargetsCurrentSession && retryTurn.error
				? apiErrorMessage(retryTurn.error)
				: undefined,
		turnId:
			trackedDispatch?.operation === "retry"
				? trackedDispatch.sourceTurnId
				: retryTargetsCurrentSession
					? retryTurn.variables?.sourceTurnId
					: undefined,
	};
}

export function compactUnavailableMessage(error: unknown): string | undefined {
	return apiErrorCode(error) === "CHAT_COMPACTION_UNSUPPORTED"
		? "This agent cannot compact its history"
		: apiErrorCode(error) === "CHAT_COMPACTION_BUSY"
			? "Stop the current turn before compacting"
			: undefined;
}
