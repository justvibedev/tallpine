/** Turn settings, models, config options and the conversation snapshot. */
import type { AutoCompactChoice } from "../../lib/auto-compact";
import { SessionMode, ConversationTurn, ConversationBranchPoint } from "./turn";
import { ConversationItem } from "./details";

/** Tallpine's permission vocabulary, applied per turn in chat mode. */
export type ApprovalMode = "default" | "accept-edits" | "auto" | "bypass-permissions";

/**
 * The provider choices for the next turn.
 *
 * Every field is optional and empty means the provider's own default, so clearing
 * a choice and never making one are the same thing.
 */
export interface TurnSettings {
	model?: string;
	reasoningEffort?: string;
	approvalMode?: ApprovalMode;
	/** Ask for the provider's faster tier, on models that offer one. */
	fastMode?: boolean;
	/** Ask the provider to plan rather than act, where Tallpine sets that per turn. */
	planMode?: boolean;
	/** Where Claude Code compacts on its own. Unset leaves it to Claude Code. */
	autoCompactWindow?: AutoCompactChoice;
	/** The running agent still compacts at an older value; the chosen one applies after a restart. Daemon-set. */
	autoCompactPending?: boolean;
	/** The window the project's own environment sets, which Auto leaves in force. Daemon-set. */
	autoCompactProjectTokens?: number;
	/** The model's real context window and where the chosen auto-compact setting compacts against it. Daemon-set. */
	contextCapacity?: number;
	compactThreshold?: number;
	/** The catalog's name for the model. Daemon-set. */
	modelLabel?: string;
}

/** One model the provider offers for this session. */
export interface ChatModel {
	id: string;
	displayName: string;
	description?: string;
	/** The model the provider would pick on its own. */
	default: boolean;
	/** Reasoning levels this model supports, in the provider's order. */
	efforts?: string[];
	defaultEffort?: string;
	/** The model can run on the provider's faster tier. */
	supportsFast?: boolean;
	/** Why the model cannot be chosen now (the installed agent CLI is too old), from the agent's catalog. */
	unavailableReason?: string;
}

/** One provider-owned setting advertised for this live chat session. */
export interface ChatConfigOption {
	id: string;
	name: string;
	description?: string;
	/** ACP's semantic category, such as model, thought_level, or mode. */
	category?: string;
	type: "select" | "boolean";
	currentValue?: string;
	currentBoolean?: boolean;
	choices: ChatConfigChoice[];
}

/** One value offered by a select config option. */
export interface ChatConfigChoice {
	/** Exact Tallpine permission equivalent supplied by the daemon, when supported. */
	permissionMode?: ApprovalMode;
	value: string;
	name: string;
	description?: string;
	/** Provider grouping is presentation data, not part of the value sent back. */
	group?: string;
	groupName?: string;
	/** Why the choice cannot be picked now; Tallpine adds it from the agent's catalog, the provider never sends it. */
	unavailableReason?: string;
}

/** The two value shapes ACP session config supports. */
export type ChatConfigOptionValue = { value: string } | { enabled: boolean };

/**
 * One named skill the provider will let this session invoke.
 *
 * `name` is the invocable identifier and the only part the agent resolves;
 * `displayName` is a label and must never be what gets sent.
 */
export interface ChatSkill {
	name: string;
	displayName: string;
	description?: string;
	/** Short provider-authored placeholder for command arguments. */
	inputHint?: string;
	/** The provider's scope: user, repo, system, admin. */
	source?: string;
}

/** State of the daemon's connection to the provider. */
export type ControllerState = "connecting" | "ready" | "busy" | "recovering" | "stopped" | "hibernated";

/**
 * How full this conversation is.
 *
 * Current state, not a timeline entry: the provider reports it after every tool
 * call, and one row per report is what buried the conversation. What a reader
 * needs is the fraction, not the raw figure -- a token count with no window is a
 * number with no scale.
 */
export interface ConversationUsage {
	contextUsed: number;
	/** 0 when the provider would not state a window for this model. */
	contextWindow: number;
	/** The conversation's cumulative spend, which is a different question from
	 *  fullness: it grows without bound while context rises and falls. */
	inputTokens: number;
	outputTokens: number;
	cachedTokens: number;
	totalTokens: number;
	/** Cumulative provider-reported money, when the account reports it. */
	cost?: number | null;
	currency?: string;
}

/**
 * The account's quota position, which is why a turn can fail for reasons that
 * have nothing to do with what the user asked.
 */
export interface ConversationRateLimits {
	/** Percentages in 0..100. Negative means the provider did not report that
	 *  window, which is not the same as reporting it empty. */
	primaryUsedPercent: number;
	secondaryUsedPercent: number;
	/** Seconds remaining, not an absolute instant. */
	primaryResetsInSeconds?: number;
	secondaryResetsInSeconds?: number;
	planLabel?: string;
}

/**
 * The provider answering with a model other than the one that was asked for.
 *
 * Deliberately separate from `settings`: settings say what the user chose, this says
 * what actually replied. Without it the surface keeps advertising a model that is not
 * the one producing the answers.
 */
export interface ModelReroute {
	fromModel?: string;
	toModel: string;
	/** The provider's own word for why, carried verbatim rather than translated. */
	reason?: string;
	/** The turn it happened on, so the substitution can be pointed at an exchange. */
	providerTurnId?: string;
	at: string;
}

/** The provider account this conversation runs under. */
export interface ConversationAccount {
	authenticationState?: "unknown" | "required" | "authenticated";
	authVerifiedAt?: string;
	lastAuthFailureAt?: string;
	lastAuthFailureReason?: string;
	authFailureId?: string;
	authMode?: string;
	planLabel?: string;
	/**
	 * When the provider last demanded credentials Tallpine does not hold. Present means the
	 * daemon has not yet verified recovery.
	 */
	reauthRequiredAt?: string;
	reauthReason?: string;
}

/** The provider's own lifecycle view of a thread. */
export type ThreadStatus = "active" | "idle" | "not_loaded" | "system_error" | "closed";

/**
 * The provider's thread lifecycle, which is NOT Tallpine's controller state.
 *
 * The controller banner reports the daemon's connection to the agent process. This
 * reports what the provider says about the thread behind it, and the two disagree
 * routinely — a healthy controller can be attached to a thread the provider has put
 * into `system_error`.
 */
export interface ConversationThreadState {
	status?: ThreadStatus;
	/** The provider's active flags. A thread can be active AND blocked on a person. */
	waitingOn?: string[];
	archivedAt?: string;
	closedAt?: string;
	/** The latest context compaction the provider reported; absent until one is. */
	compaction?: ConversationCompaction;
}

/** One context compaction. Token counts are absent when the provider did not report them. */
export interface ConversationCompaction {
	status: "compacting" | "completed" | "failed";
	trigger?: "auto" | "manual";
	preTokens?: number;
	postTokens?: number;
	/** ISO time the compaction started, or finished once it has. */
	at: string;
}

/** One tool server's startup state. */
export interface McpServer {
	name: string;
	status: "starting" | "ready" | "failed" | "cancelled" | (string & {});
	/** The provider's failure text. */
	error?: string;
	/** Its classification of the failure, which is actionable in a way a message is not. */
	failureReason?: string;
}

export interface ConversationSnapshot {
	/** Backend-selected dispatched turn; an empty value means Continue is unavailable. */
	continueTurnId?: string;
	conversationId: string;
	sessionId: string;
	harness: string;
	mode: SessionMode;
	controller: { state: ControllerState; error?: string };
	turns: ConversationTurn[];
	/** Already ordered by sequence. The renderer does not re-sort. */
	items: ConversationItem[];
	latestSequence: number;
	oldestSequence: number;
	hasMoreBefore: boolean;
	/** The first provider-backed prompt in the active provider scope. */
	nativeForkAvailableAfterSequence?: number;
	activeBranchId?: string;
	branchedFromEarlierMessage?: boolean;
	branchPoints?: ConversationBranchPoint[];
	/** How the active historical branch acquired its provider context. */
	branchMaterialization?: ConversationBranchMaterialization;
	/** What the next turn will be sent with. Daemon-owned, so it survives a
	 *  restart and applies to turns Tallpine dispatches on the user's behalf. */
	settings: TurnSettings;
	/** Undefined until the provider reports. Distinct from a conversation using
	 *  nothing, so the meter is withheld rather than drawn empty. */
	usage?: ConversationUsage;
	rateLimits?: ConversationRateLimits;
	/**
	 * When history was last summarized to reclaim context, or absent if never.
	 *
	 * Read from the snapshot rather than scanned out of the timeline: the timeline
	 * is unbounded and this is consulted on every render.
	 */
	compactedAt?: string;
	/**
	 * The name the provider gives this thread. Empty until something names it.
	 * The session's own label is set from this by the daemon, so this is here for
	 * the header rather than for the sidebar.
	 */
	title?: string;
	/** Present when the provider answered with a model other than the chosen one. */
	modelReroute?: ModelReroute;
	/** Absent until the provider says anything about the account. */
	account?: ConversationAccount;
	/** The provider's lifecycle view of the thread, which is not the controller's. */
	threadState?: ConversationThreadState;
	/**
	 * The tool servers this conversation can reach.
	 *
	 * It answers a question the timeline cannot: a tool call that never happened
	 * because its server failed to start reads, from the timeline alone, as the agent
	 * choosing not to use it.
	 */
	mcpServers?: McpServer[];
	/**
	 * What this session's provider can do, so a control is gated before it is drawn.
	 *
	 * Absent until a controller is live — which means "not known yet", NOT "cannot".
	 * Read it through `can()`, which encodes that distinction once.
	 */
	capabilities?: string[];
}

/** The provider-history fidelity of the active branch. */
export interface ConversationBranchMaterialization {
	strategy: "native" | "approximate_context" | (string & {});
	/** True when Tallpine's bounded reconstructed context omitted some text. */
	replayTruncated: boolean;
}

