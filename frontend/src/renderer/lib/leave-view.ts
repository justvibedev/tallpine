import { useNavigate } from "@tanstack/react-router";
import { useCallback } from "react";
import { useUiStore } from "../stores/ui-store";

/**
 * The one rule for leaving a full-screen view (Settings, Backlog, Inbox,
 * Artifacts, Automations, Terminals): Esc, the top-right close control, and
 * re-clicking the active rail tab all land on the exact previous Sessions page
 * (normally the session chat). Settings is a state layer over the routed page,
 * so closing it already restores that page.
 */
export function useLeaveView() {
	const navigate = useNavigate();
	return useCallback(() => {
		const ui = useUiStore.getState();
		if (ui.settingsModal?.scope === "global") {
			ui.closeSettings();
			return;
		}
		void navigate({ to: ui.lastSessionsPath });
	}, [navigate]);
}

/** Targets that handle Escape themselves (fields, menus, dialogs). */
export function escapeBelongsToTarget(event: KeyboardEvent): boolean {
	if (event.defaultPrevented || event.isComposing) return true;
	const popup = 'input, textarea, select, [contenteditable=""], [contenteditable="true"], [role="menu"], [role="listbox"], [role="dialog"], [data-radix-popper-content-wrapper]';
	const target = event.target instanceof Element ? event.target : null;
	const focused = document.activeElement instanceof Element ? document.activeElement : null;
	return [target, focused].some((element) => element?.closest(popup));
}
