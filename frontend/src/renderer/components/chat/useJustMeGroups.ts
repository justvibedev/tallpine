import { useCallback, useMemo, useState } from "react";
import { useStableList } from "../../lib/stable-list";
import { useChatJustMeStore } from "../../stores/chat-just-me-store";
import { justMeGroups } from "./just-me";
import { groupKey, sameGroup, type TimelineGroup } from "./timeline-groups";

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
	const shown = useMemo(() => (justMe ? justMeGroups(groups, expanded) : groups), [groups, expanded, justMe]);
	return { groups: useStableList(shown, groupKey, sameGroup), toggleRun, justMe };
}
