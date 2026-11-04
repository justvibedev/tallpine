import type { ChatDraftInlineEdit } from "../../lib/chat-drafts";
import type { ChatEditOutcome, ChatSteerOutcome } from "../../types/conversation";
import { useLatestCallback } from "../../hooks/useStable";
/**
 * The Chat surface for a session whose persisted mode is `chat`.
 *
 * Rendered from a durable snapshot the daemon serves. The renderer stays thin:
 * items arrive already ordered by sequence and are never re-sorted here, turn
 * state comes from the daemon rather than being inferred from the last message,
 * and a decision is a typed action carrying the provider's request id.
 *
 * State this surface must be able to show, because each one happens: empty,
 * streaming, waiting on an approval, a command still running with no completion,
 * a turn the user stopped, a controller that died mid-turn, and history the user
 * has scrolled away from.
 */

import { useCallback } from "react";
import {
	beginChatInlineEditMutation,
	cancelChatInlineEditMutation,
	clearRejectedChatInlineEditDelivery,
	getChatInlineEditMutation,
	prepareChatInlineEditDelivery,
	writeChatInlineEdit,
	type ChatDraftScope,
	type ChatInlineEditDelivery,
} from "../../lib/chat-drafts";
import { type ConversationMessage } from "../../types/conversation";
import type { Dispatch, SetStateAction, RefObject } from "react";

/**
 * Start, update, cancel and submit actions for the inline message edit.
 */
export function useMessageEditActions({
	inlineEditLocked,
	draftScope,
	reconstructedTurns,
	messageEditRef,
	setMessageEdit,
	setInlineEditRecoveryNotice,
	setInlineEditOutcomeNotice,
	setDraftPersistenceError,
	onEditHumanMessage,
	inlineEditPending,
	durableInlineEditDelivery,
	setInlineEditUncertain,
	setDurableInlineEditDelivery,
	acceptAndClearInlineEditDelivery,
	editHumanMessage,
}: {
	inlineEditLocked: boolean;
	draftScope: ChatDraftScope;
	reconstructedTurns: Set<string>;
	messageEditRef: RefObject<ChatDraftInlineEdit | undefined>;
	setMessageEdit: Dispatch<SetStateAction<ChatDraftInlineEdit | undefined>>;
	setInlineEditRecoveryNotice: Dispatch<SetStateAction<string | undefined>>;
	setInlineEditOutcomeNotice: Dispatch<SetStateAction<string | undefined>>;
	setDraftPersistenceError: Dispatch<SetStateAction<string | undefined>>;
	onEditHumanMessage: ((turnId: string, text: string, clientMessageId?: string) => void | ChatEditOutcome | Promise<ChatEditOutcome | void>) | undefined;
	inlineEditPending: boolean;
	durableInlineEditDelivery: ChatInlineEditDelivery | undefined;
	setInlineEditUncertain: Dispatch<SetStateAction<boolean>>;
	setDurableInlineEditDelivery: Dispatch<SetStateAction<ChatInlineEditDelivery | undefined>>;
	acceptAndClearInlineEditDelivery: (delivery: ChatInlineEditDelivery, mutationToken?: symbol) => boolean;
	editHumanMessage: (turnId: string, text: string, clientMessageId?: string | undefined) => void | ChatSteerOutcome | Promise<void | ChatSteerOutcome> | undefined;
}) {
	const startMessageEdit = useCallback((message: ConversationMessage) => {
		if (!message.turnId || inlineEditLocked) return;
		const result = writeChatInlineEdit(draftScope, {
			turnId: message.turnId,
			text: message.text,
			content: message.content ?? [],
			reconstructedContext: reconstructedTurns.has(message.turnId),
		});
		const next = result.draft.inlineEdit;
		if (!next) return;
		messageEditRef.current = next;
		setMessageEdit(next);
		setInlineEditRecoveryNotice(undefined);
		setInlineEditOutcomeNotice(undefined);
		setDraftPersistenceError(
			result.ok
				? undefined
				: "chat.draft.editSaveFailed",
		);
	}, [draftScope, inlineEditLocked, reconstructedTurns]);

	const updateMessageEdit = useCallback((text: string) => {
		if (inlineEditLocked) return;
		const current = messageEditRef.current;
		if (!current) return;
		const result = writeChatInlineEdit(draftScope, {
			turnId: current.turnId,
			text,
			content: current.content,
			reconstructedContext: current.reconstructedContext,
		});
		const next = result.draft.inlineEdit;
		if (!next) return;
		messageEditRef.current = next;
		setMessageEdit(next);
		setInlineEditRecoveryNotice(undefined);
		setInlineEditOutcomeNotice(undefined);
		setDraftPersistenceError(
			result.ok
				? undefined
				: "chat.draft.editSaveFailed",
		);
	}, [draftScope, inlineEditLocked]);

	const cancelMessageEdit = useCallback(() => {
		if (inlineEditLocked) return;
		const result = writeChatInlineEdit(draftScope, undefined);
		if (!result.ok) {
			setDraftPersistenceError(
				"chat.draft.editDiscardFailed",
			);
			return;
		}
		messageEditRef.current = undefined;
		setMessageEdit(undefined);
		setInlineEditRecoveryNotice(undefined);
		setInlineEditOutcomeNotice(undefined);
		setDraftPersistenceError(undefined);
	}, [draftScope, inlineEditLocked]);

	const submitMessageEdit = useCallback(
		async (text: string) => {
			const current = messageEditRef.current;
			if (
				!current ||
				!onEditHumanMessage ||
				inlineEditPending ||
				(!durableInlineEditDelivery &&
					getChatInlineEditMutation(draftScope).accepted?.result.ok === false)
			) return;
				const prepared = prepareChatInlineEditDelivery(draftScope, {
					turnId: current.turnId,
					text,
					content: current.content,
					reconstructedContext: current.reconstructedContext,
					clientMessageId:
					durableInlineEditDelivery?.clientMessageId ?? crypto.randomUUID(),
			});
			if (!prepared.ok) {
				setDraftPersistenceError(
					"chat.draft.prepareEditFailed",
				);
				return;
			}
			const delivery = prepared.mutation;
			setInlineEditUncertain(false);
			setDurableInlineEditDelivery(delivery);
			setDraftPersistenceError(undefined);
			setInlineEditRecoveryNotice(
				delivery.state === "accepted"
					? "chat.draft.acceptedEdit"
					: undefined,
			);
			if (delivery.state === "accepted") {
				acceptAndClearInlineEditDelivery(delivery);
				return;
			}
			const mutationToken = beginChatInlineEditMutation(draftScope);
			if (!mutationToken) return;
			let mutationFinished = false;
			try {
				const outcome = await editHumanMessage(
					delivery.turnId,
					delivery.requestText,
					delivery.clientMessageId,
				);
				if (outcome?.status === "not-accepted") {
					const cleared = clearRejectedChatInlineEditDelivery(
						draftScope,
							delivery.clientMessageId,
							delivery.revision,
						);
						setDurableInlineEditDelivery(cleared.draft.inlineEditDelivery);
						if (cleared.ok) {
							setDraftPersistenceError(undefined);
							setInlineEditRecoveryNotice(undefined);
							setInlineEditOutcomeNotice(outcome.reason);
						} else {
						setDraftPersistenceError(
							"chat.draft.clearRefusedEditFailed",
						);
					}
					return;
				}
				acceptAndClearInlineEditDelivery(delivery, mutationToken);
				mutationFinished = true;
			} catch {
				setInlineEditUncertain(true);
				setInlineEditRecoveryNotice(
					"chat.draft.editUncertain",
				);
			} finally {
				if (!mutationFinished) {
					cancelChatInlineEditMutation(draftScope, mutationToken);
				}
			}
		},
		[
			acceptAndClearInlineEditDelivery,
			durableInlineEditDelivery,
			editHumanMessage,
			inlineEditPending,
			onEditHumanMessage,
			draftScope,
		],
	);

	// Both churn with their deps while streaming; a stable identity keeps memoized turn groups from re-rendering.
	const stableStartMessageEdit = useLatestCallback(startMessageEdit);

	const stableSubmitMessageEdit = useLatestCallback(submitMessageEdit);

	return {
		updateMessageEdit,
		cancelMessageEdit,
		submitMessageEdit,
		stableStartMessageEdit,
		stableSubmitMessageEdit,
	};
}
