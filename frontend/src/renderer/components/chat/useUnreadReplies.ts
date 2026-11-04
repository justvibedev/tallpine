import { useEffect, useLayoutEffect, useMemo, useRef, useState, type RefObject } from "react";
import { chatLastSeen, ownerReplies } from "./chat-last-seen";
import { chatScrollMemory } from "./useChatScrollMemory";
import { INITIAL_SNAP_MS } from "./timeline-constants";
import type { TimelineGroup } from "./timeline-groups";

type Virtualizer = { scrollToIndex: (index: number, options?: { align?: "start" | "center" | "end" | "auto" }) => void };

/**
 * Returning to a chat lands on the first reply the reader has not seen, and counts
 * the unseen replies for the "N new replies" pill. Reaching the end in a visible,
 * focused window marks them seen; coming back to the window counts as returning.
 *
 * Call before `useChatScrollMemory`: landing on an unread reply drops the remembered
 * position first, so the two do not fight over the scroll top.
 */
export function useUnreadReplies({ memoryKey, groups, pinned, setPinnedNow, markWritten, scroller, scrollContent, virtualized, virtualizer, layoutKey }: {
	memoryKey: string;
	groups: readonly TimelineGroup[];
	pinned: boolean;
	setPinnedNow: (next: boolean) => void;
	markWritten: (top: number) => void;
	scroller: RefObject<HTMLDivElement | null>;
	scrollContent: RefObject<HTMLDivElement | null>;
	virtualized: boolean;
	virtualizer: Virtualizer;
	layoutKey: unknown;
}) {
	const replies = useMemo(() => ownerReplies(groups), [groups]);
	const latest = replies.at(-1)?.sequence;
	const [seen, setSeen] = useState<{ key: string; sequence: number | undefined }>({ key: memoryKey, sequence: undefined });
	const landedKey = useRef<string | undefined>(undefined);
	const landing = useRef<{ index: number; until: number } | null>(null);

	const scrollToTarget = () => {
		const goal = landing.current;
		const node = scroller.current;
		if (!goal || !node) return;
		if (performance.now() > goal.until) {
			landing.current = null;
			return;
		}
		if (virtualized) virtualizer.scrollToIndex(goal.index, { align: "start" });
		else scrollContent.current?.querySelector<HTMLElement>(`[data-index="${goal.index}"]`)?.scrollIntoView({ block: "start" });
		markWritten(node.scrollTop);
	};

	const land = (sequenceSeen: number) => {
		const first = replies.find((reply) => reply.sequence > sequenceSeen);
		if (!first) return;
		chatScrollMemory.set(memoryKey, null);
		landing.current = { index: first.index, until: performance.now() + INITIAL_SNAP_MS };
		setPinnedNow(false);
		scrollToTarget();
	};

	useLayoutEffect(() => {
		if (landedKey.current === memoryKey || latest === undefined) return;
		landedKey.current = memoryKey;
		const stored = chatLastSeen.get(memoryKey);
		if (stored === undefined) {
			// First visit: everything already here counts as seen.
			chatLastSeen.set(memoryKey, latest);
			setSeen({ key: memoryKey, sequence: latest });
			return;
		}
		setSeen({ key: memoryKey, sequence: stored });
		land(stored);
	});

	// Long histories measure over the first frames, so keep re-aiming briefly.
	useLayoutEffect(() => scrollToTarget(), [layoutKey]);

	const lastSeen = seen.key === memoryKey ? seen.sequence : undefined;
	// A chat left open at the end in a background window has not been read: only a
	// visible, focused window counts, and coming back to it is "returning".
	const attending = useAttending();
	const wasAttending = useRef(attending);
	useLayoutEffect(() => {
		const returned = attending && !wasAttending.current;
		wasAttending.current = attending;
		if (returned && lastSeen !== undefined) land(lastSeen);
	});
	useEffect(() => {
		const stillLanding = landing.current !== null && performance.now() <= landing.current.until;
		if (!attending || !pinned || stillLanding || latest === undefined || lastSeen === undefined || latest <= lastSeen) return;
		chatLastSeen.set(memoryKey, latest);
		setSeen({ key: memoryKey, sequence: latest });
	}, [attending, pinned, latest, lastSeen, memoryKey]);

	const unreadCount = lastSeen === undefined ? 0 : replies.filter((reply) => reply.sequence > lastSeen).length;
	return { unreadCount };
}

const attendingNow = () => document.visibilityState === "visible" && document.hasFocus();

function useAttending(): boolean {
	const [attending, setAttending] = useState(attendingNow);
	useEffect(() => {
		const update = () => setAttending(attendingNow());
		document.addEventListener("visibilitychange", update);
		window.addEventListener("focus", update);
		window.addEventListener("blur", update);
		return () => {
			document.removeEventListener("visibilitychange", update);
			window.removeEventListener("focus", update);
			window.removeEventListener("blur", update);
		};
	}, []);
	return attending;
}
