import { sameContent } from "../../lib/stable-list";
import { previewText } from "@tallpine/shared/chat/timeline-text";
import { quietWorkerReply } from "./WorkerUpdates";
import { spawnedSessionIds } from "./SpawnedAgents";
import { isOrdinaryActivity } from "./activity-action";
import { planUpdateSince, type PlanUpdate } from "../../lib/session-plan";
import {
	isCompaction,
	isSteer,
	type ConversationPlan,
	type ConversationSnapshot,
	type ConversationActivity,
	type ConversationItem,
	type ConversationTurn,
	type ConversationTurnAutoRetry,
	type ConversationUsageLimit,
	type TurnDiff,
} from "../../types/conversation";
import { itemKey } from "./timeline-echoes";

/**
 * Batch consecutive plain activities into a run.
 *
 * A run of tool calls is one thought, not several, so it renders as one tight
 * block on a shared rail. Messages and approvals break a run because each is
 * something the reader stops on: prose to read, or a decision to make.
 */
export type TimelineRun =
	| { kind: "activities"; key: string; items: ConversationItem[] }
	| { kind: "single"; key: string; items: [ConversationItem] };

export function runsOf(items: ConversationItem[]): TimelineRun[] {
	const runs: TimelineRun[] = [];
	for (const item of items) {
		// Approvals, user input, errors, reasoning, and stamped system events stay
		// boundaries. Confirmed Tallpine actions have no event stamp, so they batch.
		const runnable = isOrdinaryActivity(item);
		const last = runs.at(-1);
		if (runnable && last?.kind === "activities") {
			last.items.push(item);
			continue;
		}
		runs.push(
			runnable
				? {
						kind: "activities",
						key: `run-${item.sequence}`,
						items: [item],
					}
				: { kind: "single", key: itemKey(item), items: [item] },
		);
	}
	return runs;
}

/**
 * What belongs in a conversation, as opposed to what the provider happens to emit.
 *
 * Two kinds are dropped:
 *
 *   - usage. The daemon now projects it as current state on the snapshot rather
 *     than as a timeline entry, so this filter is a guard for conversations
 *     recorded by an older build whose usage rows are still on disk. Rendering one
 *     row per report is what buried the actual conversation; it lives in the
 *     header meter instead.
 *   - reasoning. Providers can emit one per tool call and they usually carry no
 *     readable body, so they are internal signal rather than conversation chrome.
 *
 *   - a plan row whose turn already carries the plan. The daemon writes both, from
 *     one event, so they cannot disagree; the turn's copy renders as a checklist at
 *     the end of the turn and a second rendering of the same plan in the middle of
 *     it would just be the same list twice.
 *
 * Everything else is kept, including an activity this build does not fully
 * understand — dropping an unrecognized item would hide work the agent really did.
 */
export function readableItems(snapshot: ConversationSnapshot): ConversationItem[] {
	const plannedTurns = new Set(
		snapshot.turns.filter((turn) => turn.plan?.steps.length).map((turn) => turn.id),
	);
	const pendingQuestionTurns = new Set(
		snapshot.items
			.filter((item) => item.kind === "activity" && item.activityKind === "user_input" && item.status === "pending")
			.map((item) => item.turnId),
	);
	return snapshot.items.filter((item) => {
		if (item.kind !== "activity") return true;
		if (item.activityKind === "usage") return false;
		if (item.activityKind === "plan" && item.turnId && plannedTurns.has(item.turnId)) return false;
		if (item.activityKind === "reasoning") return false;
		// Claude emits both a tool call and an elicitation for the same question.
		// The pending form already presents it above the composer.
		if (
			item.activityKind === "mcp_tool" &&
			item.detail?.providerToolName === "AskUserQuestion" &&
			(item.status === "pending" || item.status === "running") &&
			item.turnId && pendingQuestionTurns.has(item.turnId)
		) return false;
		return true;
	});
}

export const groupKey = (group: TimelineGroup): string => group.key;

/**
 * Whether two groups say the same thing.
 *
 * A group carries more than its items: a turn's plan and its changed files are
 * current state that the daemon overwrites, and both move while the turn's items
 * stay exactly as they were. Comparing only the items would keep the previous
 * group object — and with it the previous plan — so a checklist would stop ticking
 * until something else in the turn happened to change.
 */
export function sameGroup(a: TimelineGroup, b: TimelineGroup): boolean {
	return (
		a.anchor === b.anchor &&
		a.turnId === b.turnId &&
		a.live === b.live &&
		a.rollbackable === b.rollbackable &&
		sameContent(a.outcome, b.outcome) &&
		sameContent(a.diff, b.diff) &&
		sameContent(a.plan, b.plan) &&
		sameContent(a.planUpdate, b.planUpdate) &&
		sameContent(a.collapsedRun, b.collapsedRun) &&
		a.items.length === b.items.length &&
		// The items are already identity-stable by the time a group is compared, so a
		// reference check here is exact and avoids walking their contents twice.
		a.items.every((item, index) => item === b.items[index])
	);
}

export type TimelineGroup = {
	key: string;
	turnId?: string;
	/** Where this group sits in the timeline: the lowest sequence it contains. */
	anchor: number;
	items: ConversationItem[];
	outcome?: {
		state: "completed" | "recovered" | "interrupted" | "failed";
		durationMs?: number;
		error?: string;
		usageLimit?: ConversationUsageLimit;
		/** The daemon is retrying this transient failure on its own. */
		autoRetry?: ConversationTurnAutoRetry;
		/** A transient failure that a later attempt already retried. */
		retriedTransient?: boolean;
	};
	/** What the turn changed on disk, when the daemon reported anything. */
	diff?: TurnDiff;
	/** The agent's plan for this turn, when it made one. */
	plan?: ConversationPlan;
	/**
	 * A session-wide plan this turn changed, as the one-line row that points at the
	 * inspector's Plan tab. Such a plan never draws the full card, and a turn that
	 * only carries a re-sent copy gets neither.
	 */
	planUpdate?: PlanUpdate;
	/** The turn is still running, so its diff and plan can still change. */
	live?: boolean;
	liveStartedAt?: string;
	blocked?: boolean;
	/** A live provider failure replaces the generic Working label until the turn settles. */
	liveProviderFailure?: ConversationActivity;
	/** The provider accepted this turn, so there is history it can be asked to drop. */
	rollbackable?: boolean;
	/** An account switch stopped this turn; its notice replaces the outcome. */
	accountSwitched?: boolean;
	/** "Just me" view: this row stands for a run of orchestration turns (see just-me.ts). */
	collapsedRun?: { count: number; open: boolean };
};

type GroupPreview = { title: string; detail?: string };

/** A turn-sized, plain-text preview for the minimap hover card. */
export function groupPreview(group: TimelineGroup): GroupPreview {
	const userMessage = group.items.find(
		(item) => item.kind === "message" && item.role === "user" && item.origin === "human",
	);
	const assistantMessage = [...group.items]
		.reverse()
		.find(
			(item) => item.kind === "message" && item.role === "assistant" && item.text.trim() !== "",
		);
	const firstActivity = group.items.find(
		(item): item is ConversationActivity => item.kind === "activity",
	);
	const title = previewText(
		userMessage?.kind === "message"
			? userMessage.text
			: firstActivity?.summary || "Conversation update",
		120,
	);
	const detailSource =
		assistantMessage?.kind === "message"
			? assistantMessage.text
			: firstActivity?.detail?.text || firstActivity?.detail?.output || firstActivity?.summary;
	const detail = detailSource ? previewText(detailSource, 240) : undefined;
	return { title, detail: detail && detail !== title ? detail : undefined };
}

export function groupHasHumanPrompt(group: TimelineGroup): boolean {
	return group.items.some(
		(item) => item.kind === "message" && item.role === "user" && item.origin === "human",
	);
}

function settledTurnDuration(turn: ConversationTurn): number | undefined {
	if (!turn.completedAt) return undefined;
	const startedAt = Date.parse(turn.startedAt ?? turn.requestedAt);
	const completedAt = Date.parse(turn.completedAt);
	if (!Number.isFinite(startedAt) || !Number.isFinite(completedAt)) return undefined;
	// Match the live label's start fallback and whole-second minimum so completion
	// cannot make a visible timer disappear or jump backward.
	return Math.max(1_000, completedAt - startedAt);
}

// Retry correlation is daemon-owned rather than inferred from repeated text.
// Match it against turn ids in this snapshot before consuming an affordance.
export function retrySourceTurnIds(snapshot: ConversationSnapshot): Set<string> {
	const turnIds = new Set(snapshot.turns.map((turn) => turn.id));
	const sources = new Set<string>();
	for (const turn of snapshot.turns) {
		if (turn.hasRetryAttempt) sources.add(turn.id);
		const source = turn.retryOfTurnId;
		if (source && turnIds.has(source)) sources.add(source);
	}
	return sources;
}

/** How tall the trailing spacer must be to park `anchorOffset` near the top when scrolled to the end. */
export function promptSpacerHeight({
	viewportHeight,
	contentHeightWithoutSpacer,
	anchorOffset,
	topInset = 12,
}: {
	viewportHeight: number;
	contentHeightWithoutSpacer: number;
	anchorOffset: number;
	topInset?: number;
}): number {
	const afterAnchor = Math.max(0, contentHeightWithoutSpacer - anchorOffset);
	return Math.max(0, Math.round(viewportHeight - topInset - afterAnchor));
}

/**
 * Leave a band of prior chat above the latest prompt (Cursor-style), not flush to
 * the top edge. Scales with the timeline viewport, with a floor so short panes
 * still keep a peek of history.
 */
export function promptTopInset(viewportHeight: number): number {
	return Math.max(80, Math.round(viewportHeight * 0.2));
}

/**
 * Group items by the turn that produced them, so a completed turn can be closed
 * off with its outcome.
 *
 * A turn is one block, even when its items are not contiguous in sequence. That
 * matters because of the send queue: a message typed mid-turn is recorded
 * immediately, so its sequence lands before the answers to everything ahead of
 * it. Reading strictly by sequence would stack every queued question at the top
 * and every answer below, separating each question from its own reply.
 *
 * Sequence still decides order — a turn takes the position of its first item, and
 * items inside a turn stay in sequence order. Nothing is re-derived: this is the
 * daemon's ordering, grouped.
 *
 * Items with no turn (an automation relay that arrived between turns) form their
 * own group and keep their sequence position.
 */
export function groupByTurn(snapshot: ConversationSnapshot, hasPlanTab: boolean): TimelineGroup[] {
	const byTurn = new Map(snapshot.turns.map((turn) => [turn.id, turn]));
	const groups: TimelineGroup[] = [];
	const groupForTurn = new Map<string, TimelineGroup>();

	for (const item of snapshot.items) {
		if (item.turnId === undefined) {
			// Consecutive turn-less items share one group rather than getting one each.
			// A provider can run a turn Tallpine never dispatched — a compaction, or a turn
			// resumed inside the provider's own history — and every item it emits then
			// correlates to no Tallpine turn. One group per item made `runsOf` see no two
			// adjacent activities, so a wall of tool calls stopped collapsing and the
			// conversation turned back into a log. Grouping must not depend on
			// correlation succeeding.
			const last = groups.at(-1);
			if (last && last.turnId === undefined) {
				last.items.push(item);
				continue;
			}
			groups.push({
				// A "Switched" line keeps its key from echo to the daemon's row, as its position changes.
				key: item.id.startsWith("local-switch:") ? item.id : `loose-${item.sequence}`,
				anchor: item.sequence,
				items: [item],
			});
			continue;
		}
		const existing = groupForTurn.get(item.turnId);
		if (existing) {
			existing.items.push(item);
			continue;
		}
		const group: TimelineGroup = {
			// A turn opened by a keyed human message keeps the key its echo had.
			key: itemKey(item) === item.id ? `${item.turnId}-${item.sequence}` : itemKey(item),
			turnId: item.turnId,
			anchor: item.sequence,
			items: [item],
		};
		groupForTurn.set(item.turnId, group);
		groups.push(group);
	}

	groups.sort((a, b) => a.anchor - b.anchor);

	// A list the agent keeps across turns reaches every later turn as a copy of the
	// same list. Where the inspector has a Plan tab, only a turn whose copy differs
	// from the one before says anything, and then as a one-line row. Where it has
	// none, only the latest copy is current, so only that turn shows the full card.
	let lastSpanningPlan: ConversationPlan | undefined;
	const currentSpanningPlanTurn = groups.findLast(
		(group) => group.turnId !== undefined && byTurn.get(group.turnId)?.plan?.spansTurns,
	)?.turnId;

	for (const group of groups) {
		if (!group.turnId) continue;
		const turn = byTurn.get(group.turnId);
		if (!turn) continue;
		// The diff and the plan are attached whether or not the turn has finished: a
		// running turn's changed-file list growing, and its checklist ticking itself
		// off, are the useful parts.
		group.diff = turn.diff;
		if (turn.plan?.spansTurns && !hasPlanTab) {
			group.plan = turn.plan.steps.length && group.turnId === currentSpanningPlanTurn ? turn.plan : undefined;
		} else if (turn.plan?.spansTurns) {
			group.planUpdate = planUpdateSince(lastSpanningPlan, turn.plan);
			if (turn.plan.steps.length) lastSpanningPlan = turn.plan;
		} else {
			group.plan = turn.plan?.steps.length ? turn.plan : undefined;
		}
		group.live = turn.state === "running";
		group.liveStartedAt = turn.startedAt ?? turn.requestedAt;
		group.blocked = group.items.some(
			(item) =>
				item.kind === "activity" &&
				(item.activityKind === "approval" || item.activityKind === "user_input") &&
				item.status === "pending",
		);
		group.liveProviderFailure = [...group.items]
			.reverse()
			.find(
				(item): item is ConversationActivity =>
					item.kind === "activity" &&
					item.status === "running" &&
					item.detail?.event === "provider.failure",
			);
		if (turn.state === "running" || turn.state === "queued" || turn.state === "cancelled") continue;
		group.rollbackable = Boolean(turn.providerTurnId);
		group.accountSwitched = group.items.some((item) => item.kind === "activity" && item.detail?.event === "account.switched");
		group.outcome = {
			state: turn.state,
			usageLimit: turn.usageLimit,
			autoRetry: turn.autoRetry,
			retriedTransient: Boolean(turn.transientError && turn.hasRetryAttempt),
			durationMs: settledTurnDuration(turn),
			error: turn.errorMessage || (turn.state === "failed"
				? [...group.items].reverse().find(
					(item): item is ConversationActivity => item.kind === "activity" && item.activityKind === "error",
				)?.summary
				: undefined),
		};
	}

	return groups.flatMap(splitAtSteers);
}

/**
 * A steer is the user speaking, so it opens a new visual turn even though the
 * provider keeps running the same one: everything before it settles into its own
 * "Worked for" row, and the steer starts a fresh Working row and reply. Only the
 * last segment carries the live state, outcome, diff and plan of the real turn.
 */
function splitAtSteers(group: TimelineGroup): TimelineGroup[] {
	const starts = group.items.flatMap((item, index) =>
		index > 0 && item.kind === "activity" && isSteer(item) && !item.detail?.senderSessionId ? [index] : [],
	);
	if (starts.length === 0) return [group];
	const bounds = [0, ...starts, group.items.length];
	const segmentStarts = bounds.slice(0, -1).map((from, index) =>
		index === 0 ? Date.parse(group.liveStartedAt ?? "") : Date.parse(group.items[from]!.createdAt),
	);
	const lastIndex = starts.length;
	return starts.concat(group.items.length).map((_end, index) => {
		const items = group.items.slice(bounds[index], bounds[index + 1]);
		const first = items[0]!;
		const key = index === 0 ? group.key : `${group.key}:steer:${first.id}`;
		const startedAt = segmentStarts[index]!;
		if (index === lastIndex) {
			const priorMs = Number.isFinite(startedAt) ? startedAt - segmentStarts[0]! : 0;
			return {
				...group,
				key,
				anchor: first.sequence,
				items,
				liveStartedAt: Number.isFinite(startedAt) ? new Date(startedAt).toISOString() : group.liveStartedAt,
				outcome: group.outcome
					? {
						...group.outcome,
						durationMs:
							group.outcome.durationMs === undefined || !Number.isFinite(priorMs)
								? group.outcome.durationMs
								: Math.max(1_000, group.outcome.durationMs - priorMs),
					}
					: undefined,
			};
		}
		const endedAt = segmentStarts[index + 1]!;
		return {
			key,
			turnId: group.turnId,
			anchor: first.sequence,
			items,
			outcome: {
				state: "completed" as const,
				durationMs:
					Number.isFinite(startedAt) && Number.isFinite(endedAt)
						? Math.max(1_000, endedAt - startedAt)
						: undefined,
			},
		};
	});
}

export function isHumanRun(item: ConversationItem | undefined): boolean {
	return (item?.kind === "message" && item.role === "user") || (item?.kind === "activity" && isSteer(item));
}

/** Conversation records that stay readable outside a settled turn's folded work. */
export function isNoticeRun(run: TimelineRun): boolean {
	return run.kind === "single" && isNoticeItem(run.items[0]);
}

function isNoticeItem(item: ConversationItem | undefined): boolean {
	if (item?.kind !== "activity") return false;
	if (item.activityKind === "error") return true;
	if (item.activityKind === "user_input") return item.status !== "pending";
	return item.detail?.event !== undefined && !isSteer(item) && !isCompaction(item) && item.activityKind !== "plan";
}

/**
 * A settled turn the provider ran on its own to compact history: no prompt opened
 * it and no reply closed it. Its compaction is the only thing it says, so it draws
 * as the marker rather than an empty "Worked for" row with nothing under it.
 */
export function compactionRuns(runs: TimelineRun[], settled: boolean): TimelineRun[] {
	if (!settled || runs.some((run) => isHumanRun(run.items[0]) || (run.kind === "single" && run.items[0]?.kind === "message" && !rendersNothing(run)))) return [];
	return runs.filter((run) => run.kind === "single" && run.items[0] !== undefined && isCompaction(run.items[0]));
}

const quietWorkerGroups = new WeakMap<TimelineGroup, boolean>();

/**
 * A settled worker turn that draws only rows and a quiet reply, so it can sit
 * tight against its neighbours. Groups keep their identity while unchanged, so
 * each is judged once rather than on every streamed delta.
 */
export function isQuietWorkerGroup(group: TimelineGroup | undefined): boolean {
	if (!group) return false;
	let quiet = quietWorkerGroups.get(group);
	if (quiet === undefined) {
		quiet = group.outcome?.state === "completed"
			&& !group.plan
			&& !group.planUpdate
			&& !group.diff
			&& quietWorkerReply(group.items) !== undefined
			&& !group.items.some(isNoticeItem)
			&& spawnedSessionIds(group.items).length === 0;
		quietWorkerGroups.set(group, quiet);
	}
	return quiet;
}

/** Mirrors the items `TimelineItem` draws nothing for, so they cannot fill an empty accordion. */
export function rendersNothing(run: TimelineRun): boolean {
	const item = run.items[0];
	if (run.kind !== "single" || !item) return false;
	if (item.kind === "message") return item.role === "assistant" && item.text.trim() === "";
	// ReasoningBlock draws nothing for a thought with no text.
	if (item.activityKind === "reasoning") return !(item.detail?.text ?? item.detail?.reason);
	return (item.activityKind === "user_input" || item.activityKind === "approval") && item.status === "pending";
}
