import type { Virtualizer } from "@tanstack/react-virtual";
import {
	type KeyboardEvent as ReactKeyboardEvent,
	type PointerEvent as ReactPointerEvent,
	type WheelEvent as ReactWheelEvent,
} from "react";
import { TimelineGroup } from "./timeline-groups";
import type { Dispatch, SetStateAction, RefObject } from "react";
import { READER_INTENT_HOLD_MS } from "./timeline-constants";

/**
 * Wheel, key, pointer and scrollbar-drag handlers for the transcript viewport.
 */
export function useTimelineScrollInput({
	scroller,
	lastScrollTop,
	isOwnScroll,
	pinnedRef,
	releaseFollow,
	skipRelatchFollow,
	setPinnedNow,
	updateScrollbar,
	holdScrollFollow,
	cancelScrollFollow,
	scrollTrack,
	scrollbar,
	minimapEnabled,
	inspectorOpenRef,
	navigableGroups,
	virtualized,
	virtualizer,
	groups,
	drag,
	setHoveredMarker,
}: {
	scroller: RefObject<HTMLDivElement | null>;
	lastScrollTop: RefObject<number>;
	isOwnScroll: (top: number) => boolean;
	pinnedRef: RefObject<boolean>;
	releaseFollow: () => void;
	skipRelatchFollow: RefObject<boolean>;
	setPinnedNow: (next: boolean) => void;
	updateScrollbar: () => void;
	holdScrollFollow: (ms: number) => void;
	cancelScrollFollow: () => void;
	scrollTrack: RefObject<HTMLDivElement | null>;
	scrollbar: { visible: boolean; top: number; height: number; percent: number; markers: Array<{ top: number; scrollTop: number; visible: boolean; }>; };
	minimapEnabled: boolean;
	inspectorOpenRef: RefObject<boolean>;
	navigableGroups: TimelineGroup[];
	virtualized: boolean;
	virtualizer: Virtualizer<HTMLDivElement, Element>;
	groups: TimelineGroup[];
	drag: RefObject<{ pointerId: number; startY: number; startScrollTop: number; } | null>;
	setHoveredMarker: Dispatch<SetStateAction<number | null>>;
}) {
	function onScroll() {
		const node = scroller.current;
		if (!node) return;
		const top = node.scrollTop;
		const movedUp = top < lastScrollTop.current - 1;
		lastScrollTop.current = top;
		const distance = node.scrollHeight - top - node.clientHeight;
		// Classified by where the scroll landed, not when: our own glide, follow and
		// anchoring writes are recognized by position and never unpin. A reader scroll
		// releases the follow once it is away from the end, or whenever it moves up
		// (so a small nudge up is not pulled straight back). A scroll that only clamps
		// to the end after content shrank is not the reader leaving.
		if (isOwnScroll(top)) {
			// Ours: no change.
		} else if (distance >= 64 || (movedUp && distance > 2)) {
			if (pinnedRef.current) releaseFollow();
		} else if (!pinnedRef.current && !movedUp) {
			// The reader is already here: the follow layout this re-pin triggers must not
			// snap the last few pixels under their scroll, which reads as a jitter.
			skipRelatchFollow.current = true;
			setPinnedNow(true);
		}
		updateScrollbar();
	}

	function onViewportWheel(event: ReactWheelEvent<HTMLDivElement>) {
		// Wheel intent arrives before the scroll it causes. Hold the follow so a
		// concurrent stream commit cannot pull the reader back, and let the scroll
		// itself decide: a wheel consumed by a nested scroller (a code block) never
		// moves the log, so it must not unpin it.
		if (event.deltaY < 0) holdScrollFollow(READER_INTENT_HOLD_MS);
	}

	function onViewportKeyDown(event: ReactKeyboardEvent<HTMLDivElement>) {
		const target = event.target as HTMLElement;
		if (event.defaultPrevented || target.closest("input, textarea, select, [contenteditable='true'], [contenteditable=''], [role='menu'], [role='listbox'], [role='textbox']")) return;
		if (event.key === "ArrowUp" || event.key === "PageUp" || event.key === "Home" || (event.key === " " && event.shiftKey)) {
			holdScrollFollow(READER_INTENT_HOLD_MS);
		}
	}

	function onViewportPointerDown(event: ReactPointerEvent<HTMLDivElement>) {
		// A disclosure the reader is opening: following the end would pull it out from
		// under them. Menu triggers (aria-haspopup) open a popup, not content, so they
		// keep the follow; so do clicks on empty margins.
		if ((event.target as HTMLElement).closest("[aria-expanded]:not([aria-haspopup])")) {
			releaseFollow();
		}
	}

	function setScrollFromTrack(clientY: number) {
		cancelScrollFollow();
		const node = scroller.current;
		const track = scrollTrack.current;
		if (!node || !track) return;
		const rect = track.getBoundingClientRect();
		const travel = Math.max(1, track.clientHeight - scrollbar.height);
		const top = Math.min(travel, Math.max(0, clientY - rect.top - scrollbar.height / 2));
		node.scrollTop = (top / travel) * Math.max(0, node.scrollHeight - node.clientHeight);
		updateScrollbar();
	}

	function onScrollbarPointerDown(event: ReactPointerEvent<HTMLDivElement>) {
		cancelScrollFollow();
		if (!minimapEnabled || inspectorOpenRef.current) return;
		const track = scrollTrack.current;
		const node = scroller.current;
		if (!track || !node) return;
		const marker = (event.target as HTMLElement).closest<HTMLElement>("[data-chat-scroll-marker]");
		if (marker?.dataset.scrollTarget) {
			const group = navigableGroups[Number(marker.dataset.markerIndex)];
			if (virtualized && group) {
				virtualizer.scrollToIndex(groups.findIndex((candidate) => candidate.key === group.key), { align: "center" });
			} else {
				node.scrollTop = Number(marker.dataset.scrollTarget);
			}
			onScroll();
			return;
		}
		setScrollFromTrack(event.clientY);
		drag.current = {
			pointerId: event.pointerId,
			startY: event.clientY,
			startScrollTop: node.scrollTop,
		};
		event.currentTarget.setPointerCapture(event.pointerId);
	}

	function onScrollbarPointerMove(event: ReactPointerEvent<HTMLDivElement>) {
		if (!minimapEnabled || inspectorOpenRef.current) return;
		const active = drag.current;
		const node = scroller.current;
		const track = scrollTrack.current;
		if (!node || !track) return;
		if (!active) {
			const pointerY = event.clientY - track.getBoundingClientRect().top;
			let nearest = 0;
			for (let index = 1; index < scrollbar.markers.length; index += 1) {
				if (
					Math.abs(scrollbar.markers[index]!.top - pointerY) <
					Math.abs(scrollbar.markers[nearest]!.top - pointerY)
				) {
					nearest = index;
				}
			}
			if (scrollbar.markers.length > 0) setHoveredMarker(nearest);
			return;
		}
		if (active.pointerId !== event.pointerId) return;
		const travel = Math.max(1, track.clientHeight - scrollbar.height);
		const maxScroll = Math.max(0, node.scrollHeight - node.clientHeight);
		node.scrollTop = active.startScrollTop + ((event.clientY - active.startY) / travel) * maxScroll;
		updateScrollbar();
	}

	function stopScrollbarDrag(event: ReactPointerEvent<HTMLDivElement>) {
		if (drag.current?.pointerId !== event.pointerId) return;
		drag.current = null;
		if (event.currentTarget.hasPointerCapture(event.pointerId)) {
			event.currentTarget.releasePointerCapture(event.pointerId);
		}
	}

	function onScrollbarWheel(event: ReactWheelEvent<HTMLDivElement>) {
		cancelScrollFollow();
		if (!minimapEnabled || inspectorOpenRef.current) return;
		const node = scroller.current;
		if (!node) return;
		event.preventDefault();
		node.scrollTop += event.deltaY;
		onScroll();
	}

	function onScrollbarKeyDown(event: ReactKeyboardEvent<HTMLDivElement>) {
		cancelScrollFollow();
		if (!minimapEnabled || inspectorOpenRef.current) return;
		const node = scroller.current;
		if (!node) return;
		const maxScroll = Math.max(0, node.scrollHeight - node.clientHeight);
		const page = Math.max(48, node.clientHeight * 0.85);
		const next: Record<string, number> = {
			ArrowUp: node.scrollTop - 48,
			ArrowDown: node.scrollTop + 48,
			PageUp: node.scrollTop - page,
			PageDown: node.scrollTop + page,
			Home: 0,
			End: maxScroll,
		};
		if (!(event.key in next)) return;
		event.preventDefault();
		node.scrollTop = Math.min(maxScroll, Math.max(0, next[event.key]!));
		updateScrollbar();
	}

	return {
		onScroll,
		onViewportWheel,
		onViewportKeyDown,
		onViewportPointerDown,
		onScrollbarPointerDown,
		onScrollbarPointerMove,
		stopScrollbarDrag,
		onScrollbarWheel,
		onScrollbarKeyDown,
	};
}
