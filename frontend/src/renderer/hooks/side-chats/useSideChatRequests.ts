/* Side-chat interrupt, compact, resolve, settings, retry and queued-edit requests, split out of useSideChats (B303). */

import { useCallback } from "react";
import type { Dispatch, SetStateAction } from "react";
import { apiErrorMessage } from "../../lib/api-client";
import {
	compactSideChat,
	editQueuedSideTurn,
	interruptSideChat,
	resolveSideApproval,
	resolveSideInput,
	retrySideTurn,
	updateSideSettings,
} from "../../lib/side-chats-api";

type Setter<T> = Dispatch<SetStateAction<T>>;

export function useSideChatRequests({
	sessionId, setError, updateDraft, inputAnswers, editingTurnId, editingText, setEditingTurnId,
}: {
	sessionId: string;
	setError: Setter<string | undefined>;
	updateDraft: (sideId: string, text: string) => void;
	inputAnswers: Record<string, Record<string, string>>;
	editingTurnId: string | undefined;
	editingText: string;
	setEditingTurnId: Setter<string | undefined>;
}) {
	const interrupt = useCallback(
		async (sideId: string) => {
			const { error: requestError } = await interruptSideChat(sessionId, sideId);
			if (requestError) setError(apiErrorMessage(requestError));
		},
		[sessionId],
	);
	const compact = useCallback(
		async (sideId: string) => {
			setError(undefined);
			const { error: requestError } = await compactSideChat(sessionId, sideId);
			if (requestError) {
				setError(apiErrorMessage(requestError));
				return;
			}
			updateDraft(sideId, "");
		},
		[sessionId, updateDraft],
	);
	const resolveApproval = useCallback(
		async (sideId: string, requestId: string, decisionId: string) => {
			const { error: requestError } = await resolveSideApproval(sessionId, sideId, requestId, decisionId);
			if (requestError) setError(apiErrorMessage(requestError));
		},
		[sessionId],
	);
	const resolveInput = useCallback(
		async (sideId: string, requestId: string, action: "accept" | "decline" | "cancel") => {
			const { error: requestError } = await resolveSideInput(
				sessionId,
				sideId,
				requestId,
				action,
				action === "accept" ? (inputAnswers[requestId] ?? {}) : {},
			);
			if (requestError) setError(apiErrorMessage(requestError));
		},
		[sessionId, inputAnswers],
	);
	const updateSettings = useCallback(
		async (sideId: string, model: string, effort: string) => {
			const { error: requestError } = await updateSideSettings(sessionId, sideId, model, effort);
			if (requestError) setError(apiErrorMessage(requestError));
		},
		[sessionId],
	);
	const retry = useCallback(
		async (sideId: string, turnId: string) => {
			const { error: requestError } = await retrySideTurn(sessionId, sideId, turnId);
			if (requestError) setError(apiErrorMessage(requestError));
		},
		[sessionId],
	);
	const saveQueuedEdit = useCallback(
		async (sideId: string) => {
			if (!editingTurnId || !editingText.trim()) return;
			const { error: requestError } = await editQueuedSideTurn(sessionId, sideId, editingTurnId, editingText);
			if (requestError) {
				setError(apiErrorMessage(requestError));
				return;
			}
			setEditingTurnId(undefined);
		},
		[sessionId, editingTurnId, editingText],
	);
	return { interrupt, compact, resolveApproval, resolveInput, updateSettings, retry, saveQueuedEdit };
}
