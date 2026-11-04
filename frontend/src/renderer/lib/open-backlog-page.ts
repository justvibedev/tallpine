import type { NavigateFn } from "@tanstack/react-router";

/** Backlog is a page, not a session panel: the shortcut and the palette command both open it. */
export function openBacklogPage(navigate: NavigateFn): void {
	void navigate({ to: "/backlog" });
}
