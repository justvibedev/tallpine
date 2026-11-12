import type { ChatSteerOutcome, ConversationSnapshot, QueuedMessageEditOptions } from "../../types/conversation";
import type { DraftWriteResult } from "../../lib/chat-drafts";
import { useCallback, useMemo, RefObject } from "react";
import {
	type ChatDraftQueuedEdit,
	type ChatDraftExcerptReference,
	type ChatDraftScope,
} from "../../lib/chat-drafts";
import { apiErrorCode } from "../../lib/api-client";
import { useStableCallback } from "./useStableCallback";
import { type StoredComposerAttachment } from "./composer/composer-model";
import { stagedAttachmentParts, attachmentName, isImageAttachment } from "@tallpine/shared/chat/attachments";
import { pathsForPlaceholders } from "@tallpine/shared/chat/image-placeholders";
import { type QueuedMessage } from "./QueuedMessageDock";

/**
 * Sending, steering and queue actions behind the composer.
 */
export function useComposerSend({
	onSend,
	queueEdit,
	onEditQueuedTurn,
	queuedMessages,
	queueEditRef,
	updateQueueDraft,
	nativeImages,
	draftScope,
	onInterrupt,
	onSteer,
	newWorkDisabled,
	stablePromoteQueuedTurn,
	snapshot,
}: {
	onSend: ((text: string, attachments?: { mimeType: string; data: string; }[], clientMessageId?: string, excerpts?: ChatDraftExcerptReference[]) => void | Promise<unknown>) | undefined;
	queueEdit: ChatDraftQueuedEdit | undefined;
	onEditQueuedTurn: ((turnId: string, text: string, options?: QueuedMessageEditOptions) => Promise<unknown>) | undefined;
	queuedMessages: QueuedMessage[];
	queueEditRef: RefObject<ChatDraftQueuedEdit | undefined>;
	updateQueueDraft: (edit: Omit<ChatDraftQueuedEdit, "revision"> | undefined, expectedRevision?: string) => DraftWriteResult & { attempted?: { revisions: (string | undefined)[]; }; };
	nativeImages: boolean | undefined;
	draftScope: ChatDraftScope;
	onInterrupt: (() => void) | undefined;
	onSteer: ((text: string, attachments?: { mimeType: string; data: string; }[], clientMessageId?: string, recoverOnly?: boolean) => Promise<ChatSteerOutcome | void>) | undefined;
	newWorkDisabled: boolean;
	stablePromoteQueuedTurn: (turnId: string) => Promise<unknown> | undefined;
	snapshot: ConversationSnapshot;
}) {
	// Keep the dispatch target with this composer instance while attachment staging
	// awaits. A newer queue editor must not redirect an older ordinary send.
	const handleComposerSend = useCallback(
		async (text: string, attachments?: Parameters<NonNullable<typeof onSend>>[1], clientMessageId?: string, retainedContent?: number[], excerpts?: ChatDraftExcerptReference[]) => {
			if (queueEdit) {
				if (!onEditQueuedTurn) {
					throw new Error("chat.draft.queueUnavailable");
				}
				if (!queueEdit.clientMessageId && !queuedMessages.some((entry) => entry.turnId === queueEdit.turnId)) {
					throw new Error("chat.draft.queueMissing");
				}
				const currentEdit = queueEditRef.current;
				if (!currentEdit || currentEdit.turnId !== queueEdit.turnId || currentEdit.ownerId !== queueEdit.ownerId) {
					throw new Error("chat.draft.queueReplaced");
				}
				const prepared = updateQueueDraft({
					...currentEdit,
					saving: true,
					clientMessageId: currentEdit.clientMessageId ?? crypto.randomUUID(),
					nativeImages: currentEdit.nativeImages ?? Boolean(nativeImages),
				}, currentEdit.revision);
				if (!prepared.ok || !prepared.draft.queuedEdit) throw new Error("chat.draft.queuePrepareFailed");
				try {
					await onEditQueuedTurn(queueEdit.turnId, text, {
						clientMessageId: prepared.draft.queuedEdit.clientMessageId,
						...(attachments?.length ? { attachments } : {}),
						retainedContent: queueEdit.attachments === undefined ? undefined : retainedContent,
						expectedRevision: queueEdit.expectedRevision,
					});
				} catch (error) {
					// These outcomes follow the daemon's atomic receipt lookup, so even
					// a recovered retry proves this exact edit was never accepted.
					if ([
						"CHAT_TURN_NOT_QUEUED",
						"CHAT_QUEUED_EDIT_CONFLICT",
						"CHAT_QUEUED_CONTENT_INVALID",
						"CHAT_QUEUED_TEXT_REQUIRED",
					].includes(apiErrorCode(error) ?? "")) {
						updateQueueDraft({
							...prepared.draft.queuedEdit,
							clientMessageId: undefined, saving: undefined, nativeImages: undefined,
						}, prepared.draft.queuedEdit.revision);
					}
					throw error;
				}
				if (queueEditRef.current?.ownerId === queueEdit.ownerId) {
					if (!updateQueueDraft(undefined, prepared.draft.queuedEdit.revision).ok) throw new Error("chat.draft.queueClearFailed");
				}
				return;
			}
			return onSend?.(text, attachments, clientMessageId, excerpts);
		},
		[draftScope, onEditQueuedTurn, onSend, nativeImages, queueEdit, queuedMessages, updateQueueDraft],
	);

	const stableInterrupt = useStableCallback(onInterrupt);

	const stableSteer = useStableCallback(onSteer);

	const beginQueuedEdit = useCallback(
		(turnId: string, text: string) => {
			if (newWorkDisabled || queueEditRef.current?.clientMessageId) return;
			const message = queuedMessages.find((queued) => queued.turnId === turnId)?.message;
			if (!message) return;
			const parts = stagedAttachmentParts(text);
			// Paths and native blocks have no shared persisted identity.
			const attachments: StoredComposerAttachment[] = parts.attachments.map((path) => ({
				id: path, name: attachmentName(path), path,
				...(parts.originals?.[path] ? { originalPath: parts.originals[path] } : {}),
			}));
			(message.content ?? []).forEach((item, index) => {
				attachments.push({ id: `content-${index}`, contentIndex: index, contentType: item.type,
					name: item.name || (item.type === "image" ? `Image ${index + 1}` : item.uri || "Attachment") });
			});
			updateQueueDraft({ turnId, ownerId: crypto.randomUUID(), text: pathsForPlaceholders(parts.body, parts.attachments.filter(isImageAttachment)), expectedRevision: message.revision, attachments, stagedAttachments: [] });
		},
		[newWorkDisabled, queuedMessages, updateQueueDraft],
	);

	const cancelQueuedEdit = useCallback(() => {
		const current = queueEditRef.current;
		if (current && !current.clientMessageId) updateQueueDraft(undefined, current.revision);
	}, [updateQueueDraft]);

	const promoteQueuedTurn = useCallback(
		async (turnId: string) => stablePromoteQueuedTurn(turnId),
		[stablePromoteQueuedTurn],
	);

	const steer = useCallback(
		async (
			text: string,
			attachments?: { mimeType: string; data: string }[],
			clientMessageId?: string,
			recoverOnly?: boolean,
		) => stableSteer(text, attachments, clientMessageId, recoverOnly),
		[stableSteer],
	);

	const acceptedClientMessageIds = useMemo(
		() =>
			new Set(
				snapshot.items.flatMap((item) => {
					// A sent message's own row proves it arrived, as a steer's activity does.
					if (item.kind === "message") {
						return item.role === "user" && item.origin === "human" && item.clientMessageId ? [item.clientMessageId] : [];
					}
					if (
						item.kind !== "activity" ||
						item.detail?.event !== "steer" ||
						!item.detail.clientMessageId
					) return [];
					return [item.detail.clientMessageId];
				}),
			),
		[snapshot.items],
	);

	return {
		handleComposerSend,
		stableInterrupt,
		beginQueuedEdit,
		cancelQueuedEdit,
		promoteQueuedTurn,
		steer,
		acceptedClientMessageIds,
	};
}
