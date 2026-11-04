import type { ChatDraftInlineEdit, ChatDraftMutationSnapshot } from "../../lib/chat-drafts";
import { useCallback, useEffect } from "react";
import {
	acknowledgeChatInlineEditMutation,
	cancelChatInlineEditMutation,
	clearAcceptedChatInlineEdit,
	clearUncertainChatInlineEditDelivery,
	finishChatInlineEditMutation,
	markChatInlineEditDeliveryAccepted,
	readChatSessionDraft,
	type DraftClearResult,
	type ChatDraftScope,
	type ChatInlineEditDelivery,
} from "../../lib/chat-drafts";
import type { Dispatch, SetStateAction, RefObject } from "react";

/**
 * Restoring, resolving and abandoning an inline edit whose delivery was interrupted.
 */
export function useInlineEditRecovery({
	draftPersistenceAvailable,
	draftScope,
	messageEditRef,
	setMessageEdit,
	setDurableInlineEditDelivery,
	setInlineEditUncertain,
	setDraftPersistenceError,
	setInlineEditRecoveryNotice,
	setAppliedEditAcceptanceSequence,
	setInlineEditOutcomeNotice,
	automaticInlineRecoveryAttempted,
	durableInlineEditDelivery,
	inlineEditMutation,
	appliedEditAcceptanceSequence,
}: {
	draftPersistenceAvailable: boolean;
	draftScope: ChatDraftScope;
	messageEditRef: RefObject<ChatDraftInlineEdit | undefined>;
	setMessageEdit: Dispatch<SetStateAction<ChatDraftInlineEdit | undefined>>;
	setDurableInlineEditDelivery: Dispatch<SetStateAction<ChatInlineEditDelivery | undefined>>;
	setInlineEditUncertain: Dispatch<SetStateAction<boolean>>;
	setDraftPersistenceError: Dispatch<SetStateAction<string | undefined>>;
	setInlineEditRecoveryNotice: Dispatch<SetStateAction<string | undefined>>;
	setAppliedEditAcceptanceSequence: Dispatch<SetStateAction<number>>;
	setInlineEditOutcomeNotice: Dispatch<SetStateAction<string | undefined>>;
	automaticInlineRecoveryAttempted: RefObject<string | undefined>;
	durableInlineEditDelivery: ChatInlineEditDelivery | undefined;
	inlineEditMutation: ChatDraftMutationSnapshot<string>;
	appliedEditAcceptanceSequence: number;
}) {
	useEffect(() => {
		const restored = draftPersistenceAvailable ? readChatSessionDraft(draftScope) : undefined;
		messageEditRef.current = restored?.inlineEdit;
		setMessageEdit(restored?.inlineEdit);
		setDurableInlineEditDelivery(restored?.inlineEditDelivery);
		setInlineEditUncertain(restored?.inlineEditDelivery?.state === "dispatching");
		setDraftPersistenceError(undefined);
		setInlineEditRecoveryNotice(
			restored?.inlineEditDelivery
				? restored.inlineEditDelivery.state === "accepted"
					? "chat.draft.acceptedEdit"
					: "chat.draft.editRestart"
				: undefined,
		);
		setAppliedEditAcceptanceSequence(0);
		setInlineEditOutcomeNotice(undefined);
		automaticInlineRecoveryAttempted.current = undefined;
	}, [draftPersistenceAvailable, draftScope]);

	const applyAcceptedInlineEditResult = useCallback((result: DraftClearResult) => {
		setDurableInlineEditDelivery(result.draft.inlineEditDelivery);
		if (!result.ok) {
			setInlineEditRecoveryNotice(
				"chat.draft.acceptedEdit",
			);
			setDraftPersistenceError(
				"chat.draft.clearEditFailed",
			);
			return;
		}
		setDraftPersistenceError(undefined);
		setInlineEditRecoveryNotice(undefined);
		if (!result.cleared) return;
		messageEditRef.current = undefined;
		setMessageEdit(undefined);
	}, []);

	const acceptAndClearInlineEditDelivery = useCallback(
		(delivery: ChatInlineEditDelivery, mutationToken?: symbol) => {
			const accepted = markChatInlineEditDeliveryAccepted(
				draftScope,
				delivery.clientMessageId,
				delivery.revision,
			);
			if (!accepted.ok) {
				if (mutationToken) {
					cancelChatInlineEditMutation(draftScope, mutationToken);
				}
				setDurableInlineEditDelivery(delivery);
				setDraftPersistenceError(
					"chat.draft.recordEditFailed",
				);
				return false;
			}
			setDurableInlineEditDelivery(
				accepted.draft.inlineEditDelivery ?? { ...delivery, state: "accepted" },
			);
			const result = clearAcceptedChatInlineEdit(draftScope, delivery.revision);
			if (mutationToken) {
				finishChatInlineEditMutation(
					draftScope,
					mutationToken,
					delivery.revision,
					result,
				);
				return result.ok;
			}
			applyAcceptedInlineEditResult(result);
			return result.ok;
		},
		[applyAcceptedInlineEditResult, draftScope],
	);

	const abandonUncertainInlineEdit = useCallback(() => {
		if (!durableInlineEditDelivery) return;
		const result = clearUncertainChatInlineEditDelivery(
			draftScope,
			durableInlineEditDelivery.clientMessageId,
			durableInlineEditDelivery.revision,
		);
		setDurableInlineEditDelivery(result.draft.inlineEditDelivery);
		if (!result.ok) {
			setDraftPersistenceError(
				"chat.draft.abandonEditFailed",
			);
			return;
		}
		setInlineEditUncertain(false);
		setDraftPersistenceError(undefined);
		setInlineEditRecoveryNotice(undefined);
		setInlineEditOutcomeNotice(
			"chat.draft.abandonedEdit",
		);
	}, [draftScope, durableInlineEditDelivery]);

	useEffect(() => {
		if (!durableInlineEditDelivery || durableInlineEditDelivery.state !== "accepted") return;
		if (
			automaticInlineRecoveryAttempted.current ===
			durableInlineEditDelivery.clientMessageId
		) return;
		automaticInlineRecoveryAttempted.current = durableInlineEditDelivery.clientMessageId;
		acceptAndClearInlineEditDelivery(durableInlineEditDelivery);
	}, [acceptAndClearInlineEditDelivery, durableInlineEditDelivery]);

	useEffect(() => {
		const accepted = inlineEditMutation.accepted;
		if (!accepted || accepted.sequence <= appliedEditAcceptanceSequence) return;
		applyAcceptedInlineEditResult(accepted.result);
		setAppliedEditAcceptanceSequence(accepted.sequence);
		acknowledgeChatInlineEditMutation(draftScope, accepted.sequence);
	}, [
		appliedEditAcceptanceSequence,
		applyAcceptedInlineEditResult,
		draftScope,
		inlineEditMutation.accepted,
	]);

	return { acceptAndClearInlineEditDelivery, abandonUncertainInlineEdit };
}
