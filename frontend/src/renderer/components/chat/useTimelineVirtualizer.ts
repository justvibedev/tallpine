import type { ChatDraftInlineEdit } from "../../lib/chat-drafts";
import { useCallback, useMemo, useRef } from "react";
import {
	defaultRangeExtractor,
	measureElement,
	observeElementRect,
	useVirtualizer,
} from "@tanstack/react-virtual";
import { TimelineGroup, groupHasHumanPrompt, groupPreview } from "./timeline-groups";
import { RefObject } from "react";
import { CHAT_ESTIMATED_TURN_HEIGHT, CHAT_INITIAL_VIEWPORT_HEIGHT, CHAT_TURN_GAP, CHAT_VIRTUALIZE_THRESHOLD } from "./timeline-constants";

/**
 * Row virtualization for long transcripts.
 */
export function useTimelineVirtualizer({
	groups,
	scroller,
	virtualScrollMargin,
	pinned,
	measuredVirtualGroups,
	pendingVirtualLayout,
	isFlying,
	reaim,
	markWritten,
	messageEdit,
	renderedVirtualHeight,
}: {
	groups: TimelineGroup[];
	scroller: RefObject<HTMLDivElement | null>;
	virtualScrollMargin: number;
	pinned: boolean;
	measuredVirtualGroups: RefObject<WeakSet<TimelineGroup>>;
	pendingVirtualLayout: RefObject<boolean>;
	isFlying: () => boolean;
	reaim: () => void;
	markWritten: (top: number) => void;
	messageEdit: ChatDraftInlineEdit | undefined;
	renderedVirtualHeight: RefObject<number>;
}) {
	// Read by syncScrollLayout (a stable callback) so the initial snap only latches
	// once real conversation content exists (history can load after the chat opens).
	const groupCountRef = useRef(groups.length);

	groupCountRef.current = groups.length;

	const navigableGroups = useMemo(() => groups.filter(groupHasHumanPrompt), [groups]);

	const previews = useMemo(() => navigableGroups.map(groupPreview), [navigableGroups]);

	const virtualized = groups.length > CHAT_VIRTUALIZE_THRESHOLD;

	const virtualItemKey = useCallback((index: number) => groups[index]!.key, [groups]);

	const virtualizer = useVirtualizer({
		// Scroll anchoring may notify during a layout effect; defer the React
		// update rather than attempting a synchronous flush inside that effect.
		useFlushSync: false,
		// Measure short histories too, so crossing the windowing threshold can
		// preserve the visible turn by key rather than restarting at the bottom.
		enabled: groups.length > 0,
		count: groups.length,
		getScrollElement: () => scroller.current,
		getItemKey: virtualItemKey,
		estimateSize: () => CHAT_ESTIMATED_TURN_HEIGHT,
		gap: CHAT_TURN_GAP,
		overscan: 2,
		scrollMargin: virtualScrollMargin,
		initialRect: { width: 768, height: CHAT_INITIAL_VIEWPORT_HEIGHT },
		initialOffset: () => virtualized
			? Math.max(0, groups.length * (CHAT_ESTIMATED_TURN_HEIGHT + CHAT_TURN_GAP) - CHAT_INITIAL_VIEWPORT_HEIGHT)
			: 0,
		anchorTo: virtualized ? "end" : "start",
		// The virtualizer's distance-from-end excludes the trailing prompt spacer.
		// Disable end anchoring while unpinned so streamed output cannot mistake the
		// reader's position for the physical end of the scroll container.
		scrollEndThreshold: pinned ? 1 : -1,
		// Appends are followed by syncScrollLayout, which glides and accounts for the
		// prompt spacer; the virtualizer's own follow would snap past both.
		followOnAppend: false,
		// Bootstrap unmeasurable panels until their real geometry is available.
		observeElementRect: (instance, callback) => observeElementRect(instance, (rect) =>
			callback(rect.height ? rect : { width: rect.width, height: CHAT_INITIAL_VIEWPORT_HEIGHT })),
		measureElement: (element, entry, instance) => {
			const group = groups[Number(element.getAttribute("data-index"))];
			if (group && !measuredVirtualGroups.current.has(group)) {
				measuredVirtualGroups.current.add(group);
				pendingVirtualLayout.current = true;
			}
			return measureElement(element, entry, instance) || CHAT_ESTIMATED_TURN_HEIGHT;
		},
		scrollToFn: (offset, { adjustments = 0 }, instance) => {
			if (!instance.scrollElement) return;
			// An instant write would abort our smooth glide (and land at a stale offset).
			// Let the glide carry the adjustment: re-aim it at the new end instead. The
			// next scroll event re-syncs the virtualizer's offset.
			if (isFlying()) {
				reaim();
				return;
			}
			instance.scrollElement.scrollTop = offset + adjustments;
			// Anchoring writes are ours; they must never read as the reader leaving the end.
			markWritten(instance.scrollElement.scrollTop);
		},
		rangeExtractor: (range) => {
			const indexes = defaultRangeExtractor(range);
			// Keep an in-progress inline edit mounted while its row leaves view.
			const editing = messageEdit ? groups.findIndex((group) => group.turnId === messageEdit.turnId) : -1;
			if (editing >= 0 && !indexes.includes(editing)) indexes.push(editing);
			if (pinned && !indexes.includes(groups.length - 1)) indexes.push(groups.length - 1);
			return indexes.sort((a, b) => a - b);
		},
	});

	virtualizer.shouldAdjustScrollPositionOnItemSizeChange = virtualized ? undefined : () => false;

	const virtualRows = virtualizer.getVirtualItems();

	const virtualHeight = virtualizer.getTotalSize();

	renderedVirtualHeight.current = virtualHeight;

	const renderedGroups = virtualized
		? virtualRows.map((row) => ({ group: groups[row.index]!, row, index: row.index }))
		: groups.map((group, index) => ({ group, row: undefined, index }));

	return {
		groupCountRef,
		navigableGroups,
		previews,
		virtualized,
		virtualizer,
		virtualRows,
		virtualHeight,
		renderedGroups,
	};
}
