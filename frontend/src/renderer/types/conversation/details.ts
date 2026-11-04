/** Typed activity details and the activity/item union. */
import type { ArtifactRef, RenderRef } from "@tallpine/shared/chat/render-frame";
import { ActivityKind, DiffStatus, PlanStep, ConversationPlan, ActivityStatus, ConversationMessage } from "./turn";
import { ApprovalMode } from "./settings";

/** One choice the provider says is valid for a pending request. */
export interface DecisionOption {
	/** e.g. "accept", "acceptForSession", "acceptWithExecpolicyAmendment". */
	id: string;
	label: string;
	/** Provider-neutral consent meaning; IDs and labels may be opaque or localized. */
	kind?: "allow_once" | "allow_always" | "reject_once" | "reject_always";
}

/** Provider context retained before and after a human approval decision. */
export interface ApprovalDetail {
	/** The provider request shape, used to choose command or file-change copy. */
	method?: string;
	/** The provider decision id Tallpine successfully returned. */
	decision?: string;
	/** Present when another connected client resolved the request. */
	resolvedBy?: string;
	resolvedNote?: string;
	/** The normalized activity the provider wants permission to perform. */
	subjectKind?: ActivityKind;
	/** ACP's tool category, retained for diagnostics and forward compatibility. */
	toolKind?: string;
}

/** Provider-supplied recovery information carried by an error activity. */
export interface ProviderErrorDetail {
	/** Optional provider destination; the renderer shows web URLs literally. */
	actionUrl?: string;
}

export interface CommandDetail {
	/**
	 * Free text payload: a plan body, a reasoning summary, a message.
	 *
	 * For reasoning this is one field whether the item is still streaming or already
	 * settled — the provider's finished summary lands under the same key the deltas
	 * accumulate into, so a reader never has to know which state it is in.
	 */
	text?: string;
	/** The accumulated text stopped at the daemon's cap; the model wrote more. */
	textTruncated?: boolean;
	command?: string;
	rawCommand?: string;
	cwd?: string;
	output?: string;
	/**
	 * Provider output aggregation was observed to drop leading bytes even on tiny
	 * commands, so this display data is not an authoritative record of what ran.
	 *
	 * Set for BOTH output sources. Measured on codex-cli 0.146.0: a command
	 * printing tick-1..tick-8 lost tick-1 from the delta stream and from the
	 * aggregate alike, so accumulating deltas buys liveness, not completeness.
	 */
	outputMayBePartial?: boolean;
	/**
	 * Which channel the text came from.
	 *
	 * `stream` is accumulated output deltas: it exists while the command is still
	 * running, and it is the only source for a command that never completes.
	 * `aggregate` is the provider's own roll-up, which only appears on completion
	 * and is all a fast command produces.
	 */
	outputSource?: "stream" | "aggregate";
	/** Accumulation stopped at the daemon's cap; the command printed more. */
	outputTruncated?: boolean;
	exitCode?: number;
	durationMs?: number;
	reason?: string;
	/**
	 * What the agent typed into a running command's terminal — usually `^C` to abort
	 * one that will not finish on its own.
	 *
	 * Kept out of `output` on purpose: the PTY echoes keystrokes, so merging the two
	 * would print the abort twice and leave no way to tell what the command said from
	 * what was done to it.
	 */
	terminalInput?: string;
	terminalInputTruncated?: boolean;
	/** The provider's own process handle, when it reported one. */
	processId?: number;
	/** ACP transcript hierarchy and richer provider terminal metadata. */
	parentProviderItemId?: string;
	nestedAgent?: boolean;
	providerToolName?: string;
	providerTitle?: string;
	subagentType?: string;
	subagentRetry?: unknown;
	terminalId?: string;
	signal?: string;
}

/** ACP form/URL elicitation projected into a durable Tallpine activity. */
export interface UserInputDetail {
	inputMode?: "form" | "url";
	message?: string;
	schema?: {
		title?: string;
		description?: string;
		type?: "object";
		required?: string[];
		properties?: Record<string, Record<string, unknown>>;
	};
	url?: string;
	elicitationId?: string;
}

/**
 * One changed file, as the daemon now normalizes it.
 *
 * Before, this carried a path and two counts and nothing else: the provider spells a
 * change's kind as an object, so no client could read a status at all. `status` is a
 * plain string now, and `patch` is this file's own unified diff — which is what lets
 * a change be read where it happened instead of only counted.
 *
 * `status` stays optional because rows written by earlier builds are still on disk
 * and have none; a missing status is drawn as a modification rather than dropping the
 * file.
 */
export interface FileChangeFile {
	path: string;
	/** Set only for a rename: where the file was before. */
	oldPath?: string;
	status?: DiffStatus;
	additions: number;
	deletions: number;
	patch?: string;
	/** Native provider before/after text, used to render a fallback diff. */
	oldText?: string;
	newText?: string;
	/** The patch was cut at the daemon's cap, so it is not the whole change. */
	patchTruncated?: boolean;
}

export interface FileChangeDetail {
	/**
	 * Objects for a `file_change`; bare paths for an `auto_review`, whose action names
	 * the files it was allowed to touch. The key is shared on the wire, so the kind is
	 * what disambiguates — read it through `fileChangeFiles` or `reviewedPaths` rather
	 * than by hand.
	 */
	files?: FileChangeFile[] | string[];
	/** Accumulated patch text for a change still being written. */
	patchOutput?: string;
	patchOutputTruncated?: boolean;
}

/**
 * A call to a tool served by an MCP server.
 *
 * `arguments` and `result` are whatever JSON the tool takes and returns, so they are
 * `unknown` and rendered as JSON. When a payload exceeded the daemon's cap it is
 * replaced by `{ truncated, bytes, note }` — a stand-in the UI has to recognize
 * rather than print as the tool's actual answer.
 */
export interface McpToolDetail {
	server?: string;
	toolName?: string;
	/** The provider's grouping when the tool is namespaced rather than server-scoped. */
	namespace?: string;
	arguments?: unknown;
	result?: unknown;
	/** Structured provider content, including native ACP read output. */
	content?: unknown;
	error?: string;
	success?: boolean;
	/** Progress notes streamed while a long call runs. */
	progress?: string;
	progressTruncated?: boolean;
}

/** How much of a risk the provider judged an action to be. */
export type ReviewRiskLevel = "low" | "medium" | "high" | "critical";

/**
 * A decision the provider made on the user's behalf.
 *
 * Not an approval: nobody was asked. The row exists so an action that ran unattended
 * is still on the record, with the reasoning that allowed it.
 */
export interface AutoReviewDetail {
	reviewId?: string;
	/** The item the review gated, so the decision can be tied to the command it let run. */
	targetItemId?: string;
	actionType?: string;
	riskLevel?: string;
	rationale?: string;
	/** Who decided: a policy rule matching, or a model making the call. */
	decisionSource?: string;
	/** The provider's own verdict — `approved`, `denied`, and so on. */
	status?: string;
	userAuthorization?: string;
	/** Set when the action was network access. */
	host?: string;
	commandSource?: string;
}

export type { ArtifactRef, RenderRef } from "@tallpine/shared/chat/render-frame";

/**
 * A daemon-confirmed Tallpine mutation. `action` is the only classifier. Command
 * text and tool prose never promote an attempt into one of these.
 */
export interface TallpineActionDetail {
	action?: string;
	operationId?: string;
	sourceSessionId?: string;
	targetSessionId?: string;
	displayName?: string;
	previousDisplayName?: string;
	harness?: string;
	previousHarness?: string;
	href?: string;
	prNumber?: number;
	prTitle?: string;
	error?: string;
}

/**
 * A `system` activity's discriminator and the fields that belong to it.
 *
 * The kind is a general bucket, so the daemon stamps the event rather than leaving
 * the renderer to infer a compaction from the presence of token counts or a steer
 * from the presence of text. Read the discriminator, never the kind.
 */
export interface SystemEventDetail {
	event?:
		| "compaction"
		| "model.rerouted"
		| "auth.reauth_required"
		| "provider.failure"
		| "steer"
		| "plan"
		| "context.reset"
		| "context.boundary"
		| "provider_switch"
		| "render"
		| "artifact"
		| "workspace.setup"
		| "account.switched"
		| "mcp.status";
	/** mcp.status: the MCP server, its status ("failed", "needs-auth", "disconnected") and the redacted error. */
	server?: string;
	status?: string;
	text?: string;
	/** account.switched: the account a stopped turn's conversation continues on. */
	accountId?: string;
	/** workspace.setup: how the project's Setup commands ended (exitCode, durationMs and output carry the rest). */
	setupStatus?: "succeeded" | "failed" | "timed_out";
	/** workspace.setup: the 1-based Setup step that failed or timed out. */
	setupStep?: number;
	/** provider_switch */
	sourceHarness?: string;
	targetHarness?: string;
	sourceModel?: string;
	targetModel?: string;
	targetEffort?: string;
	/** The Claude Code compaction window the target started with, such as "200k". */
	targetAutoCompactWindow?: string;
	/** The permission mode the target started in. */
	permissionMode?: ApprovalMode;
	/** The chat's own mode, when the target could not run in it; a switch away goes back to it. */
	chatPermissionMode?: ApprovalMode;
	/** provider_switch: drawn before the daemon has switched, so the line shows it is still in progress. Never stored. */
	switching?: boolean;
	/** workspace.setup: a failed step's command, the user and groups it ran as, where, and its full log. */
	setupFailure?: { command?: string; user?: string; groups?: string[]; dir?: string; logPath?: string };
	/** workspace.setup: why a failed step likely failed (no Docker, no sudo, a refused path). */
	setupHint?: string;
	/** render */
	render?: RenderRef;
	/** artifact */
	artifact?: ArtifactRef;
	/** model.rerouted */
	fromModel?: string;
	toModel?: string;
	/** provider.failure */
	category?: string;
	severity?: "warning" | "error" | (string & {});
	revision?: number;
	/** steer: the user's own words, delivered into a turn already running. */
	origin?: string;
	/** steer: source session metadata for an Tallpine automation steer. */
	senderSessionId?: string;
	senderProjectId?: string;
	senderDisplayName?: string;
	clientMessageId?: string;
	/** steer: complete provider-neutral content copied from a promoted queue item. */
	content?: Array<{
		type: string;
		data?: string;
		mimeType?: string;
		uri?: string;
		name?: string;
		text?: string;
	}>;
}

/** A `plan` activity's payload, which is the same plan the turn carries. */
export interface PlanDetail {
	explanation?: string;
	steps?: PlanStep[];
}

export interface UsageDetail {
	inputTokens?: number;
	outputTokens?: number;
	totalTokens?: number;
}

/**
 * What a compaction reclaimed.
 *
 * Every field is optional because the provider reports none of them on its own
 * compaction event — the driver brackets them from the token-usage reports either
 * side of it, and omits what it could not observe. A compaction right after a
 * restart genuinely does not know what it saved, so the UI says nothing rather
 * than showing a zero.
 */
export interface CompactionDetail {
	tokensBefore?: number;
	tokensAfter?: number;
	tokensReclaimed?: number;
	contextWindow?: number;
}

/**
 * Whether a timeline item is the marker for a history compaction.
 *
 * Deliberately not a type predicate: it is used inside a boolean expression that
 * has already narrowed the item to an activity, and a predicate there would narrow
 * the negation to `never`.
 */
export function isCompaction(item: ConversationItem): boolean {
	return item.kind === "activity" && item.detail?.event === "compaction";
}

/**
 * Whether a timeline item is a steer: the user's own words, delivered into a turn
 * that was already running.
 *
 * Read by the `event` discriminator and not by the kind, the same convention a
 * compaction follows. A steer has to be an activity rather than a message because it
 * joins a turn in flight, and Tallpine's only durable write that can attach to one is the
 * activity row — but it is still something a person said, and the timeline shows it
 * that way.
 */
export function isSteer(item: ConversationItem): boolean {
	return item.kind === "activity" && item.detail?.event === "steer";
}

/** The structured changed files on a `file_change`, or none. */
export function fileChangeFiles(activity: ConversationActivity): FileChangeFile[] {
	const files = activity.detail?.files;
	if (!Array.isArray(files)) return [];
	return files.filter((file): file is FileChangeFile => typeof file === "object" && file !== null);
}

/** The bare paths an `auto_review` action named, or none. */
export function reviewedPaths(activity: ConversationActivity): string[] {
	const files = activity.detail?.files;
	if (!Array.isArray(files)) return [];
	return files.filter((file): file is string => typeof file === "string");
}

/**
 * The plan an activity carries, when it is a plan row the turn did not absorb.
 *
 * The daemon writes both: the turn column answers "what is the plan now" and this
 * row answers "where in the conversation did the agent plan". They cannot disagree —
 * both are written from one event — so the surface prefers the turn's copy and only
 * falls back to this for a plan whose turn predates the controller.
 */
export function activityPlan(activity: ConversationActivity): ConversationPlan | undefined {
	const steps = activity.detail?.steps;
	if (!Array.isArray(steps) || steps.length === 0) return undefined;
	return { explanation: activity.detail?.explanation, steps };
}

export interface ConversationActivity {
	kind: "activity";
	id: string;
	turnId?: string;
	sequence: number;
	revision: number;
	activityKind: ActivityKind;
	status: ActivityStatus;
	/** The one-line label shown when collapsed. */
	summary: string;
	/**
	 * The typed payload for this kind. One intersection rather than a discriminated
	 * union because the daemon's `detail` is one JSON column read by every kind, and
	 * a union would force a cast at every access for no safety the fields do not
	 * already have — every one of them is optional, because every one of them is
	 * something a provider may not report.
	 */
	detail?: CommandDetail &
		ApprovalDetail &
		ProviderErrorDetail &
		FileChangeDetail &
		UsageDetail &
		CompactionDetail &
		McpToolDetail &
		AutoReviewDetail &
		UserInputDetail &
		SystemEventDetail &
		PlanDetail &
		TallpineActionDetail;
	/**
	 * The provider's identifier for an approval. Resolving matches on this, so a
	 * card left on screen cannot answer a request that replaced it.
	 */
	requestId?: string;
	/** Structured-input resolution decoded from the daemon's detail payload. */
	inputResponse?: {
		action: "accept" | "decline" | "cancel";
		content?: Record<string, unknown>;
	};
	/** Provider correlation key used to attach nested agent activity to its parent. */
	providerItemId?: string;
	/**
	 * Authoritative for an approval. The provider varies what it offers per
	 * request and does not always include a decline, so buttons are rendered
	 * from this list and never from a fixed set.
	 */
	decisions?: DecisionOption[];
	createdAt: string;
}

/** One ordered entry in the timeline. */
export type ConversationItem = ConversationMessage | ConversationActivity;

