import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import {
	getChatInlineEditMutation,
	readChatSessionDraft,
	subscribeChatDraftRuntime,
	type ChatDraftScope,
	type ChatInlineEditDelivery,
} from "../../lib/chat-drafts";
import { setChatDraftBoundary } from "../../lib/chat-draft-boundary";
import { MessageEditDraft } from "./ChatWorkspaceProps";

/**
 * Inline message-edit draft, its durable delivery state and recovery labels.
 */
export function useInlineEditState({ draftPersistenceAvailable, draftScope, editPending }: {
	draftPersistenceAvailable: boolean;
	draftScope: ChatDraftScope;
	editPending: boolean | undefined;
}) {
	const [messageEdit, setMessageEdit] = useState<MessageEditDraft | undefined>(
		() => (draftPersistenceAvailable ? readChatSessionDraft(draftScope).inlineEdit : undefined),
	);

	const messageEditRef = useRef(messageEdit);

	const [durableInlineEditDelivery, setDurableInlineEditDelivery] =
		useState<ChatInlineEditDelivery | undefined>(
			() => (draftPersistenceAvailable ? readChatSessionDraft(draftScope).inlineEditDelivery : undefined),
		);

	const [inlineEditUncertain, setInlineEditUncertain] = useState(
		() => draftPersistenceAvailable && readChatSessionDraft(draftScope).inlineEditDelivery?.state === "dispatching",
	);

	const automaticInlineRecoveryAttempted = useRef<string | undefined>(undefined);

	const [draftPersistenceError, setDraftPersistenceError] = useState<string>();

	const [inlineEditRecoveryNotice, setInlineEditRecoveryNotice] = useState<string>();

	const [inlineEditOutcomeNotice, setInlineEditOutcomeNotice] = useState<string>();

	const subscribeInlineEditMutation = useCallback(
		(listener: () => void) => subscribeChatDraftRuntime(draftScope, listener),
		[draftScope],
	);

	const getInlineEditMutation = useCallback(
		() => getChatInlineEditMutation(draftScope),
		[draftScope],
	);

	const inlineEditMutation = useSyncExternalStore(
		subscribeInlineEditMutation,
		getInlineEditMutation,
		getInlineEditMutation,
	);

	const [appliedEditAcceptanceSequence, setAppliedEditAcceptanceSequence] = useState(0);

	const acceptedEditWaiting = Boolean(
		inlineEditMutation.accepted &&
			inlineEditMutation.accepted.sequence > appliedEditAcceptanceSequence,
	);

	const acceptedEditClearFailed = inlineEditMutation.accepted?.result.ok === false;

	const inlineEditPending = Boolean(editPending) || inlineEditMutation.pending || acceptedEditWaiting;

	const inlineEditLocked = inlineEditPending || Boolean(durableInlineEditDelivery);

	const inlineEditSendBlocked =
		inlineEditPending || (acceptedEditClearFailed && !durableInlineEditDelivery);

	const inlineEditRecoveryLabel = durableInlineEditDelivery
		? durableInlineEditDelivery.state === "accepted"
			? "chat.draft.clearEdit"
			: "chat.draft.retryEdit"
		: undefined;

	const canAbandonInlineEditRecovery = Boolean(
		inlineEditUncertain && durableInlineEditDelivery?.state === "dispatching",
	);

	useEffect(() => {
		setChatDraftBoundary(
			draftScope.sessionId,
			"inline-edit",
			[
				...(draftPersistenceError ? (["persistence-failed"] as const) : []),
			],
		);
	}, [draftPersistenceError, draftScope.sessionId]);

	useEffect(
		() => () => setChatDraftBoundary(draftScope.sessionId, "inline-edit", undefined),
		[draftScope.sessionId],
	);

	return {
		messageEdit,
		setMessageEdit,
		messageEditRef,
		durableInlineEditDelivery,
		setDurableInlineEditDelivery,
		setInlineEditUncertain,
		automaticInlineRecoveryAttempted,
		draftPersistenceError,
		setDraftPersistenceError,
		inlineEditRecoveryNotice,
		setInlineEditRecoveryNotice,
		inlineEditOutcomeNotice,
		setInlineEditOutcomeNotice,
		inlineEditMutation,
		appliedEditAcceptanceSequence,
		setAppliedEditAcceptanceSequence,
		inlineEditPending,
		inlineEditLocked,
		inlineEditSendBlocked,
		inlineEditRecoveryLabel,
		canAbandonInlineEditRecovery,
	};
}
