import { useCallback, useRef } from "react";

const widthVar = "--session-pinned-actions-w";

/**
 * Publishes the session's pinned top-bar controls' width as `--session-pinned-actions-w` so the shell
 * top bar (when the panel is closed) and the panel's header row (when open) reserve exactly that much
 * room, whatever number of switcher icons the page offers. Returns a ref callback for the pinned container.
 */
export function usePinnedActionsWidth(): (element: HTMLElement | null) => void {
	const observerRef = useRef<ResizeObserver | null>(null);
	return useCallback((element: HTMLElement | null) => {
		observerRef.current?.disconnect();
		observerRef.current = null;
		const root = document.documentElement;
		if (!element) {
			root.style.removeProperty(widthVar);
			return;
		}
		const publish = () => root.style.setProperty(widthVar, `${Math.ceil(element.getBoundingClientRect().width)}px`);
		publish();
		if (typeof ResizeObserver === "undefined") return;
		observerRef.current = new ResizeObserver(publish);
		observerRef.current.observe(element);
	}, []);
}
