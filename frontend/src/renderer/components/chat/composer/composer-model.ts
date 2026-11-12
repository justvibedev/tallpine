/** Send-rejection codes, size limits and small pure helpers for the ChatComposer. */
import { type FileAttachment } from "../../../hooks/useFileAttachments";
import { type ChatComposerDelivery, type ChatDraftAttachment, type ChatDraftAttachmentResize, type ChatDraftRetainedAttachment } from "../../../lib/chat-drafts";

// These responses precede AppendUserMessage. Provider/transport errors can
// follow durable acceptance and must keep the original delivery ID for recovery.
/** The agent switch that runs before a send failed, so the message never left. */
export const AGENT_SWITCH_FAILED = "AGENT_SWITCH_FAILED";
export const DEFINITIVE_SEND_REJECTIONS = new Set([
	AGENT_SWITCH_FAILED,
	// Cloud validation failures reject the request before durable acceptance.
	"validation_error",
	"INVALID_BODY",
	"CHAT_MESSAGE_EMPTY",
	"INVALID_RESOURCE",
	"UNSUPPORTED_ATTACHMENT_TYPE",
	"INVALID_ATTACHMENT_DATA",
	"ATTACHMENT_TOO_LARGE",
	"TOO_MANY_ATTACHMENTS",
	"ATTACHMENTS_TOO_LARGE",
	"SESSION_NOT_FOUND",
	"SESSION_MODE_MISMATCH",
	"CHAT_CONTROLLER_NOT_READY",
	"CHAT_RESUME_FAILED",
	"CHAT_INTERFACE_TRANSITION",
	// Excerpts are verified before AppendUserMessage, so retrying the same
	// references can only fail again; return them to the draft for removal.
	"CHAT_EXCERPT_INVALID",
	"CHAT_EXCERPT_STALE",
	// A /btw or /side request the daemon refused never opened a side chat, so a
	// retry cannot succeed; return the draft instead of locking the composer.
	"CHAT_SIDE_UNAVAILABLE",
	"CHAT_SIDE_UNSUPPORTED",
	"CHAT_SIDE_ANCHOR_UNAVAILABLE",
	"CHAT_SIDE_INVALID_REQUEST",
	"CHAT_SIDE_NOT_READY",
	"CHAT_SIDE_RECREATE_REQUIRED",
	"CHAT_SIDE_CLOSED",
	"CHAT_SIDE_IDEMPOTENCY_CONFLICT",
]);

// Native image blocks are persisted with the chat turn and sent to the provider.
// Larger attachments still reach the agent through their staged workspace paths.
/** How long a sent draft stays hidden before it reappears if the daemon has not answered. */
export const SEND_CONCEAL_MS = 3000;
export const MAX_NATIVE_IMAGE_BYTES = 10 * 1024 * 1024;
export const MAX_NATIVE_IMAGES_BYTES = 25 * 1024 * 1024;

/** A saved draft descriptor back as an attachment, keeping its resize note and original link. */
export function attachmentFromDraft(attachment: ChatDraftAttachment): FileAttachment {
	return {
		id: attachment.id,
		name: attachment.name,
		mimeType: attachment.mimeType,
		bytes: attachment.bytes,
		stagedPath: attachment.path,
		...(attachment.resize ? { resize: attachment.resize } : {}),
	};
}

/** The part of a downscaled image worth saving with the draft: its note and original link. */
export function draftResize({ resize }: Pick<FileAttachment, "resize">): { resize?: ChatDraftAttachmentResize } {
	return resize?.originalPath
		? { resize: { note: resize.note, originalPath: resize.originalPath, originalBytes: resize.originalBytes } }
		: {};
}

/** Each downscaled image's staged path mapped to its full original, for the placeholder block. */
export function resizeOriginals(
	attachments: ReadonlyArray<Partial<Pick<FileAttachment, "stagedPath" | "resize">>>,
): Record<string, string> {
	return Object.fromEntries(attachments.flatMap(({ stagedPath, resize }) =>
		stagedPath && resize?.originalPath ? [[stagedPath, resize.originalPath]] : []));
}

export function restoredDeliveryNotice(delivery: ChatComposerDelivery | undefined): string | null {
	if (!delivery) return null;
	if (delivery.state === "accepted") {
		return "chat.draft.acceptedMessage";
	}
	return delivery.kind === "steer"
		? "chat.draft.steerRestart"
		: "chat.draft.sendRestart";
}
/** A retained server-owned attachment; image bytes stay in durable storage. */
export type StoredComposerAttachment = ChatDraftRetainedAttachment & { dataUrl?: string };
export type ChatComposerHandle = {
	focus(): void;
	isEmpty(): boolean;
	/** Replaces the draft with text, saved like typed input, and focuses the editor. */
	setDraft(text: string): void;
	/** Attaches dropped files; false when attaching is unavailable right now. */
	attachFiles(files: File[]): boolean;
};

