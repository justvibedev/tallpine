import { useCallback, useEffect, useLayoutEffect, useRef, type RefObject } from "react";
import { INITIAL_SNAP_MS } from "./timeline-constants";

/** Where each session's chat was last scrolled to. Absent means "at the end" (the default landing). */
const memory = new Map<string, number>();
const AT_END_PX = 64;

export const chatScrollMemory = {
	get: (sessionId: string) => memory.get(sessionId),
	set: (sessionId: string, top: number | null) => {
		if (top === null) memory.delete(sessionId);
		else memory.set(sessionId, top);
	},
	clear: () => memory.clear(),
};

/**
 * Leaving a chat (another session, or a full-screen view that unmounts it) and coming
 * back restores the reader's scroll position instead of jumping to the end. A chat the
 * reader left at the end stays unremembered, so it keeps following new output.
 */
export function useChatScrollMemory({ memoryKey, scroller, setPinnedNow, markWritten, layoutKey }: {
	/** The timeline's own scope (main chat or one side chat), not the shared session id. */
	memoryKey: string;
	scroller: RefObject<HTMLDivElement | null>;
	setPinnedNow: (next: boolean) => void;
	markWritten: (top: number) => void;
	layoutKey: unknown;
}) {
	const keyRef = useRef(memoryKey);
	const target = useRef<{ top: number; until: number; unpinned: boolean } | null>(null);

	// The scroller may not exist yet (an empty transcript renders nothing), so applying
	// and unpinning happen on whichever layout first finds it.
	const apply = useCallback(() => {
		const goal = target.current;
		const node = scroller.current;
		if (!goal || !node) return;
		if (performance.now() > goal.until) {
			target.current = null;
			return;
		}
		if (!goal.unpinned) {
			goal.unpinned = true;
			setPinnedNow(false);
		}
		node.scrollTop = goal.top;
		markWritten(node.scrollTop);
	}, [scroller, setPinnedNow, markWritten]);

	useLayoutEffect(() => {
		keyRef.current = memoryKey;
		const saved = chatScrollMemory.get(memoryKey);
		target.current = saved === undefined ? null : { top: saved, until: performance.now() + INITIAL_SNAP_MS, unpinned: false };
		apply();
	}, [memoryKey, apply]);

	// Long histories measure over the first frames, so keep re-applying the saved top briefly.
	useLayoutEffect(() => {
		apply();
	}, [layoutKey, apply]);

	useEffect(() => {
		const node = scroller.current;
		if (!node) return;
		const onScroll = () => {
			const goal = target.current;
			if (goal && performance.now() <= goal.until) return;
			const distance = node.scrollHeight - node.scrollTop - node.clientHeight;
			chatScrollMemory.set(keyRef.current, distance < AT_END_PX ? null : node.scrollTop);
		};
		node.addEventListener("scroll", onScroll, { passive: true });
		return () => node.removeEventListener("scroll", onScroll);
	}, [scroller, layoutKey]);
}
