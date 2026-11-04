import type { ComponentProps, MutableRefObject } from "react";
import type { ChatDraftExcerptReference, ChatDraftScope } from "../../lib/chat-drafts";
import type { ChatRetryControl, ChatWorkspaceProps } from "./ChatWorkspaceProps";
import type { ConversationLocalEcho } from "../../hooks/conversation";
import type { SessionStartup } from "./SessionStartup";
import type { ConversationSnapshot } from "../../types/conversation";

/** Props of the conversation Timeline. */

export interface TimelineProps {
	onAddToSideChat?: ChatWorkspaceProps["onAddToSideChat"];
	/** Replaces the default "Add to chat" (write to this draft scope's composer). */
	onAddToChat?: (excerpt: ChatDraftExcerptReference) => void;
	/** Handles annotations whose source is another conversation. */
	onForeignAnnotation?: (annotation: { text: string; messageId?: string; revision?: number; conversationId?: string }) => void;
	annotationNavigationRef: MutableRefObject<((annotation: { text: string; messageId?: string; revision?: number }) => void) | null>;
	snapshot: ConversationSnapshot;
	assetBaseUrl?: string;
	remoteHost?: boolean;
	draftScope: ChatDraftScope;
	/** False when drafts can't be saved; persisted inline edits are then ignored. */
	draftPersistenceAvailable?: boolean;
	hasOlder?: boolean;
	loadingOlder?: boolean;
	onLoadOlder?: () => void;
	excerptsEnabled?: boolean;
	onDecide?: (requestId: string, decisionId: string) => void;
	busy?: boolean;
	onRollback?: (turnId: string) => void;
	/** Opens the inspector's Plan tab, where a session-wide plan lives. */
	onOpenPlan?: () => void;
	/** The inspector beside this timeline has a Plan tab, so a session-wide plan lives there. */
	hasPlanTab?: boolean;
	onOpenFiles?: () => void;
	onOpenFile?: (path: string, line?: number) => void;
	retryControl?: ChatRetryControl;
	composerRecoveryTurnId?: string;
	onEditHumanMessage?: ChatWorkspaceProps["onEditMessage"];
	editPending?: boolean;
	editBusy?: boolean;
	editError?: string;
	onActivateBranch?: (branchId: string) => Promise<unknown> | void;
	activateBranchPending?: boolean;
	activateBranchError?: string;
	newWorkDisabled?: boolean;
	rollbackDisabled?: boolean;
	localEchos?: ConversationLocalEcho[];
	/** A session that is starting, or failed to start, and its setup checklist. */
	startup?: ComponentProps<typeof SessionStartup> & { openingTurnId?: string };
	/** An interface switch is bringing this conversation in; only then does the transcript fade in. */
	arriving?: boolean;
	/** Orchestrator chat: draw worker messages and reports as rows, and the orchestrator's handling under them. */
	workerUpdates?: boolean;
}
