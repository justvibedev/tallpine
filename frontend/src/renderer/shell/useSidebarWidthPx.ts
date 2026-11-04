import { useEffect, useState } from "react";
import { isMac } from "./platform";

export function useSidebarWidthPx({ sidebarHasLayout }: {
	sidebarHasLayout: boolean;
}) {
	// The drag strip above the sidebar must be exactly as wide as the sidebar.
	// `--tallpine-sidebar-w` only reaches the strip if it already exists when the
	// sidebar first applies its saved width, so measure the sidebar instead.
	const [sidebarWidthPx, setSidebarWidthPx] = useState<number | null>(null);
	useEffect(() => {
		if (!isMac || !sidebarHasLayout) return;
		let observer: ResizeObserver | undefined;
		const frame = requestAnimationFrame(() => {
			const el = document.querySelector<HTMLElement>('[data-slot="sidebar-container"]');
			if (!el) return;
			observer = new ResizeObserver(([entry]) => setSidebarWidthPx(entry.target.getBoundingClientRect().width));
			observer.observe(el);
		});
		return () => {
			cancelAnimationFrame(frame);
			observer?.disconnect();
		};
	}, [sidebarHasLayout]);
	return { sidebarWidthPx };
}
