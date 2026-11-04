import { isSteer, type ConversationItem } from "../../types/conversation";
import { replyNeedsOwner } from "./WorkerUpdates";
import { groupHasHumanPrompt, type TimelineGroup } from "./timeline-groups";

/** Prefix of a collapsed run's key; the rest is the key of the run's first group. */
export const JUST_ME_RUN_PREFIX = "just-me:";
/** Prefix of the line folding an owner turn's own tool rows; the rest is the turn's key. */
export const JUST_ME_TURN_PREFIX = "just-me-turn:";

/** Something the owner must act on stays visible whatever produced it. */
function needsOwner(item: ConversationItem): boolean {
	return item.kind === "activity"
		&& (item.activityKind === "approval" || item.activityKind === "user_input")
		&& item.status === "pending";
}

/** The owner's words, the assistant's replies, and anything waiting on the owner. */
function ownerFacing(item: ConversationItem): boolean {
	if (item.kind === "message") return item.role === "assistant" || item.origin === "human";
	if (isSteer(item)) return true;
	return needsOwner(item) || item.activityKind === "error";
}

function isOwnerGroup(group: TimelineGroup): boolean {
	return groupHasHumanPrompt(group) || group.items.some(needsOwner) || replyNeedsOwner(group.items);
}

/**
 * The "Just me" view of a transcript: each owner turn keeps only its messages, and
 * the rest of it (tool rows, compaction) folds into one line under the turn. Every
 * run of orchestration turns between owner turns (worker reports, [auto] notices)
 * folds into one line too. `TimelineGroupRow` draws each line as one expandable
 * row; a line whose key is in `expanded` also shows what it folds.
 */
export function justMeGroups(groups: readonly TimelineGroup[], expanded: ReadonlySet<string>): TimelineGroup[] {
	const out: TimelineGroup[] = [];
	let run: TimelineGroup[] = [];
	const flush = () => {
		if (run.length === 0) return;
		const key = `${JUST_ME_RUN_PREFIX}${run[0].key}`;
		const open = expanded.has(key);
		// The line stays while the run is open, so the reader can fold it again.
		out.push({
			key,
			anchor: run[0].anchor,
			items: open ? [] : run.flatMap((group) => group.items),
			live: run.some((group) => group.live),
			collapsedRun: { count: run.length, open },
		});
		if (open) out.push(...run);
		run = [];
	};
	for (const group of groups) {
		if (!isOwnerGroup(group)) {
			run.push(group);
			continue;
		}
		flush();
		const items = group.items.filter(ownerFacing);
		const hidden = group.items.length - items.length;
		if (hidden === 0) {
			out.push(group);
			continue;
		}
		// The turn's own tool rows and notices fold into a line under it, like a run does.
		const key = `${JUST_ME_TURN_PREFIX}${group.key}`;
		const open = expanded.has(key);
		out.push(open ? group : { ...group, items });
		out.push({ key, anchor: group.anchor, items: [], live: group.live, collapsedRun: { count: hidden, open } });
	}
	flush();
	return out;
}
