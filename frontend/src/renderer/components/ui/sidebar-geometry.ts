type Side = "left" | "right";

/**
 * Target width of the layout gap placeholder. Animating the real width lets the
 * flex sibling <main> follow in real time. The shell rail (`--tallpine-rail-w`,
 * 0px without one) always keeps its width; the panel adds its own unless it is
 * closed or floating as an overlay.
 */
export function sidebarGapWidth({ side, offcanvasCollapsed, iconCollapsed, inset, overlay }: { side: Side; offcanvasCollapsed: boolean; iconCollapsed: boolean; inset: boolean; overlay: boolean }): string | number {
	const panel = iconCollapsed
		? inset
			? "calc(var(--sidebar-width-icon) + 1rem)"
			: "var(--sidebar-width-icon)"
		: "var(--tallpine-sidebar-w, var(--sidebar-width))";
	if (side === "left") {
		return offcanvasCollapsed || overlay ? "var(--tallpine-rail-w, 0px)" : `calc(var(--tallpine-rail-w, 0px) + ${panel})`;
	}
	return offcanvasCollapsed ? 0 : panel;
}
