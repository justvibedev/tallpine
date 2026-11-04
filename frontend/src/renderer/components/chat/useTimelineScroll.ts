import { useCallback, useRef, useState } from "react";
import { useScrollFollow } from "../../hooks/useScrollFollow";
import { TimelineGroup } from "./timeline-groups";

/**
 * Scroll containers, the pin/follow state and the glide-to-end controls of the transcript.
 */
export function useTimelineScroll() {
	const scroller = useRef<HTMLDivElement>(null);

	const scrollContent = useRef<HTMLDivElement>(null);

	const virtualContent = useRef<HTMLDivElement>(null);

	const [virtualScrollMargin, setVirtualScrollMargin] = useState(20);

	const measuredVirtualGroups = useRef(new WeakSet<TimelineGroup>());

	const pendingVirtualLayout = useRef(false);

	const renderedVirtualHeight = useRef(0);

	const promptSpacer = useRef<HTMLDivElement>(null);

	const scrollTrack = useRef<HTMLDivElement>(null);

	const drag = useRef<{
		pointerId: number;
		startY: number;
		startScrollTop: number;
	} | null>(null);

	const pinnedRef = useRef(true);

	const [pinned, setPinned] = useState(true);

	const getScroller = useCallback(() => scroller.current, []);

	const {
		glideToEnd,
		followEnd,
		cancel: cancelScrollFollow,
		hold: holdScrollFollow,
		markWritten,
		isOwnScroll,
		isFlying,
		reaim,
		endFlight,
	} = useScrollFollow(getScroller);

	// Set when the reader sends or jumps to the latest: the next layout glides there
	// instead of snapping. Opening a chat never animates, so the first layout snaps.
	const glideRequested = useRef(false);

	const initialLayoutDone = useRef(false);

	const initialLayoutAt = useRef(0);

	const lastScrollTop = useRef(0);

	const skipRelatchFollow = useRef(false);

	const setPinnedNow = useCallback((next: boolean) => {
		pinnedRef.current = next;
		setPinned(next);
	}, []);

	// The reader took over (scrolled up, opened a disclosure): stop following.
	const releaseFollow = useCallback(() => {
		cancelScrollFollow();
		if (pinnedRef.current) setPinnedNow(false);
	}, [cancelScrollFollow, setPinnedNow]);

	const requestGlideToEnd = useCallback(() => {
		glideRequested.current = true;
		setPinnedNow(true);
	}, [setPinnedNow]);

	const [activityDisclosureOverrides, setActivityDisclosureOverrides] = useState<Record<string, boolean>>({});

	const onActivityDisclosureChange = useCallback((key: string, open: boolean) => {
		setActivityDisclosureOverrides((current) => ({ ...current, [key]: open }));
	}, []);

	return {
		scroller,
		scrollContent,
		virtualContent,
		virtualScrollMargin,
		setVirtualScrollMargin,
		measuredVirtualGroups,
		pendingVirtualLayout,
		renderedVirtualHeight,
		promptSpacer,
		scrollTrack,
		drag,
		pinnedRef,
		pinned,
		glideToEnd,
		followEnd,
		cancelScrollFollow,
		holdScrollFollow,
		markWritten,
		isOwnScroll,
		isFlying,
		reaim,
		endFlight,
		glideRequested,
		initialLayoutDone,
		initialLayoutAt,
		lastScrollTop,
		skipRelatchFollow,
		setPinnedNow,
		releaseFollow,
		requestGlideToEnd,
		activityDisclosureOverrides,
		setActivityDisclosureOverrides,
		onActivityDisclosureChange,
	};
}
