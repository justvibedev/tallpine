/** Chat draft types and schema versions. */
import type { ConversationContentSummary } from "../../types/conversation";

export const CHAT_DRAFT_SCHEMA_VERSION = 2 as const;
export const CHAT_DRAFT_SCOPE_SCHEMA_VERSION = 1 as const;

/**
 * One immutable Tallpine session incarnation. Session ids are human-stable handles and
 * may be reused after authoritative deletion; createdAt (or another daemon-owned
 * UUID) is what prevents an old renderer draft from entering the replacement.
 */
export interface ChatDraftScope {
	sessionId: string;
	incarnation: string;
}

export type ChatDraftScopeInput = string | ChatDraftScope;

export type ChatDraftScopeLease = {
	schemaVersion: typeof CHAT_DRAFT_SCOPE_SCHEMA_VERSION;
	sessionId: string;
	incarnation: string;
	state: "active" | "activating";
	previousIncarnation?: string;
};

export type ChatDraftScopeActivationResult =
	| { ok: true; replaced: boolean; previousIncarnation?: string }
	| { ok: false; reason: "obsolete" | "storage" };

export interface ChatDraftAttachment {
	id: string;
	path: string;
	name: string;
	mimeType: string;
	bytes: number;
	/** Set when an oversized image was downscaled; links the staged full original. */
	resize?: ChatDraftAttachmentResize;
}

export interface ChatDraftAttachmentResize {
	note: string;
	originalPath: string;
	originalBytes: number;
}

export interface ChatDraftInlineEdit {
	/** Changes whenever the inline edit changes. Used for accepted-edit CAS. */
	revision: string;
	turnId: string;
	text: string;
	content: ConversationContentSummary[];
	/** True when the edit replays portable history instead of using a native fork. */
	reconstructedContext?: boolean;
}

export interface ChatDraftQueuedEdit {
	/** Original request mode, independent of current controller capabilities. */
	nativeImages?: boolean;
	/** Stable daemon receipt key for the exact pending save. */
	clientMessageId?: string;
	/** Stable for one editing attempt, including remounts and staged reads. */
	ownerId?: string;
	revision: string;
	turnId: string;
	text: string;
	/** Daemon message revision, held fixed across retries of this edit. */
	expectedRevision?: number;
	attachments?: ChatDraftRetainedAttachment[];
	stagedAttachments?: ChatDraftAttachment[];
	/** The daemon may have saved this exact edit before the response was lost. */
	saving?: boolean;
}

/** Server-owned content selected for retention; never stores attachment bytes. */
export interface ChatDraftRetainedAttachment {
	id: string;
	name: string;
	path?: string;
	contentIndex?: number;
	contentType?: string;
}

export type ChatDraftDeliveryState = "dispatching" | "accepted";

export interface ChatComposerDelivery {
	/** Original request mode, kept across capability changes during recovery. */
	nativeImages?: boolean;
	kind: "send" | "steer";
	state: ChatDraftDeliveryState;
	/** Exact composer revision this delivery was created from. */
	revision: number;
	/** Stable daemon idempotency key. Reused for every recovery attempt. */
	clientMessageId: string;
	/** Exact API text, including durable attachment path references. */
	requestText: string;
	/** Sent draft, separate from the editable next draft during optimistic delivery. */
	draft?: { text: string; attachments: ChatDraftAttachment[] };
	/** Exact verified transcript excerpts attached to this delivery. */
	excerpts?: ChatDraftExcerptReference[];
}

export interface ChatDraftExcerptReference {
	id: string;
	conversationId: string;
	messageId: string;
	revision: number;
	text: string;
	role: "user" | "assistant";
}

export interface ChatInlineEditDelivery {
	kind: "inline-edit";
	state: ChatDraftDeliveryState;
	/** Exact inline-edit revision this delivery was created from. */
	revision: string;
	clientMessageId: string;
	turnId: string;
	requestText: string;
}

export type ChatDraftInlineEditInput = Omit<ChatDraftInlineEdit, "revision">;

export interface ChatSessionDraft {
	schemaVersion: typeof CHAT_DRAFT_SCHEMA_VERSION;
	sessionId: string;
	incarnation: string;
	composer: {
		/** Changes whenever text or attachments change. Used for accepted-send CAS. */
		revision: number;
		text: string;
		attachments: ChatDraftAttachment[];
		excerpts?: ChatDraftExcerptReference[];
		/** Durable delivery journal. Present until acceptance is durably cleared. */
		delivery?: ChatComposerDelivery;
	};
	queuedEdit?: ChatDraftQueuedEdit;
	inlineEdit?: ChatDraftInlineEdit;
	/** Durable delivery journal for an inline branch edit. */
	inlineEditDelivery?: ChatInlineEditDelivery;
}

export type DraftStorage = Pick<Storage, "getItem" | "setItem" | "removeItem">;

export type DraftWriteResult = { ok: true; draft: ChatSessionDraft } | { ok: false; draft: ChatSessionDraft };

export type DraftClearResult =
	| { ok: true; cleared: boolean; draft: ChatSessionDraft }
	| { ok: false; cleared: false; draft: ChatSessionDraft };

export type DraftDeliveryResult<Mutation> =
	| { ok: true; recovered: boolean; draft: ChatSessionDraft; mutation: Mutation }
	| { ok: false; recovered: boolean; draft: ChatSessionDraft; mutation?: Mutation };

export interface PrepareChatComposerDeliveryInput {
	nativeImages?: boolean;
	optimistic?: boolean;
	kind: ChatComposerDelivery["kind"];
	composerText: string;
	attachments: ChatDraftAttachment[];
	excerpts?: ChatDraftExcerptReference[];
	requestText: string;
	clientMessageId: string;
}

export interface PrepareChatInlineEditDeliveryInput extends ChatDraftInlineEditInput {
	clientMessageId: string;
}

export type ChatDraftMutationToken = symbol;

export interface ChatDraftMutationSnapshot<Revision> {
	pending: boolean;
	accepted?: {
		sequence: number;
		revision: Revision;
		result: DraftClearResult;
	};
}

export type DraftMutationKind = "composer" | "inline-edit";

export type ChatDraftRuntime = {
	sessionId: string;
	listeners: Set<() => void>;
	composerToken?: ChatDraftMutationToken;
	inlineEditToken?: ChatDraftMutationToken;
	composer: ChatDraftMutationSnapshot<number>;
	inlineEdit: ChatDraftMutationSnapshot<string>;
};
