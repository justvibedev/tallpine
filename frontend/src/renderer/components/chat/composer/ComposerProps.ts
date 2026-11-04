/** Props of the ChatComposer. */
import { ReactNode, Ref } from "react";
import { ComposerPromptSuggestion } from "../ComposerEditor";
import { FileAttachmentPayload } from "../../../hooks/useFileAttachments";
import { ChatSkill, ChatSteerOutcome } from "../../../types/conversation";
import { ChatDraftScope, ChatDraftAttachment, ChatDraftExcerptReference, ChatDraftRetainedAttachment } from "../../../lib/chat-drafts";
import { ChatComposerHandle, StoredComposerAttachment } from "./composer-model";

export interface ChatComposerProps {
	onSelectAnnotation?: (annotation: { text: string; messageId?: string; revision?: number }) => void;
	/** This conversation's sent human messages, oldest first, recalled with Up and Down. */
	sentMessages?: readonly string[];
	/** The conversation branch `sentMessages` belongs to; changing it ends an open walk. */
	sentMessagesScope?: string;
	focusRef?: Ref<ChatComposerHandle>;
	onSend: (
		text: string,
		attachments?: FileAttachmentPayload[],
		clientMessageId?: string,
		retainedContent?: number[],
		excerpts?: ChatDraftExcerptReference[],
	) => void | Promise<unknown>;
	settings?: ReactNode;
	/** Mode rows for the add menu. With them the plus button opens a menu instead of the file picker. */
	addMenuItems?: ReactNode;
	/** A provider decision that temporarily replaces ordinary message entry. */
	approval?: ReactNode;
	/** A provider question, docked above the composer until it is answered. */
	elicitation?: ReactNode;
	/** A send is in flight. */
	busy?: boolean;
	/** The agent is mid-turn, so this message is held until the turn ends. */
	willQueue?: boolean;
	/** Replaces the default queueing hint, e.g. while the agent is still starting. */
	queuePlaceholder?: string;
	disabled?: boolean;
	/** Explains why message entry is temporarily blocked. */
	disabledPlaceholder?: string;
	starting?: boolean;
	/** A contextual prompt shown before an otherwise empty conversation begins. */
	emptyPlaceholder?: string;
	/** A proposed next message, kept out of the draft until accepted with Tab. */
	promptSuggestion?: ComposerPromptSuggestion;
	/** The provider's skills. Empty leaves `/` an ordinary character. */
	skills?: ChatSkill[];
	/** Worktree-relative paths offered for `@`. Empty leaves `@` ordinary. */
	filePaths?: string[];
	/** The path list was capped, so the menu says so rather than implying it is all. */
	filePathsTruncated?: boolean;
	/**
	 * Writes staged files into the worktree and answers with the paths the agent
	 * can open. Absent means files cannot be delivered, and no attach control is
	 * offered at all.
	 */
	onStageAttachments?: (attachments: FileAttachmentPayload[]) => Promise<string[]>;
	/** Send the same staged bytes as native ACP image blocks when negotiated. */
	nativeImages?: boolean;
	/**
	 * Deliver this text into the turn already running. Absent means the harness
	 * cannot steer and the choice is never offered.
	 */
	onSteer?: (text: string, attachments?: FileAttachmentPayload[], clientMessageId?: string, recoverOnly?: boolean) => Promise<ChatSteerOutcome | void>;
	/** Expose steering as a separate action while a Cloud turn is running. */
	showSteerButton?: boolean;
	/** Stop the turn already running when there is no draft to send. */
	onInterrupt?: () => void;
	/**
	 * Pick a stopped turn back up when there is no draft to send: the same button
	 * that stopped it continues it, like play after pause. Present only while the
	 * latest turn is one the user stopped.
	 */
	onContinue?: () => Promise<unknown> | void;
	/** A turn is actually running, so there is something to steer into. */
	canSteer?: boolean;
	/** A send mutation is in flight for this session. */
	sendPending?: boolean;
	steerPending?: boolean;
	/** Why the last steer was refused. */
	steerRefusal?: string;
	/** A selected history message to load into the composer as a new draft. */
	draftSeed?: { id: string; text: string; attachments?: StoredComposerAttachment[]; stagedAttachments?: ChatDraftAttachment[] };
	/** A queued turn being edited in the composer instead of the dock. */
	editingQueuedTurnId?: string;
	onCancelQueuedEdit?: () => void;
	onQueuedDraftChange?: (text: string) => void;
	/** Called when the draft turns empty or non-empty, including after a restore. */
	onDraftEmptyChange?: (empty: boolean) => void;
	queuedDraftScope?: ChatDraftScope;
	onQueuedAttachmentsChange?: (attachments: ChatDraftAttachment[]) => void;
	onQueuedRetainedAttachmentsChange?: (attachments: ChatDraftRetainedAttachment[]) => void;
	/** The queued edit mutation is in flight for the turn being edited. */
	savingQueuedEditPending?: boolean;
	/** Keep the exact queued edit immutable until its saved delivery ID is reconciled. */
	queuedEditRecovery?: boolean;
	/** A failed send, approval, interrupt, or settings mutation. */
	commandError?: string;
	/** A queued-message dock owns the shared rounded top edge. */
	attachedTop?: boolean;
	/** Queued messages rendered above the composer. */
	queuedDock?: ReactNode;
	/** Run Tallpine's built-in `/compact` command instead of sending it to the agent. */
	onCompact?: () => void | Promise<unknown>;
	/** The provider is already compacting this conversation. */
	compacting?: boolean;
	/** A typed provider refusal from the last compaction attempt. */
	compactUnavailable?: string;
	/** A running turn must be stopped before its history can be compacted. */
	compactBlocked?: boolean;
	/** Changes when the owning chat surface should reclaim composer focus. */
	autoFocusKey?: string;
	/** Whether this composer is currently visible and should take focus. */
	autoFocus?: boolean;
	/** Durable Tallpine session identity used to scope unsent composer state. */
	draftSessionId?: string;
	/** Immutable daemon identity for this exact incarnation of the session id. */
	draftSessionIncarnation?: string;
	/** Host-specific proxy origin for staged attachment reads. */
	assetBaseUrl?: string;
	/** The session belongs to a remote host, even if its proxy is disconnected. */
	remoteHost?: boolean;
	/** Daemon wire session ID when draft storage uses a host-scoped identity. */
	assetSessionId?: string;
	/** Client ids already present in daemon-authoritative conversation history. */
	acceptedClientMessageIds?: ReadonlySet<string>;
}
