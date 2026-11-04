import type { ChatDraftInlineEdit } from "../../lib/chat-drafts";
import type { Virtualizer } from "@tanstack/react-virtual";
import { useCallback, useEffect, useRef } from "react";
import { TimelineGroup, groupHasHumanPrompt, promptSpacerHeight, promptTopInset } from "./timeline-groups";
import { type ConversationSnapshot } from "../../types/conversation";
import type { Dispatch, SetStateAction, RefObject } from "react";
import { INITIAL_SNAP_MS } from "./timeline-constants";

/**
 * Scrollbar metrics, prompt spacer and follow-to-end layout sync.
 */
export function useTimelineScrollLayout({
	scroller,
	scrollTrack,
	virtualized,
	scrollContent,
	groups,
	virtualizer,
	setScrollbar,
	promptSpacer,
	renderedVirtualHeight,
	initialLayoutDone,
	initialLayoutAt,
	markWritten,
	followEnd,
	skipRelatchFollow,
	pinnedRef,
	glideRequested,
	glideToEnd,
	groupCountRef,
	pinned,
	snapshot,
	messageEdit,
	pendingVirtualLayout,
	virtualHeight,
}: {
	scroller: RefObject<HTMLDivElement | null>;
	scrollTrack: RefObject<HTMLDivElement | null>;
	virtualized: boolean;
	scrollContent: RefObject<HTMLDivElement | null>;
	groups: TimelineGroup[];
	virtualizer: Virtualizer<HTMLDivElement, Element>;
	setScrollbar: Dispatch<SetStateAction<{ visible: boolean; top: number; height: number; percent: number; markers: Array<{ top: number; scrollTop: number; visible: boolean; }>; }>>;
	promptSpacer: RefObject<HTMLDivElement | null>;
	renderedVirtualHeight: RefObject<number>;
	initialLayoutDone: RefObject<boolean>;
	initialLayoutAt: RefObject<number>;
	markWritten: (top: number) => void;
	followEnd: () => void;
	skipRelatchFollow: RefObject<boolean>;
	pinnedRef: RefObject<boolean>;
	glideRequested: RefObject<boolean>;
	glideToEnd: () => void;
	groupCountRef: RefObject<number>;
	pinned: boolean;
	snapshot: ConversationSnapshot;
	messageEdit: ChatDraftInlineEdit | undefined;
	pendingVirtualLayout: RefObject<boolean>;
	virtualHeight: number;
}) {
	// Long transcripts use measured/estimated row positions for the full minimap,
	// including turns that are currently outside the mounted viewport.
	const anchorGeometry = useRef<{ height: number; width: number; positions: number[] } | null>(null);

	const contentMutations = useRef<MutationObserver | null>(null);

	const updateScrollbar = useCallback(() => {
		const node = scroller.current;
		const track = scrollTrack.current;
		if (!node || !track) return;

		const maxScroll = Math.max(0, node.scrollHeight - node.clientHeight);
		const visible = maxScroll > 1;
		const trackHeight = track.clientHeight;
		const height = visible
			? Math.max(40, trackHeight * (node.clientHeight / node.scrollHeight))
			: trackHeight;
		const travel = Math.max(0, trackHeight - height);
		const fraction = maxScroll > 0 ? Math.min(1, Math.max(0, node.scrollTop / maxScroll)) : 0;
		if (contentMutations.current?.takeRecords().length) anchorGeometry.current = null;
		let geometry = anchorGeometry.current;
		if (virtualized || !geometry || geometry.height !== node.scrollHeight || geometry.width !== node.clientWidth) {
			const viewportRect = node.getBoundingClientRect();
			const anchors = Array.from(
				scrollContent.current?.querySelectorAll<HTMLElement>("[data-chat-scroll-anchor]") ?? [],
			);
			geometry = {
				height: node.scrollHeight,
				width: node.clientWidth,
				positions: virtualized ? groups.flatMap((group, index) => {
					const row = virtualizer.measurementsCache[index];
					return groupHasHumanPrompt(group) && row ? [row.start + row.size / 2] : [];
				}) : anchors.map((anchor) => {
					const rect = anchor.getBoundingClientRect();
					return rect.top - viewportRect.top + node.scrollTop + rect.height / 2;
				}),
			};
			anchorGeometry.current = geometry;
		}
		const positions = geometry.positions;
		// 8px is the existing 2px dash + 6px gap cadence.
		const markerGap = positions.length > 1 ? Math.min(8, (trackHeight - 12) / (positions.length - 1)) : 0;
		const markerStart = (trackHeight - markerGap * Math.max(0, positions.length - 1)) / 2;
		const markers = positions.map((contentY, index) => ({
			top: markerStart + index * markerGap,
			scrollTop: Math.min(maxScroll, Math.max(0, contentY - node.clientHeight / 2)),
			visible: contentY >= node.scrollTop && contentY <= node.scrollTop + node.clientHeight,
		}));
		const next = {
			visible,
			top: travel * fraction,
			height,
			percent: Math.round(fraction * 100),
			markers,
		};
		setScrollbar((current) =>
			current.visible === next.visible &&
			Math.abs(current.top - next.top) < 0.5 &&
			Math.abs(current.height - next.height) < 0.5 &&
			current.percent === next.percent &&
			current.markers.length === next.markers.length &&
			current.markers.every((marker, index) => {
				const candidate = next.markers[index];
				return (
					candidate !== undefined &&
					Math.abs(marker.top - candidate.top) < 0.5 &&
					Math.abs(marker.scrollTop - candidate.scrollTop) < 0.5 &&
					marker.visible === candidate.visible
				);
			})
				? current
				: next,
		);
	}, [virtualized, virtualizer, groups]);

	/**
	 * Size the trailing spacer so scrolling to the bottom parks the latest human
	 * prompt below a band of prior chat — matching Cursor, where a little of the
	 * previous turn stays visible above the new prompt instead of flush to the top.
	 * Shrinks naturally as that reply grows.
	 */
	const syncPromptSpacer = useCallback(() => {
		const node = scroller.current;
		const pad = promptSpacer.current;
		if (!node || !pad) return;

		const anchors = scrollContent.current?.querySelectorAll<HTMLElement>(
			"[data-chat-scroll-anchor]",
		);
		const anchor = anchors && anchors.length > 0 ? anchors[anchors.length - 1] : null;
		const lastPromptIndex = groups.findLastIndex(groupHasHumanPrompt);
		const virtualAnchor = virtualized && lastPromptIndex >= 0 ? virtualizer.measurementsCache[lastPromptIndex] : undefined;
		// A just-appended turn is measured in steps: the virtualizer learns its real height
		// before the DOM does, so for a moment the rendered content still carries the
		// 600px estimate. Sized from that, the spacer collapses to zero, the browser clamps
		// scrollTop to the shorter content, and the view jumps up and never comes back.
		// Wait for the DOM to catch up; the resize that follows re-runs this.
		if (virtualized && renderedVirtualHeight.current !== virtualizer.getTotalSize()) return;
		const nextHeight = (virtualAnchor || anchor)
			? promptSpacerHeight({
					viewportHeight: node.clientHeight,
					contentHeightWithoutSpacer: node.scrollHeight - pad.offsetHeight,
					anchorOffset:
						virtualAnchor?.start ?? (anchor!.getBoundingClientRect().top - node.getBoundingClientRect().top + node.scrollTop),
					topInset: promptTopInset(node.clientHeight),
				})
			: 0;
		if (Math.abs(pad.offsetHeight - nextHeight) > 0.5) {
			pad.style.height = `${nextHeight}px`;
		}
	}, [virtualized, virtualizer, groups]);

	// Opening a chat lands at the end instantly. Long histories measure their rows
	// over the first frames (and may arrive in more than one snapshot), so keep
	// snapping briefly after the first real layout instead of animating across them.
	const followOrSnapToEnd = useCallback((node: HTMLElement) => {
		if (!initialLayoutDone.current || performance.now() - initialLayoutAt.current < INITIAL_SNAP_MS) {
			node.scrollTop = node.scrollHeight;
			markWritten(node.scrollTop);
			return;
		}
		followEnd();
	}, [followEnd, markWritten]);

	const syncScrollLayout = useCallback(() => {
		anchorGeometry.current = null;
		syncPromptSpacer();
		const node = scroller.current;
		const relatched = skipRelatchFollow.current;
		skipRelatchFollow.current = false;
		if (node && pinnedRef.current && !(relatched && !glideRequested.current)) {
			if (glideRequested.current) {
				glideRequested.current = false;
				glideToEnd();
			} else {
				followOrSnapToEnd(node);
			}
		}
		if (groupCountRef.current > 0 && !initialLayoutDone.current) {
			initialLayoutDone.current = true;
			initialLayoutAt.current = performance.now();
		}
		updateScrollbar();
	}, [syncPromptSpacer, updateScrollbar, glideToEnd, followOrSnapToEnd]);

	// Streamed text grows the content without a new sequence. While following, let
	// the reply fill the prompt spacer first (the view holds still), then follow the
	// end smoothly once the reply is taller than the viewport.
	const syncStreamedGrowth = useCallback(() => {
		if (!pinnedRef.current || !initialLayoutDone.current) return;
		anchorGeometry.current = null;
		syncPromptSpacer();
		const node = scroller.current;
		if (node) followOrSnapToEnd(node);
		updateScrollbar();
	}, [syncPromptSpacer, followOrSnapToEnd, updateScrollbar]);

	// A disclosure animation changes the content box but is not new conversation
	// content. Re-measure it without re-pinning the viewport to the bottom; doing
	// that during a height animation makes the whole chat jump under the reader.
	const syncScrollMetrics = useCallback(() => {
		anchorGeometry.current = null;
		// UI-only disclosure resizes must not rewrite the trailing spacer. Doing so
		// every animation frame changes the scroll range and causes a small jerk when
		// a panel collapses near the bottom of the viewport.
		updateScrollbar();
	}, [updateScrollbar]);

	useEffect(() => {
		syncScrollLayout();
	}, [pinned, snapshot.latestSequence, groups.length, messageEdit?.turnId, syncScrollLayout]);

	useEffect(() => {
		if (!virtualized) return;
		// Wait for measured sizes to reach the DOM before following new content.
		// Resizing an already measured group only refreshes scroll metrics.
		if (pendingVirtualLayout.current && virtualHeight === virtualizer.getTotalSize()) {
			pendingVirtualLayout.current = false;
			syncScrollLayout();
		} else syncScrollMetrics();
	}, [virtualized, virtualHeight, virtualizer, syncScrollLayout, syncScrollMetrics]);

	useEffect(() => {
		const content = scrollContent.current;
		const mutations = new MutationObserver(() => { anchorGeometry.current = null; });
		if (content) mutations.observe(content, {
			subtree: true, childList: true, characterData: true, attributes: true,
			attributeFilter: ["class", "style", "hidden", "open", "data-chat-scroll-anchor"],
		});
		contentMutations.current = mutations;
		return () => {
			mutations.disconnect();
			contentMutations.current = null;
			anchorGeometry.current = null;
		};
	}, []);

	// Read through a ref so a streamed chunk (new `groups` identity) does not tear
	// down and re-arm the observer, which would re-run a full layout per chunk.
	const onContentResize = useRef(syncScrollMetrics);

	onContentResize.current = () => {
		if (pinnedRef.current) syncStreamedGrowth();
		else syncScrollMetrics();
	};

	useEffect(() => {
		syncScrollLayout();
		if (typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver(() => onContentResize.current());
		if (scroller.current) observer.observe(scroller.current);
		if (scrollContent.current) observer.observe(scrollContent.current);
		return () => observer.disconnect();
	}, [groups.length]);

	return { updateScrollbar };
}
