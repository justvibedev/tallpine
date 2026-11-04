import { useCallback, useRef, type RefObject } from "react";
import { useResizable } from "../../hooks/useResizable";
import { SIDEBAR_DEFAULT_WIDTH, SIDEBAR_MAX_WIDTH, SIDEBAR_MIN_WIDTH } from "./sizing";

export function useSidebarResize({ resizeAuxiliaryTargetRef, setOpen }: { resizeAuxiliaryTargetRef?: RefObject<HTMLElement | null>; setOpen: (open: boolean) => void }) {
	// tallpine's sidebar resize: drag the right edge (200-420px,
	// persisted), double-click to reset to 240px. Drives --tallpine-sidebar-w on :root,
	// only to the two layout consumers and fixed titlebar strip, rather than
	// :root. Dragging clamps
	// at SIDEBAR_MIN_WIDTH — collapsing stays on the explicit toggle (⌘B /
	// titlebar button), never on a drag.
	const resizeScopeRef = useRef<HTMLDivElement>(null);
	const getResizeTargets = useCallback(() => {
		const scope = resizeScopeRef.current;
		return [
			scope?.querySelector<HTMLElement>('[data-slot="sidebar-gap"]') ?? null,
			scope?.querySelector<HTMLElement>('[data-slot="sidebar-container"]') ?? null,
			resizeAuxiliaryTargetRef?.current ?? null,
		];
	}, [resizeAuxiliaryTargetRef]);
	const {
		onPointerDown: onResizePointerDown,
		onCollapsedPointerDown: onCollapsedResizePointerDown,
		onDoubleClick: onResizeDoubleClick,
	} = useResizable({
		cssVar: "--tallpine-sidebar-w",
		getCssTargets: getResizeTargets,
		// Persisted user data: keep the original key so saved widths survive the rename.
		storageKey: "ao-sidebar-w",
		defaultWidth: SIDEBAR_DEFAULT_WIDTH,
		min: SIDEBAR_MIN_WIDTH,
		max: SIDEBAR_MAX_WIDTH,
		edge: "right",
		reclampOnWindowResize: true,
		onExpand: () => setOpen(true),
	});

	return { resizeScopeRef, onResizePointerDown, onCollapsedResizePointerDown, onResizeDoubleClick };
}
