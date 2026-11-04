/** Send-rejection codes, size limits and small pure helpers for the ChatComposer. */
import { type ChatComposerDelivery, type ChatDraftRetainedAttachment } from "../../../lib/chat-drafts";

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

/**
 * Tell the agent to open the attached files. Mirrors the wording spawn uses for a task
 * brief, so the same instruction reaches the agent whether a file was attached at
 * spawn or mid-conversation.
 */
export function withAttachmentReferences(text: string, paths: string[]): string {
	if (paths.length === 0) return text;
	const lead = text.trim() === "" ? "" : `${text}\n\n`;
	return `${lead}Attached files (read these files in the workspace):\n${paths.map((path) => `- ${path}`).join("\n")}`;
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
};

