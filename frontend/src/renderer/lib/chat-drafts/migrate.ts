/** Validation and normalisation of stored draft shapes. */
import type { ConversationContentSummary } from "../../types/conversation";
import { ChatDraftScopeInput, ChatSessionDraft, CHAT_DRAFT_SCHEMA_VERSION, ChatDraftAttachment, ChatDraftAttachmentResize, ChatDraftExcerptReference, ChatDraftInlineEdit, ChatDraftDeliveryState, ChatComposerDelivery, ChatInlineEditDelivery, ChatDraftScope } from "./types";
import { normalizeScope } from "./store";

export function emptyDraft(scope: ChatDraftScopeInput): ChatSessionDraft {
	const identity = normalizeScope(scope);
	return {
		schemaVersion: CHAT_DRAFT_SCHEMA_VERSION,
		sessionId: identity.sessionId,
		incarnation: identity.incarnation,
		composer: { revision: 0, text: "", attachments: [] },
	};
}

export function isContentSummary(value: unknown): value is ConversationContentSummary {
	if (!value || typeof value !== "object") return false;
	const content = value as Partial<ConversationContentSummary>;
	return (
		typeof content.type === "string" &&
		(content.mimeType === undefined || typeof content.mimeType === "string") &&
		(content.uri === undefined || typeof content.uri === "string") &&
		(content.name === undefined || typeof content.name === "string")
	);
}

export function isAttachment(value: unknown): value is ChatDraftAttachment {
	if (!value || typeof value !== "object") return false;
	const attachment = value as Partial<ChatDraftAttachment>;
	return (
		typeof attachment.id === "string" &&
		attachment.id.length > 0 &&
		typeof attachment.path === "string" &&
		(attachment.path === "" || /^\.(?:tallpine|ao)\/attachments\//.test(attachment.path)) &&
		typeof attachment.name === "string" &&
		typeof attachment.mimeType === "string" &&
		typeof attachment.bytes === "number" &&
		Number.isFinite(attachment.bytes) &&
		attachment.bytes >= 0 &&
		(attachment.resize === undefined || isAttachmentResize(attachment.resize))
	);
}

function isAttachmentResize(value: unknown): value is ChatDraftAttachmentResize {
	if (!value || typeof value !== "object") return false;
	const resize = value as Partial<ChatDraftAttachmentResize>;
	return (
		typeof resize.note === "string" &&
		typeof resize.originalPath === "string" &&
		/^\.(?:tallpine|ao)\/attachments\//.test(resize.originalPath) &&
		typeof resize.originalBytes === "number" &&
		Number.isFinite(resize.originalBytes) &&
		resize.originalBytes >= 0
	);
}

export function isExcerptReference(value: unknown): value is ChatDraftExcerptReference {
	if (!value || typeof value !== "object") return false;
	const excerpt = value as Partial<ChatDraftExcerptReference>;
	return (
		typeof excerpt.id === "string" && excerpt.id.length > 0 &&
		typeof excerpt.conversationId === "string" && excerpt.conversationId.length > 0 &&
		typeof excerpt.messageId === "string" && excerpt.messageId.length > 0 &&
		typeof excerpt.revision === "number" && Number.isSafeInteger(excerpt.revision) && excerpt.revision >= 0 &&
		typeof excerpt.text === "string" && excerpt.text.trim().length > 0 &&
		(excerpt.role === "user" || excerpt.role === "assistant")
	);
}

export function isInlineEdit(value: unknown): value is ChatDraftInlineEdit {
	if (!value || typeof value !== "object") return false;
	const edit = value as Partial<ChatDraftInlineEdit>;
	return (
		typeof edit.revision === "string" &&
		edit.revision.length > 0 &&
			typeof edit.turnId === "string" &&
			edit.turnId.length > 0 &&
			typeof edit.text === "string" &&
			(edit.reconstructedContext === undefined ||
				typeof edit.reconstructedContext === "boolean") &&
			Array.isArray(edit.content) &&
			edit.content.every(isContentSummary)
	);
}

export function isDeliveryState(value: unknown): value is ChatDraftDeliveryState {
	return value === "dispatching" || value === "accepted";
}

export function isComposerDelivery(value: unknown): value is ChatComposerDelivery {
	if (!value || typeof value !== "object") return false;
	const delivery = value as Partial<ChatComposerDelivery>;
	return (
		(delivery.kind === "send" || delivery.kind === "steer") &&
		isDeliveryState(delivery.state) &&
		typeof delivery.revision === "number" &&
		Number.isInteger(delivery.revision) &&
		delivery.revision >= 0 &&
		typeof delivery.clientMessageId === "string" &&
		delivery.clientMessageId.length > 0 &&
		typeof delivery.requestText === "string" &&
		(delivery.excerpts === undefined ||
			(Array.isArray(delivery.excerpts) && delivery.excerpts.every(isExcerptReference))) &&
		(!("nativeImages" in delivery) || typeof delivery.nativeImages === "boolean") &&
		(delivery.draft === undefined || (delivery.draft !== null &&
			typeof delivery.draft === "object" &&
			typeof delivery.draft.text === "string" &&
			Array.isArray(delivery.draft.attachments) &&
			delivery.draft.attachments.every(isAttachment)))
	);
}

export function isInlineEditDelivery(value: unknown): value is ChatInlineEditDelivery {
	if (!value || typeof value !== "object") return false;
	const delivery = value as Partial<ChatInlineEditDelivery>;
	return (
		delivery.kind === "inline-edit" &&
		isDeliveryState(delivery.state) &&
		typeof delivery.revision === "string" &&
		delivery.revision.length > 0 &&
		typeof delivery.clientMessageId === "string" &&
		delivery.clientMessageId.length > 0 &&
		typeof delivery.turnId === "string" &&
		delivery.turnId.length > 0 &&
		typeof delivery.requestText === "string"
	);
}

export function draftEditRevision(): string {
	if (typeof crypto !== "undefined" && "randomUUID" in crypto) return crypto.randomUUID();
	return `${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

export function isChatSessionDraft(value: unknown, scope: ChatDraftScope): value is ChatSessionDraft {
	if (!value || typeof value !== "object") return false;
	const draft = value as Partial<ChatSessionDraft>;
	const composer = draft.composer as Partial<ChatSessionDraft["composer"]> | undefined;
	return (
		draft.schemaVersion === CHAT_DRAFT_SCHEMA_VERSION &&
		draft.sessionId === scope.sessionId &&
		draft.incarnation === scope.incarnation &&
		Boolean(composer) &&
		typeof composer?.revision === "number" &&
		Number.isInteger(composer.revision) &&
		composer.revision >= 0 &&
		typeof composer.text === "string" &&
		Array.isArray(composer.attachments) &&
		composer.attachments.every(isAttachment) &&
		(composer.excerpts === undefined ||
			(Array.isArray(composer.excerpts) && composer.excerpts.every(isExcerptReference))) &&
		(composer.delivery === undefined || isComposerDelivery(composer.delivery)) &&
		(draft.queuedEdit === undefined || (draft.queuedEdit !== null &&
			typeof draft.queuedEdit.revision === "string" && draft.queuedEdit.revision.length > 0 &&
			typeof draft.queuedEdit.turnId === "string" && draft.queuedEdit.turnId.length > 0 &&
			typeof draft.queuedEdit.text === "string" &&
			(draft.queuedEdit.nativeImages === undefined || typeof draft.queuedEdit.nativeImages === "boolean") &&
			(draft.queuedEdit.clientMessageId === undefined || typeof draft.queuedEdit.clientMessageId === "string") &&
			(draft.queuedEdit.ownerId === undefined || typeof draft.queuedEdit.ownerId === "string") &&
			(draft.queuedEdit.expectedRevision === undefined || (Number.isSafeInteger(draft.queuedEdit.expectedRevision) && draft.queuedEdit.expectedRevision >= 0)) &&
			(draft.queuedEdit.stagedAttachments === undefined || (Array.isArray(draft.queuedEdit.stagedAttachments) && draft.queuedEdit.stagedAttachments.every(isAttachment))) &&
			(draft.queuedEdit.attachments === undefined || (Array.isArray(draft.queuedEdit.attachments) && draft.queuedEdit.attachments.every((item) =>
				item !== null && typeof item === "object" && typeof item.id === "string" && typeof item.name === "string" &&
				(item.path === undefined || typeof item.path === "string") &&
				(item.contentIndex === undefined || (Number.isSafeInteger(item.contentIndex) && item.contentIndex >= 0)) &&
				(item.contentType === undefined || typeof item.contentType === "string")))) &&
			(draft.queuedEdit.saving === undefined || typeof draft.queuedEdit.saving === "boolean")
		)) &&
		(draft.inlineEdit === undefined || isInlineEdit(draft.inlineEdit)) &&
		(draft.inlineEditDelivery === undefined || isInlineEditDelivery(draft.inlineEditDelivery))
	);
}
