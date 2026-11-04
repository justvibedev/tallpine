import { useCallback } from "react";
import {
	readChatSessionDraft,
	writeChatExcerptReferences,
	type ChatDraftExcerptReference,
	type ChatDraftScope,
} from "../../lib/chat-drafts";
import { type ConversationSnapshot } from "../../types/conversation";
import type { Dispatch, SetStateAction, RefObject } from "react";

/**
 * Capturing a transcript text selection and adding it to the composer as an excerpt.
 */
export function useTranscriptSelection({
	excerptsEnabled,
	setSelectionAction,
	scrollContent,
	snapshot,
	selectionAction,
	onAddToChat,
	draftScope,
	setDraftPersistenceError,
}: {
	excerptsEnabled: boolean | undefined;
	setSelectionAction: Dispatch<SetStateAction<{ excerpt: ChatDraftExcerptReference; range: Range; } | null>>;
	scrollContent: RefObject<HTMLDivElement | null>;
	snapshot: ConversationSnapshot;
	selectionAction: { excerpt: ChatDraftExcerptReference; range: Range; } | null;
	onAddToChat: ((excerpt: ChatDraftExcerptReference) => void) | undefined;
	draftScope: ChatDraftScope;
	setDraftPersistenceError: Dispatch<SetStateAction<string | undefined>>;
}) {
	const captureTranscriptSelection = useCallback(() => {
		const selection = window.getSelection();
		if (!excerptsEnabled || !selection || selection.isCollapsed || selection.rangeCount === 0) {
			setSelectionAction(null);
			return;
		}
		const anchor = selection.anchorNode instanceof Element
			? selection.anchorNode
			: selection.anchorNode?.parentElement;
		const focus = selection.focusNode instanceof Element
			? selection.focusNode
			: selection.focusNode?.parentElement;
		const source = anchor?.closest<HTMLElement>("[data-chat-message-id]");
		if (
			!source ||
			source !== focus?.closest<HTMLElement>("[data-chat-message-id]") ||
			!scrollContent.current?.contains(source)
		) {
			setSelectionAction(null);
			return;
		}
		const text = selection.toString().trim();
		const messageId = source.dataset.chatMessageId;
		const revision = Number(source.dataset.chatMessageRevision);
		const role = source.dataset.chatMessageRole;
		if (
			!text ||
			!messageId ||
			!Number.isSafeInteger(revision) ||
			(role !== "user" && role !== "assistant")
		) {
			setSelectionAction(null);
			return;
		}
		setSelectionAction({
			excerpt: {
				id: crypto.randomUUID(),
				conversationId: snapshot.conversationId,
				messageId,
				revision,
				text,
				role,
			},
			range: selection.getRangeAt(0).cloneRange(),
		});
	}, [excerptsEnabled, snapshot.conversationId]);

	const addSelectionToChat = useCallback(async () => {
		if (!selectionAction) return;
		if (onAddToChat) {
			// A host with its own composer (a side chat) owns where references go.
			onAddToChat(selectionAction.excerpt);
			setSelectionAction(null);
			window.getSelection()?.removeAllRanges();
			return;
		}
		const composer = readChatSessionDraft(draftScope).composer;
		// A pending delivery is cleared by revision on acceptance. Changing the
		// draft now would keep the sent text and excerpts for a second send.
		if (composer.delivery) {
			setSelectionAction(null);
			return;
		}
		const current = composer.excerpts ?? [];
		const duplicate = current.some(
			(item) =>
				item.messageId === selectionAction.excerpt.messageId &&
				item.revision === selectionAction.excerpt.revision &&
				item.text === selectionAction.excerpt.text,
		);
		const next = duplicate ? current : [...current, selectionAction.excerpt].slice(-8);
		const result = writeChatExcerptReferences(draftScope, next);
		setDraftPersistenceError(result.ok ? undefined : "chat.draft.saveFailed");
		setSelectionAction(null);
		window.getSelection()?.removeAllRanges();
	}, [draftScope, onAddToChat, selectionAction]);

	return { captureTranscriptSelection, addSelectionToChat };
}
