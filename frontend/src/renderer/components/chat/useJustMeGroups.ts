import { useCallback, useMemo, useState } from "react";
import { useStableList } from "../../lib/stable-list";
import { useChatJustMeStore } from "../../stores/chat-just-me-store";
import { foldWorkerRuns, isWorkerRunGroup } from "./fold-worker-runs";
import { justMeGroups } from "./just-me";
import { groupKey, sameGroup, type TimelineGroup } from "./timeline-groups";

/** Runs of this many worker turns fold in the default view. */
const WORKER_RUN_MIN = 2;
const WORKER_RUN_PREFIX = "worker-run:";

/** The groups the transcript draws: all of them, or the persisted "Just me" view. */
export function useJustMeGroups(groups: TimelineGroup[]) {
	const justMe = useChatJustMeStore((state) => state.justMe);
	const [expanded, setExpanded] = useState<ReadonlySet<string>>(() => new Set());
	const toggleRun = useCallback((key: string) => {
		setExpanded((current) => {
			const next = new Set(current);
			if (!next.delete(key)) next.add(key);
			return next;
		});
	}, []);
	const shown = useMemo(() => (justMe ? justMeGroups(groups, expanded) : foldWorkerRuns(groups, expanded, WORKER_RUN_MIN, {
		prefix: WORKER_RUN_PREFIX,
		foldable: isWorkerRunGroup,
		pass: (group, out) => void out.push(group),
		workers: true,
	})), [groups, expanded, justMe]);
	return { groups: useStableList(shown, groupKey, sameGroup), toggleRun, justMe };
}
