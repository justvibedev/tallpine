import { motion, useReducedMotion } from "motion/react";
import type { CSSProperties, RefObject } from "react";
import { SHELL_PANEL_SPRING } from "@/lib/motion-spring";
import { cn } from "../lib/utils";

export function MacTitlebarDragStrip({ isFullScreen, sidebarDragStripRef, sidebarHasLayout, sidebarWidthPx, trafficLightDragActive }: {
	isFullScreen: boolean;
	sidebarDragStripRef: RefObject<HTMLDivElement | null>;
	sidebarHasLayout: boolean;
	sidebarWidthPx: number | null;
	trafficLightDragActive: boolean;
}) {
	const prefersReducedMotion = useReducedMotion();
	return (
						<div
							aria-hidden="true"
							data-slot="titlebar-drag-region"
							className={cn(
								// The strip itself only carries the drag region and its sizing; the
								// opaque fills are the two child layers below. The shell root goes
								// transparent while a live native browser page shows, so the fills
								// must stay opaque rather than reveal the darker native window.
								"fixed top-0 left-0 z-chrome transition-[height] duration-200 ease-out motion-reduce:transition-none",
								sidebarHasLayout ? "w-[calc(var(--tallpine-rail-w,0px)+var(--tallpine-sidebar-w,var(--size-sidebar-default)))]" : "w-titlebar-content-offset",
								isFullScreen ? "pointer-events-none h-0" : "h-traffic-light-clearance",
							)}
							ref={sidebarDragStripRef}
							style={{
								...(sidebarHasLayout && sidebarWidthPx ? { width: `calc(${sidebarWidthPx}px + var(--tallpine-rail-w, 0px))` } : null),
								...(trafficLightDragActive ? ({ WebkitAppRegion: "drag" } as CSSProperties) : null),
							}}
						>
							{/* Closed-sidebar band: the page's topbar color behind the traffic
							    lights and history arrows. Always present; the sidebar-colored
							    layer slides over it. */}
							<div className="pointer-events-none absolute inset-y-0 left-0 w-titlebar-content-offset bg-background" />
							{/* Sidebar-colored top of the sidebar. Rides the sidebar's own spring,
							    so the fake top and the real sidebar slide in and out together. */}
							<motion.div
								animate={{ x: sidebarHasLayout ? "0%" : "-100%" }}
								className="pointer-events-none absolute inset-y-0 left-(--tallpine-rail-w,0px) w-(--tallpine-sidebar-w,var(--size-sidebar-default)) bg-sidebar"
								initial={false}
								transition={prefersReducedMotion ? { duration: 0 } : SHELL_PANEL_SPRING}
							/>
							{/* The icon rail's own top: sidebar-colored whether or not the panel is open. */}
							<div className="pointer-events-none absolute inset-y-0 left-0 w-(--tallpine-rail-w,0px) bg-sidebar" />
						</div>
	);
}
