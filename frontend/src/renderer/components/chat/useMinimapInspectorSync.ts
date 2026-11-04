import { useEffect } from "react";
import { inspectorIsOpen, useUiStore } from "../../stores/ui-store";
import type { Dispatch, SetStateAction, RefObject } from "react";

/**
 * Hides the minimap track while the inspector is open.
 */
export function useMinimapInspectorSync({
	inspectorOpenRef,
	scrollTrack,
	minimapEnabled,
	drag,
	hoveredMarkerRef,
	setHoveredMarker,
	uiSessionId,
}: {
	inspectorOpenRef: RefObject<boolean>;
	scrollTrack: RefObject<HTMLDivElement | null>;
	minimapEnabled: boolean;
	drag: RefObject<{ pointerId: number; startY: number; startScrollTop: number; } | null>;
	hoveredMarkerRef: RefObject<number | null>;
	setHoveredMarker: Dispatch<SetStateAction<number | null>>;
	uiSessionId: string;
}) {
	useEffect(() => {
		const setInspectorOpen = (isOpen: boolean) => {
			inspectorOpenRef.current = isOpen;
			const track = scrollTrack.current;
			if (track) {
				track.dataset.inspectorOpen = String(isOpen);
				track.setAttribute("aria-hidden", String(isOpen));
				track.tabIndex = minimapEnabled && !isOpen ? 0 : -1;
				track.classList.toggle("cursor-pointer", minimapEnabled && !isOpen);
				track.classList.toggle("pointer-events-none", !minimapEnabled || isOpen);
				track.classList.toggle("opacity-100", minimapEnabled && !isOpen);
				track.classList.toggle("opacity-0", !minimapEnabled || isOpen);
				if (isOpen && document.activeElement === track) track.blur();
			}
			if (isOpen) {
				drag.current = null;
				if (hoveredMarkerRef.current !== null) setHoveredMarker(null);
			}
		};
		setInspectorOpen(inspectorIsOpen(useUiStore.getState().inspectorSessions, uiSessionId));
		return useUiStore.subscribe((state, previous) => {
			const currentOpen = inspectorIsOpen(state.inspectorSessions, uiSessionId);
			const previousOpen = inspectorIsOpen(previous.inspectorSessions, uiSessionId);
			if (currentOpen !== previousOpen) setInspectorOpen(currentOpen);
		});
	}, [minimapEnabled, uiSessionId]);
}
