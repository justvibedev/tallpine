import { useCallback, useEffect, useRef, useState } from "react";
import {
	chatQueuedAttachmentScopeKey,
	readChatSessionDraft,
	writeChatQueuedEdit,
	type ChatDraftQueuedEdit,
	type ChatDraftAttachment,
	type ChatDraftRetainedAttachment,
	type ChatDraftScope,
} from "../../lib/chat-drafts";
import { purgeFileAttachments } from "../../hooks/useFileAttachments";
import { setChatDraftBoundary } from "../../lib/chat-draft-boundary";

/**
 * The queued-message edit draft and its durable persistence.
 */
export function useQueuedMessageEdit({
	draftPersistenceAvailable,
	draftScope,
	onCancelQueuedTurn,
	stableCancelQueuedTurn,
}: {
	draftPersistenceAvailable: boolean;
	draftScope: ChatDraftScope;
	onCancelQueuedTurn: ((turnId: string) => Promise<unknown>) | undefined;
	stableCancelQueuedTurn: (turnId: string) => Promise<unknown> | undefined;
}) {
	const [queueEdit, setQueueEdit] = useState<ChatDraftQueuedEdit | undefined>(
		() => (draftPersistenceAvailable ? readChatSessionDraft(draftScope).queuedEdit : undefined),
	);

	const queueEditRef = useRef(queueEdit);

	const [queueDraftError, setQueueDraftError] = useState<string>();

	const unprovenQueueWrite = useRef<{ expectedRevision?: string; written: { revisions: (string | undefined)[] } } | undefined>(undefined);

	const updateQueueDraft = useCallback((edit: Omit<ChatDraftQueuedEdit, "revision"> | undefined, expectedRevision?: string) => {
		const previous = unprovenQueueWrite.current;
		const result = writeChatQueuedEdit(draftScope, edit, expectedRevision, undefined,
			previous?.expectedRevision === expectedRevision ? previous?.written : undefined);
		setQueueDraftError(result.ok ? undefined : "chat.draft.queueSaveLocalFailed");
		if (result.ok) {
			const retired = queueEditRef.current;
			unprovenQueueWrite.current = undefined;
			queueEditRef.current = result.draft.queuedEdit;
			setQueueEdit(result.draft.queuedEdit);
			if (retired && (!result.draft.queuedEdit || retired.ownerId !== result.draft.queuedEdit.ownerId)) {
				purgeFileAttachments(chatQueuedAttachmentScopeKey(draftScope,
					`${retired.turnId}:${retired.ownerId ?? retired.expectedRevision ?? "legacy"}`));
			}
		} else if (result.attempted) {
			unprovenQueueWrite.current = { expectedRevision, written: result.attempted };
		}
		return result;
	}, [draftScope]);

	useEffect(() => {
		setChatDraftBoundary(draftScope.sessionId, "queued-edit", queueDraftError ? "persistence-failed" : undefined);
		return () => setChatDraftBoundary(draftScope.sessionId, "queued-edit", undefined);
	}, [queueDraftError, draftScope.sessionId]);

	// Text equality cannot prove an attachment-only edit was accepted. Keep an
	// uncertain edit and its original daemon revision until a save is acknowledged.
	const changeQueuedDraft = useCallback((text: string) => {
		const current = queueEditRef.current;
		if (!current || current.clientMessageId || current.turnId !== queueEdit?.turnId || current.ownerId !== queueEdit.ownerId || current.text === text) return;
		const result = updateQueueDraft({ ...current, text, saving: undefined, clientMessageId: undefined }, current.revision);
		if (!result.ok) {
			queueEditRef.current = { ...current, text };
			setQueueEdit(queueEditRef.current);
		}
	}, [queueEdit?.turnId, queueEdit?.ownerId, updateQueueDraft]);

	const changeQueuedAttachments = useCallback((field: "attachments" | "stagedAttachments", attachments: ChatDraftRetainedAttachment[] | ChatDraftAttachment[]) => {
		const current = queueEditRef.current;
		if (!current || current.clientMessageId || current.turnId !== queueEdit?.turnId || current.ownerId !== queueEdit.ownerId) return;
		if (JSON.stringify(current[field] ?? []) === JSON.stringify(attachments)) return;
		const next = { ...current, [field]: attachments, saving: undefined, clientMessageId: undefined };
		const result = updateQueueDraft(next, current.revision);
		if (!result.ok) {
			queueEditRef.current = next;
			setQueueEdit(next);
		}
	}, [queueEdit?.turnId, queueEdit?.ownerId, updateQueueDraft]);

	const changeQueuedStagedAttachments = useCallback((attachments: ChatDraftAttachment[]) => {
		changeQueuedAttachments("stagedAttachments", attachments);
	}, [changeQueuedAttachments]);

	const changeQueuedRetainedAttachments = useCallback((attachments: ChatDraftRetainedAttachment[]) => {
		changeQueuedAttachments("attachments", attachments);
	}, [changeQueuedAttachments]);

	const handleCancelQueuedTurn = useCallback(
		async (turnId: string) => {
			if (!onCancelQueuedTurn) return;
			if (queueEditRef.current?.turnId === turnId && queueEditRef.current.clientMessageId) return;
			await stableCancelQueuedTurn(turnId);
			const current = queueEditRef.current;
			if (current?.turnId === turnId && !current.clientMessageId) updateQueueDraft(undefined, current.revision);
		},
		[queueEdit, stableCancelQueuedTurn, updateQueueDraft],
	);

	return {
		queueEdit,
		queueEditRef,
		queueDraftError,
		updateQueueDraft,
		changeQueuedDraft,
		changeQueuedStagedAttachments,
		changeQueuedRetainedAttachments,
		handleCancelQueuedTurn,
	};
}
