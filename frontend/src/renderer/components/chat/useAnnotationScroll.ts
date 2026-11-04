import type { VirtualItem } from "@tanstack/react-virtual";
import type { Virtualizer } from "@tanstack/react-virtual";
import { useEffect } from "react";
import {
	annotationBody,
	annotationTextRange,
	highlightChatAnnotation,
} from "../../lib/chat-annotation-navigation";
import { TimelineGroup } from "./timeline-groups";
import type { Dispatch, SetStateAction, RefObject } from "react";

/**
 * Brings the annotation target into view and highlights it.
 */
export function useAnnotationScroll({
	annotationTarget,
	groups,
	loadingOlder,
	hasOlder,
	onLoadOlder,
	annotationOlderRequestGroups,
	setAnnotationNavigationError,
	setAnnotationTarget,
	scrollContent,
	activityDisclosureOverrides,
	setActivityDisclosureOverrides,
	virtualized,
	virtualizer,
	annotationRetryCount,
	annotationHighlightTimer,
	annotationHighlightCleanup,
	scroller,
	virtualRows,
}: {
	annotationTarget: { text: string; messageId?: string; revision?: number; } | undefined;
	groups: TimelineGroup[];
	loadingOlder: boolean | undefined;
	hasOlder: boolean | undefined;
	onLoadOlder: (() => void) | undefined;
	annotationOlderRequestGroups: RefObject<number | undefined>;
	setAnnotationNavigationError: Dispatch<SetStateAction<string | undefined>>;
	setAnnotationTarget: Dispatch<SetStateAction<{ text: string; messageId?: string; revision?: number; } | undefined>>;
	scrollContent: RefObject<HTMLDivElement | null>;
	activityDisclosureOverrides: Record<string, boolean>;
	setActivityDisclosureOverrides: Dispatch<SetStateAction<Record<string, boolean>>>;
	virtualized: boolean;
	virtualizer: Virtualizer<HTMLDivElement, Element>;
	annotationRetryCount: RefObject<number>;
	annotationHighlightTimer: RefObject<number | undefined>;
	annotationHighlightCleanup: RefObject<(() => void) | undefined>;
	scroller: RefObject<HTMLDivElement | null>;
	virtualRows: VirtualItem[];
}) {
	useEffect(() => {
		if (!annotationTarget) return;
		const index = groups.findIndex((group) => group.items.some((item) => item.id === annotationTarget.messageId));
		if (index < 0) {
			if (loadingOlder) return;
			if (hasOlder && onLoadOlder && annotationOlderRequestGroups.current !== groups.length) {
				annotationOlderRequestGroups.current = groups.length;
				onLoadOlder();
				return;
			}
			setAnnotationNavigationError("The referenced message is no longer available in this chat.");
			setAnnotationTarget(undefined);
			return;
		}
		const source = Array.from(scrollContent.current?.querySelectorAll<HTMLElement>("[data-chat-message-id]") ?? [])
			.find((element) => element.dataset.chatMessageId === annotationTarget.messageId);
		if (!source) {
			const disclosureKey = `${groups[index].key}:worked`;
			if (!activityDisclosureOverrides[disclosureKey]) {
				setActivityDisclosureOverrides((current) => ({ ...current, [disclosureKey]: true }));
			}
			if (virtualized) virtualizer.scrollToIndex(index, { align: "center" });
			if (annotationRetryCount.current++ >= 20) {
				setAnnotationNavigationError("The referenced message could not be brought into view.");
				setAnnotationTarget(undefined);
				return;
			}
			const retryTimer = window.setTimeout(() => {
				setAnnotationTarget((current) => current ? { ...current } : current);
			}, 80);
			return () => window.clearTimeout(retryTimer);
		}
		// Wait until the popover has closed/restored focus before scrolling.
		const frame = requestAnimationFrame(() => {
			window.clearTimeout(annotationHighlightTimer.current);
			annotationHighlightCleanup.current?.();
			annotationHighlightCleanup.current = highlightChatAnnotation(source, annotationTarget.text, annotationTarget.revision);
			const scrollerNode = scroller.current;
			let range = annotationTextRange(annotationBody(source), annotationTarget.text);
			if (range && scrollerNode) {
				const rangeRect = range.getBoundingClientRect();
				const scrollerRect = scrollerNode.getBoundingClientRect();
				const padding = 12;
				const visible = rangeRect.top >= scrollerRect.top + padding && rangeRect.bottom <= scrollerRect.bottom - padding;
				if (!visible) {
					source.scrollIntoView({ behavior: "auto", block: "center" });
					// Re-read after the source jump; layout may have moved the selected
					// range before the final centering adjustment.
					range = annotationTextRange(annotationBody(source), annotationTarget.text);
					if (range) {
						const nextRect = range.getBoundingClientRect();
						const delta = nextRect.top + nextRect.height / 2 - (scrollerRect.top + scrollerRect.height / 2);
						scrollerNode.scrollTo({ top: scrollerNode.scrollTop + delta, behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
					}
				}
			}
			annotationHighlightTimer.current = window.setTimeout(() => annotationHighlightCleanup.current?.(), 2200);
			setAnnotationTarget(undefined);
		});
		return () => cancelAnimationFrame(frame);
	}, [annotationTarget, groups, hasOlder, loadingOlder, onLoadOlder, virtualized, virtualizer, virtualRows, activityDisclosureOverrides]);
}
