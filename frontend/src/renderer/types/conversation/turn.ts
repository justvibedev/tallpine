/** Turns, messages, plans and diffs. */

/** Which controller currently owns the Tallpine session. */
export type SessionMode = "chat" | "tui";

/** One request and the agent work that followed it. */
export type TurnState = "queued" | "running" | "completed" | "recovered" | "interrupted" | "failed" | "cancelled";

export type MessageRole = "user" | "assistant";

/**
 * Who produced a message. Durable and structural — a worker or automation is
 * never identified by sniffing a text prefix.
 */
export type MessageOrigin = "human" | "automation" | "daemon" | "provider";

/**
 * `mcp_tool` and `auto_review` are deliberately not folded into their nearest
 * neighbour. An MCP call is not a `command`: it has a server, a tool name and
 * structured arguments, and drawing it as a shell command claimed the agent had run
 * something in the worktree. An auto-review is not an `approval`: an approval is a
 * question waiting on a person, an auto-review is a decision already taken for them,
 * and those are opposites.
 */
export type ActivityKind =
	| "command"
	| "file_change"
	| "plan"
	| "reasoning"
	| "approval"
	| "usage"
	| "error"
	| "system"
	| "mcp_tool"
	| "auto_review"
	| "user_input"
	| "ao_action";

/**
 * `cancelled` means the enclosing turn stopped before the provider completed the
 * item. `recovered` means replay proved the item is historical but carried no
 * portable outcome. Both are intentionally distinct from `failed`.
 */
export type ActivityStatus =
	| "running"
	| "completed"
	| "recovered"
	| "failed"
	| "cancelled"
	| "pending"
	| "resolved";

/**
 * Delivery state for a message Tallpine sent. `uncertain` is deliberately not merged
 * into `failed`: the provider may have accepted the turn while Tallpine lost the
 * connection, and silently retrying would run the work twice.
 */
export type DeliveryState = "queued" | "sending" | "accepted" | "uncertain" | "failed";

/**
 * Result of attempting to steer a running turn. A typed non-acceptance proves
 * that the provider did not receive this guidance; transport ambiguity rejects
 * the promise instead and remains fail-closed in the composer.
 */
export type ChatSteerOutcome =
	| { status: "accepted" }
	| { status: "not-accepted"; reason: string };

/** Durable inline-edit acceptance; uncertainty remains a rejected promise. */
export type ChatEditOutcome = ChatSteerOutcome;

/** How far the agent has got with one step of its plan. */
export type PlanStepStatus = "pending" | "in_progress" | "completed";

export interface PlanStep {
	text: string;
	status: PlanStepStatus;
}

/**
 * The agent's plan for a turn.
 *
 * Current state, not history: the provider re-sends the whole plan every time it
 * changes, and the daemon overwrites the turn's copy. That is why this is on the turn
 * rather than being read out of the timeline — a row per revision would read as
 * though the agent had planned three separate times in one turn.
 */
export interface ConversationPlan {
	explanation?: string;
	steps: PlanStep[];
	/** The agent keeps this list across turns (Claude's task list), so later turns carry copies of it. */
	spansTurns?: boolean;
}

export interface ConversationUsageLimit {
	/** Absolute provider reset time, when it can be determined safely. */
	resetsAt?: string;
	autoResume: boolean;
	resumeError?: string;
}

/** The daemon will retry this failed turn on its own after a network error. */
export interface ConversationTurnAutoRetry {
	attempt: number;
	maxAttempts: number;
	nextAt: string;
	/** The network is down; the retry waits for it without spending an attempt. */
	offline?: boolean;
}

export interface ConversationTurn {
	id: string;
	state: TurnState;
	/**
	 * An undo discarded this turn. Its messages and activities are absent from the
	 * snapshot, because the agent no longer remembers them; the turn is still
	 * reported so the timeline can say what was taken back rather than just
	 * appearing to have lost some history.
	 */
	rolledBack?: boolean;
	providerTurnId?: string;
	/** Failed source whose durable prompt created this retry attempt. */
	retryOfTurnId?: string;
	/** A retry attempt exists, even if that child is outside the active branch. */
	hasRetryAttempt?: boolean;
	/** Durable human-prompt fact, independent of the loaded history page. */
	hasHumanPrompt?: boolean;
	errorMessage?: string;
	usageLimit?: ConversationUsageLimit;
	/** The failure was a transient network or API-availability error. */
	transientError?: boolean;
	/** Set while the daemon waits to retry this failed turn on its own. */
	autoRetry?: ConversationTurnAutoRetry;
	requestedAt: string;
	startedAt?: string;
	completedAt?: string;
	/**
	 * What this turn changed on disk. Absent is not "changed nothing": an agent
	 * that cannot report diffs never reports at all, and the UI must not draw an
	 * empty changed-files panel as if the turn had been inspected.
	 */
	diff?: TurnDiff;
	/** The agent's plan for this turn, or absent when it made none. */
	plan?: ConversationPlan;
}

/** How a file changed. The daemon's neutral names, not a provider's. */
export type DiffStatus = "added" | "modified" | "deleted" | "renamed";

export interface DiffFile {
	path: string;
	additions: number;
	deletions: number;
	status: DiffStatus;
	/** Set only for a rename. */
	oldPath?: string;
}

export interface TurnDiff {
	files: DiffFile[];
	/** The daemon cut the list at its cap; the change is larger than what is shown. */
	truncated?: boolean;
}

/** Lightweight, durable prompt content retained when a human message is edited. */
export interface ConversationContentSummary {
	type: string;
	mimeType?: string;
	uri?: string;
	name?: string;
	text?: string;
	sourceConversationId?: string;
 sourceMessageId?: string;
	sourceRevision?: number;
}

export interface QueuedMessageEditOptions {
	clientMessageId?: string;
	attachments?: { mimeType: string; data: string }[];
	retainedContent?: number[];
	expectedRevision?: number;
}

export interface ConversationMessage {
	kind: "message";
	id: string;
	turnId?: string;
	/** Conversation-scoped, immutable. The only ordering key. */
	sequence: number;
	/** Bumped on each streaming rewrite so a gap is detectable. */
	revision: number;
	role: MessageRole;
	origin: MessageOrigin;
	text: string;
	/** Server-authoritative prompt blocks, with heavy image/resource payloads removed. */
	content?: ConversationContentSummary[];
	/** False when the original prompt content cannot be reproduced safely. */
	editAvailable?: boolean;
	/** The user's one-click "continue" after stopping a turn; drawn as a marker, not a bubble. */
	continuation?: boolean;
	/** True while more deltas are expected. */
	streaming: boolean;
	delivery?: DeliveryState;
	/** Set when origin is a worker or automation, for the attribution line. */
	senderLabel?: string;
	senderSessionId?: string;
	senderProjectId?: string;
	senderDisplayName?: string;
	/** The sender's idempotency key. Lets the local echo and this row share one identity. */
	clientMessageId?: string;
	createdAt: string;
}

export interface ConversationBranchPoint {
	turnId: string;
	position: number;
	total: number;
	previousBranchId?: string;
	nextBranchId?: string;
}

