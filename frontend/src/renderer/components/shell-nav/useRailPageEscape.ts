import { useEffect } from "react";
import { escapeBelongsToTarget, useLeaveView } from "../../lib/leave-view";
import { useUiStore } from "../../stores/ui-store";

/** Esc leaves a rail page (Backlog, Inbox, ...) for the previous Sessions page. Settings handles its own Esc. */
export function useRailPageEscape(onRailPage: boolean) {
	const leave = useLeaveView();
	useEffect(() => {
		if (!onRailPage) return;
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.key !== "Escape" || useUiStore.getState().settingsModal) return;
			if (escapeBelongsToTarget(event)) return;
			leave();
		};
		document.addEventListener("keydown", onKeyDown);
		return () => document.removeEventListener("keydown", onKeyDown);
	}, [onRailPage, leave]);
}
