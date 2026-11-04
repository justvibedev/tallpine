import { useCallback, useEffect, useMemo, useRef } from "react";

// Browsers dispatch a click after pointerup even when dnd-kit has just completed
// a drag. Suppress only that same-turn synthetic click; if no click follows,
// clear the guard before the next user interaction.
export function usePostDragClickGuard() {
	const guardedIdRef = useRef<string | null>(null);
	const clearTimerRef = useRef<number | null>(null);

	const markDragEnded = useCallback((id: string) => {
		guardedIdRef.current = id;
		if (clearTimerRef.current !== null) window.clearTimeout(clearTimerRef.current);
		clearTimerRef.current = window.setTimeout(() => {
			guardedIdRef.current = null;
			clearTimerRef.current = null;
		}, 0);
	}, []);

	const consumeClick = useCallback((id: string) => {
		if (guardedIdRef.current !== id) return false;
		guardedIdRef.current = null;
		if (clearTimerRef.current !== null) {
			window.clearTimeout(clearTimerRef.current);
			clearTimerRef.current = null;
		}
		return true;
	}, []);

	useEffect(() => () => {
		if (clearTimerRef.current !== null) window.clearTimeout(clearTimerRef.current);
	}, []);

	return useMemo(() => ({ consumeClick, markDragEnded }), [consumeClick, markDragEnded]);
}
