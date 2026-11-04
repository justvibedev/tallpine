import { useEffect, useRef, useState } from "react";
import { useWindowZoomFactor } from "../hooks/useWindowZoomFactor";
import { useWindowFullScreen } from "../hooks/useWindowFullScreen";
import { isMac } from "./platform";

export function useTrafficLightDrag() {
	// Single subscription for sidebar clearance + drag strip (macOS no-ops inside the hook).
	const isFullScreen = useWindowFullScreen();
	// Writes --tallpine-window-zoom and the macOS topbar size tokens the titlebar clearances derive from.
	useWindowZoomFactor();
	// Drag is on immediately for a normal windowed launch. After leaving fullscreen,
	// wait for the pad/height transition so the growing strip cannot steal clicks.
	const [trafficLightDragActive, setTrafficLightDragActive] = useState(isMac);
	const leftFullScreenRef = useRef(false);
	useEffect(() => {
		if (!isMac) return;
		if (isFullScreen) {
			leftFullScreenRef.current = true;
			setTrafficLightDragActive(false);
			return;
		}
		if (!leftFullScreenRef.current) {
			setTrafficLightDragActive(true);
			return;
		}
		const reducedMotion =
			typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
		if (reducedMotion) {
			setTrafficLightDragActive(true);
			return;
		}
		const timer = window.setTimeout(() => setTrafficLightDragActive(true), 200);
		return () => window.clearTimeout(timer);
	}, [isFullScreen]);
	return { isFullScreen, trafficLightDragActive };
}
