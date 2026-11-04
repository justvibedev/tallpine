import { useRouterState } from "@tanstack/react-router";
import { useEffect } from "react";
import { useUiStore } from "../../stores/ui-store";

/** Below this window width the Sessions panel floats over the page instead of taking layout. */
export const NARROW_SHELL_MAX_WIDTH = 900;

/** Keeps `ui-store.isNarrowShell` in step with the window width and closes the floating panel on navigation. */
export function useNarrowShell() {
	const setNarrowShell = useUiStore((state) => state.setNarrowShell);
	const closeNarrowPanel = useUiStore((state) => state.closeNarrowPanel);
	const pathname = useRouterState({ select: (state) => state.location.pathname });
	useEffect(() => closeNarrowPanel(), [pathname, closeNarrowPanel]);
	useEffect(() => {
		if (typeof window.matchMedia !== "function") return;
		const query = window.matchMedia(`(max-width: ${NARROW_SHELL_MAX_WIDTH - 1}px)`);
		const sync = () => setNarrowShell(query.matches);
		sync();
		query.addEventListener("change", sync);
		return () => query.removeEventListener("change", sync);
	}, [setNarrowShell]);
}
