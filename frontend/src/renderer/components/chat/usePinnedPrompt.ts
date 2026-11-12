import { useCallback, useEffect, useState, type RefObject } from "react";
import type { Virtualizer } from "@tanstack/react-virtual";
import { groupHasHumanPrompt, type TimelineGroup } from "./timeline-groups";

export type PinnedPromptInfo = { index: number; id: string; text: string };

/** The latest human prompt in the transcript: its group index and plain text. */
export function latestPromptInfo(groups: readonly TimelineGroup[]): PinnedPromptInfo | undefined {
	for (let index = groups.length - 1; index >= 0; index--) {
		const group = groups[index]!;
		if (!groupHasHumanPrompt(group)) continue;
		const message = group.items.findLast((item) => item.kind === "message" && item.role === "user" && item.origin === "human");
		const text = message?.kind === "message" ? message.text.replace(/\s+/g, " ").trim() : "";
		return message && text ? { index, id: message.id, text } : undefined;
	}
	return undefined;
}

/**
 * Whether the prompt at `index` has scrolled off the top of the viewport.
 * A mounted row is measured from the DOM; an unmounted (virtualized) row above
 * the window counts as passed, one below it does not.
 */
function promptElement(row: HTMLElement, id: string): HTMLElement {
	return row.querySelector<HTMLElement>(`[data-chat-message-id="${CSS.escape(id)}"]`) ?? row;
}

function promptScrolledPast(scroller: HTMLElement, index: number, id: string, virtualizer: Virtualizer<HTMLDivElement, Element>, virtualScrollMargin: number): boolean {
	const row = scroller.querySelector<HTMLElement>(`[data-index="${index}"]`);
	if (row) {
		return promptElement(row, id).getBoundingClientRect().bottom < scroller.getBoundingClientRect().top;
	}
	const start = virtualizer.measurementsCache[index]?.start;
	return start !== undefined && start - virtualScrollMargin < scroller.scrollTop;
}

/**
 * Drives the pinned last-prompt banner: visible while the latest human prompt is
 * scrolled above the viewport, and `jump` scrolls back to it.
 */
export function usePinnedPrompt({ groups, scroller, virtualizer, virtualScrollMargin, layoutKey }: {
	groups: readonly TimelineGroup[];
	scroller: RefObject<HTMLDivElement | null>;
	virtualizer: Virtualizer<HTMLDivElement, Element>;
	virtualScrollMargin: number;
	layoutKey: string;
}) {
	const prompt = latestPromptInfo(groups);
	const index = prompt?.index;
	const id = prompt?.id;
	const [past, setPast] = useState(false);
	const update = useCallback(() => {
		const node = scroller.current;
		setPast(Boolean(node) && index !== undefined && id !== undefined && promptScrolledPast(node!, index, id, virtualizer, virtualScrollMargin));
	}, [index, id, scroller, virtualizer, virtualScrollMargin]);

	useEffect(() => {
		const node = scroller.current;
		if (!node) return;
		update();
		node.addEventListener("scroll", update, { passive: true });
		return () => node.removeEventListener("scroll", update);
	}, [scroller, update, layoutKey]);

	const jump = useCallback(() => {
		const node = scroller.current;
		if (!node || index === undefined || id === undefined) return;
		const reduce = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
		const row = node.querySelector<HTMLElement>(`[data-index="${index}"]`);
		if (row) {
			const delta = promptElement(row, id).getBoundingClientRect().top - node.getBoundingClientRect().top;
			node.scrollTo({ top: node.scrollTop + delta - 8, behavior: reduce ? "auto" : "smooth" });
		} else {
			virtualizer.scrollToIndex(index, { align: "start" });
		}
	}, [index, id, scroller, virtualizer]);

	return { prompt, visible: past && prompt !== undefined, jump };
}
