import { isSteer, type ConversationItem } from "../../types/conversation";
import { foldWorkerRuns, isOwnerGroup, needsOwner } from "./fold-worker-runs";
import type { TimelineGroup } from "./timeline-groups";

/** Prefix of a collapsed run's key; the rest is the key of the run's first group. */
export const JUST_ME_RUN_PREFIX = "just-me:";
/** Prefix of the line folding an owner turn's own tool rows; the rest is the turn's key. */
export const JUST_ME_TURN_PREFIX = "just-me-turn:";

/** The owner's words, the assistant's replies, and anything waiting on the owner. */
function ownerFacing(item: ConversationItem): boolean {
	if (item.kind === "message") return item.role === "assistant" || item.origin === "human";
	if (isSteer(item)) return true;
	return needsOwner(item) || item.activityKind === "error";
}

/**
 * The "Just me" view of a transcript: each owner turn keeps only its messages, and
 * the rest of it (tool rows, compaction) folds into one line under the turn. Every
 * run of orchestration turns between owner turns (worker reports, [auto] notices)
 * folds into one line too. `TimelineGroupRow` draws each line as one expandable
 * row; a line whose key is in `expanded` also shows what it folds.
 */
export function justMeGroups(groups: readonly TimelineGroup[], expanded: ReadonlySet<string>): TimelineGroup[] {
	return foldWorkerRuns(groups, expanded, 1, {
		prefix: JUST_ME_RUN_PREFIX,
		foldable: (group) => !isOwnerGroup(group),
		pass: (group, out) => {
			const items = group.items.filter(ownerFacing);
			const hidden = group.items.length - items.length;
			if (hidden === 0) {
				out.push(group);
				return;
			}
			// The turn's own tool rows and notices fold into a line under it, like a run does.
			const key = `${JUST_ME_TURN_PREFIX}${group.key}`;
			const open = expanded.has(key);
			out.push(open ? group : { ...group, items });
			out.push({ key, anchor: group.anchor, items: [], live: group.live, collapsedRun: { count: hidden, open } });
		},
	});
}
