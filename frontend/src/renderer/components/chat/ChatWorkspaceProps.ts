import { type ReactNode } from "react";
import {
	type ChatDraftExcerptReference,
	type ChatDraftInlineEdit,
	type ChatDraftScope,
} from "../../lib/chat-drafts";
import type { ApprovalMode } from "../../types/conversation";
import type { ConversationLocalEcho } from "../../hooks/conversation";
import type { ShellTerminal } from "../../hooks/useShellTerminals";
import { type SessionKind, type WorkspaceSession, type SessionProvisionStep } from "../../types/workspace";
import type { AgentSwitchProps } from "./AgentSwitchPicker";
import type { ComposerPromptSuggestion } from "./ComposerEditor";
import type { QueuedMessageEditOptions } from "../../types/conversation";
import {
	type ConversationSnapshot,
	type ChatConfigOption,
	type ChatConfigOptionValue,
	type ChatModel,
	type ChatSkill,
	type ChatSteerOutcome,
	type ChatEditOutcome,
	type TurnSettings,
} from "../../types/conversation";
import { ReviewerTerminalTarget, ShellTerminalTarget, WorkspaceTab } from "./ChatWorkspaceTabs";

// The daemon enriches this request with the interrupted task and response tail.
// Continue starts another turn in the existing conversation; it is not an exact
// execution checkpoint.
export const CONTINUE_STOPPED_TURN_PROMPT = "Continue from where you stopped.";

/** The latest turn that still counts is one the user stopped. */
export function continuableTurnId(snapshot: ConversationSnapshot): string {
	return snapshot.continueTurnId ?? "";
}

export interface ChatRetryControl {
	retry: (turnId: string) => void | Promise<unknown>;
	pending?: boolean;
	error?: string;
	turnId?: string;
	setAutoResume?: (turnId: string, enabled: boolean) => void | Promise<unknown>;
	autoResumePending?: boolean;
	autoResumeError?: string;
	autoResumeTurnId?: string;
}

export type MessageEditDraft = ChatDraftInlineEdit;

export interface ChatWorkspaceProps {
	onAddToSideChat?: (excerpt: ChatDraftExcerptReference) => void | Promise<void>;
	annotationNavigationRequest?: { id: string; text: string; messageId?: string; revision?: number };
	snapshot: ConversationSnapshot;
	promptSuggestion?: ComposerPromptSuggestion;
	/** Renderer-owned state identity; the snapshot's sessionId remains the daemon wire ID. */
	uiSessionId?: string;
	/** Durable draft owner for a conversation that is not a worker session. */
	draftOwner?: ChatDraftScope;
	/** The session title from the sidebar (matches what users see in the left sidebar) */
	sessionTitle?: string;
	/** The Tallpine role using this shared conversation surface. */
	sessionRole?: SessionKind;
	/** Host-specific proxy origin for images and staged attachments. */
	assetBaseUrl?: string;
	/** Selected remote host, even while its connection has no proxy origin. */
	remoteHostId?: string;
	/** Session-level actions owned above the conversation surface. */
	headerActions?: ReactNode;
	/** Agent-session actions on the primary chat tab (interface switch, handoff). */
	sessionTabAction?: ReactNode;
	/** Widen the primary tab action slot only while an interface switch spinner is showing. */
	sessionTabActionWide?: boolean;
	/** Pinned beside the tab strip, before the workspace topbar actions. */
	tabStripAction?: ReactNode;
	/** File tabs coordinated by SessionView, appended to the native chat tab strip. */
	workspaceTabs?: WorkspaceTab[];
	workspaceTabActions?: ReactNode;
	workspaceActiveTabKey?: string;
	/** Session-owned order shared with the terminal UI surface. */
	auxiliaryTabOrder?: string[];
	onAuxiliaryTabOrderChange?: (keys: string[]) => void;
	/** Suppress a transient stopped snapshot while a mode handoff installs Chat. */
	controllerTransitioning?: boolean;
	/** The handoff is an account move: the chat stays as it is, with no arrival chrome. */
	accountMoving?: boolean;
	/** The conversation is still being read on a plain open or navigation; hold the composer without the startup shimmer. */
	loadingQuietly?: boolean;
	/**
	 * A stopped agent is being resumed after the chat opened. Unlike a mode
	 * handoff, the history is final, so it stays readable; only sending waits.
	 */
	agentResuming?: boolean;
	startingSteps?: readonly SessionProvisionStep[];
	settingsReady?: boolean;
	/** The agent's own settings have not arrived; the bar shows the saved ones, disabled. */
	settingsLoading?: boolean;
	/** The model this chat's agent last reported running, named while its settings load. */
	lastModel?: string;
	/** Freeze agent-owned Chat controls while a durable session mutation owns input. */
	agentInputDisabled?: boolean;
	/** Fence new agent work without blocking decisions required by the current turn. */
	newWorkDisabled?: boolean;
	reviewerTerminal?: { handleId: string; harness: string };
	onOpenReviewerTerminal?: (target: { handleId: string; harness: string }) => void;
	reviewerChat?: { reviewId: string; harness: string };
	onOpenReviewerChat?: (target: { reviewId: string; harness: string }) => void;
	/** A typed reviewer owns the body while this worker surface remains mounted. */
	reviewerChatSelected?: boolean;
	/** The parent surface owns the shared session tab strip. */
	hideHeader?: boolean;
	inlineHeader?: boolean;
	/** Older durable history is available but not loaded into the DOM yet. */
	hasOlder?: boolean;
	loadingOlder?: boolean;
	onLoadOlder?: () => void;
	/** The surface's onSend forwards transcript excerpts. Without it, "Add to
	 * chat" is hidden so a selection cannot be cleared from the draft unsent. */
	excerptsEnabled?: boolean;
	onSend?: (
		text: string,
		attachments?: { mimeType: string; data: string }[],
		clientMessageId?: string,
		excerpts?: ChatDraftExcerptReference[],
	) => void | Promise<unknown>;
	onDecide?: (requestId: string, decisionId: string) => void;
	onResolveInput?: (
		requestId: string,
		action: "accept" | "decline" | "cancel",
		content?: Record<string, unknown>,
	) => Promise<unknown> | void;
	onInterrupt?: () => void;
	/**
	 * Continues the latest turn after the user stopped it, sending
	 * CONTINUE_STOPPED_TURN_PROMPT as a continuation. Offered on the composer's
	 * button while that turn is the latest and nothing is running.
	 */
	onContinueTurn?: () => Promise<unknown> | void;
	commandError?: string;
	onResumeAgent?: () => void | Promise<unknown>;
	resumingAgent?: boolean;
	resumeError?: string;
	onOpenShell?: () => void;
	openingShell?: boolean;
	shellError?: string;
	/** Open an HTTP(S) link in this session's Tallpine Browser panel. */
	onLinkOpen?: (url: string) => void;
	onSessionLinkOpen?: (url: string) => void;
	/** A send or decision is in flight. */
	busy?: boolean;
	/** The provider's model catalog. Empty hides the model control. */
	models?: ChatModel[];
	/** Makes the model control one picker for agent, model and effort. */
	agentSwitch?: AgentSwitchProps;
	/** A line above the chat box, such as a switch waiting for the next message. */
	composerNotice?: ReactNode;
	/** The Tallpine session this surface renders for. Used to attach the reviewer pane. */
	session?: WorkspaceSession;
	/** Refresh the owning workspace cache after the shared primary tab is renamed. */
	onSessionRenamed?: () => void | Promise<void>;
	/** The selected reviewer pane. Kept even while its tab is temporarily unavailable. */
	reviewerTarget?: ReviewerTerminalTarget;
	/** Switch the active tab back to the chat timeline. */
	onSelectChat?: () => void;
	/** This session's standalone shells, rendered as tabs after the reviewer. */
	shellTerminals?: ShellTerminal[];
	/** The selected shell pane, if any. Mirrors reviewerTarget. */
	shellTarget?: ShellTerminalTarget;
	onSelectShellTerminal?: (handleId: string) => void;
	onCloseShellTerminal?: (handleId: string) => void;
	onRenameShellTerminal?: (handleId: string, title: string) => void;
	/** Daemon readiness for the reviewer terminal pane. */
	daemonReady?: boolean;
	/** Resolved color theme for the reviewer terminal pane. */
	theme?: "light" | "dark";
	/** `picked` names the setting the user chose from its own menu; other changes echo the shown settings. */
	onChooseSettings?: (settings: TurnSettings, picked?: keyof TurnSettings) => void;
	/** `null` forgets the project's remembered mode. */
	onRememberPermissions?: (mode: ApprovalMode | null) => Promise<unknown> | void;
	showApprovalMode?: boolean;
	approvalModes?: ApprovalMode[];
	rememberPermissionsPending?: boolean;
	rememberPermissionsError?: string;
	rememberedPermissionMode?: ApprovalMode;
	/** Live provider-owned options, such as ACP model, effort, mode, and fast mode. */
	configOptions?: ChatConfigOption[];
	onChooseConfigOption?: (
		optionId: string,
		value: ChatConfigOptionValue,
	) => Promise<unknown> | void;
	configOptionPending?: boolean;
	configOptionError?: string;
	/** Summarize earlier history to reclaim context. */
	onCompact?: () => void;
	/** A compaction is running provider-side. It takes seconds, not milliseconds. */
	compacting?: boolean;
	/** Why compaction is not available right now, from the daemon's typed refusal. */
	compactUnavailable?: string;
	/**
	 * Discard a turn and everything after it. Absent means the agent cannot undo,
	 * and the affordance is not drawn at all rather than shown and then refused.
	 */
	onRollback?: (turnId: string) => void | Promise<unknown>;
	rollbackPending?: boolean;
	rollbackError?: string;
	/** Opens the session Files inspector from a turn's changed-files Review control. */
	onOpenFiles?: () => void;
	/** Opens the Files inspector focused on one changed path. */
	onOpenFile?: (path: string, line?: number) => void;
	/**
	 * Re-dispatch a failed turn's durable prompt as a new turn. Offered only for
	 * eligible failed human turns, so the affordance is drawn on the failed-turn
	 * boundary and never for a turn that is running or already succeeded.
	 */
	retryControl?: ChatRetryControl;
	/** Create a conversation branch by replacing a prior human prompt. */
	onEditMessage?: (
		turnId: string,
		text: string,
		clientMessageId?: string,
	) => void | ChatEditOutcome | Promise<ChatEditOutcome | void>;
	editMessagePending?: boolean;
	editMessageError?: string;
	/** Switch the visible conversation to another branch. */
	onActivateBranch?: (branchId: string) => void | Promise<unknown>;
	activateBranchPending?: boolean;
	activateBranchError?: string;
	/** The provider's skills. Empty leaves `/` an ordinary character. */
	skills?: ChatSkill[];
	/** Worktree paths offered for `@`. */
	filePaths?: string[];
	/** The path list was capped by the daemon rather than being all of them. */
	filePathsTruncated?: boolean;
	/** Renderer-only human messages awaiting their exact durable counterpart. */
	localEchos?: ConversationLocalEcho[];
	/**
	 * Writes staged images into the worktree and answers with the paths the agent
	 * can open. Absent means no attach control is offered — the fixture preview has
	 * no worktree to write into.
	 */
	onStageAttachments?: (attachments: { mimeType: string; data: string }[]) => Promise<string[]>;
	/** The provider negotiated native image prompt blocks. */
	nativeImages?: boolean;
	/**
	 * Deliver guidance into the turn already running, instead of queueing a message
	 * behind it. Absent means this harness cannot steer and no control is drawn —
	 * Claude answers `CHAT_STEER_UNSUPPORTED`, and an affordance that only ever fails
	 * is worse than none.
	 */
	onSteer?: (
		text: string,
		attachments?: { mimeType: string; data: string }[],
		clientMessageId?: string,
		recoverOnly?: boolean,
	) => Promise<ChatSteerOutcome | void>;
	showSteerButton?: boolean;
	sendPending?: boolean;
	steerPending?: boolean;
	/** Why the last steer was refused, from the daemon's typed answer. */
	steerRefusal?: string;
	onPromoteQueuedTurn?: (turnId: string) => Promise<unknown>;
	onEditQueuedTurn?: (turnId: string, text: string, options?: QueuedMessageEditOptions) => Promise<unknown>;
	onCancelQueuedTurn?: (turnId: string) => Promise<unknown>;
	onReorderQueuedTurns?: (turnIds: string[]) => Promise<unknown>;
	promoteQueuedTurnPendingTurnId?: string;
	cancelQueuedTurnPendingTurnId?: string;
	editQueuedTurnPendingTurnId?: string;
}
