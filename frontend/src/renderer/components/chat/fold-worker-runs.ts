import { splitArtifacts, splitHeadline } from "@tallpine/shared/chat/automation/worker-reports";
import type { ConversationItem } from "../../types/conversation";
import { finalReply, hasNeedsYouMarker, isWorkerLead, workerUpdatesOf } from "./WorkerUpdates";
import { groupHasHumanPrompt, type TimelineGroup } from "./timeline-groups";

/** Something the owner must act on stays visible whatever produced it. */
export function needsOwner(item: ConversationItem): boolean {
	return item.kind === "activity"
		&& (item.activityKind === "approval" || item.activityKind === "user_input")
		&& item.status === "pending";
}

const isAsk = (item: ConversationItem): boolean => item.kind === "activity" && item.detail?.event === "ask";

/** A turn the owner must see: their own words, an ask, a "Needs you:" reply, a pending decision. */
export function isOwnerGroup(group: TimelineGroup): boolean {
	return groupHasHumanPrompt(group) || group.items.some((item) => needsOwner(item) || isAsk(item)) || hasNeedsYouMarker(finalReply(group.items)?.text ?? "");
}

const AUTO_PREFIX = /^\s*\[auto\]\s*/i;
const AUTO_NOTICE = /^\s*\[auto\]/i;

/** A turn a worker or an [auto] notice opened that holds nothing the owner must act on. */
export function isWorkerRunGroup(group: TimelineGroup): boolean {
	if (isOwnerGroup(group)) return false;
	const lead = group.items[0];
	if (!lead) return false;
	if (group.items.some((item) => item.kind === "activity" && item.activityKind === "error")) return false;
	return isWorkerLead(lead) || (lead.kind === "message" && lead.origin === "automation" && AUTO_NOTICE.test(lead.text));
}

/** The headline of the newest worker update in a run, so the folded row still shows where things stand. */
function newestHeadline(run: readonly TimelineGroup[]): string | undefined {
	for (let g = run.length - 1; g >= 0; g -= 1) {
		const items = run[g].items;
		for (let i = items.length - 1; i >= 0; i -= 1) {
			const item = items[i];
			const updates = workerUpdatesOf(item);
			const body = updates.length > 0
				? updates[updates.length - 1].body
				: item.kind === "message" && item.origin === "automation" ? item.text : undefined;
			if (!body) continue;
			const headline = splitHeadline(splitArtifacts(body).text).headline.replace(AUTO_PREFIX, "").trim();
			if (headline) return headline;
		}
	}
	return undefined;
}

export interface FoldOptions {
	/** Prefix of the folded row's key; the rest is the key of the run's first group. */
	prefix: string;
	/** Which groups may join a run. */
	foldable: (group: TimelineGroup) => boolean;
	/** Draws a group that is not part of a folded run (an owner group, or a run too short to fold). */
	pass: (group: TimelineGroup, out: TimelineGroup[]) => void;
	/** Label the folded row with the newest worker headline. */
	workers?: boolean;
}

/**
 * Folds each run of at least `minRun` consecutive foldable groups into one line
 * (`collapsedRun`) that `TimelineGroupRow` draws as an expandable row. A line whose
 * key is in `expanded` stays and also shows the groups it folds.
 */
export function foldWorkerRuns(groups: readonly TimelineGroup[], expanded: ReadonlySet<string>, minRun: number, opts: FoldOptions): TimelineGroup[] {
	const out: TimelineGroup[] = [];
	let run: TimelineGroup[] = [];
	const flush = () => {
		if (run.length === 0) return;
		if (run.length < minRun) {
			for (const group of run) opts.pass(group, out);
		} else {
			const key = `${opts.prefix}${run[0].key}`;
			const open = expanded.has(key);
			out.push({
				key,
				anchor: run[0].anchor,
				items: open ? [] : run.flatMap((group) => group.items),
				live: run.some((group) => group.live),
				collapsedRun: opts.workers ? { count: run.length, open, workers: true, headline: newestHeadline(run) } : { count: run.length, open },
			});
			if (open) out.push(...run);
		}
		run = [];
	};
	for (const group of groups) {
		if (opts.foldable(group)) {
			run.push(group);
			continue;
		}
		flush();
		opts.pass(group, out);
	}
	flush();
	return out;
}
