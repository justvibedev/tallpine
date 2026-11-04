import { useCallback, useEffect, useRef, useState, type WheelEvent as ReactWheelEvent, RefObject } from "react";
import { clampTerminalFontSize, initialTerminalFontSize, terminalFontSizeStorageKey } from "../../lib/terminal-font-size";
import { WHEEL_ZOOM_RESET_MS, WHEEL_ZOOM_THRESHOLD } from "./chat-workspace-constants";

/**
 * Terminal font zoom and fullscreen for the chat surface.
 */
export function useTerminalZoomFullscreen({ surfaceRef }: {
	surfaceRef: RefObject<HTMLElement | null>;
}) {
	const lastWheelZoomAtRef = useRef(0);

	const wheelZoomRemainderRef = useRef(0);

	const [terminalFontSize, setTerminalFontSize] = useState(initialTerminalFontSize);

	const [isFullscreen, setIsFullscreen] = useState(false);

	useEffect(() => {
		const handleFullscreenChange = () => {
			setIsFullscreen(document.fullscreenElement === surfaceRef.current);
		};
		document.addEventListener("fullscreenchange", handleFullscreenChange);
		return () => document.removeEventListener("fullscreenchange", handleFullscreenChange);
	}, []);

	const updateTerminalFontSize = useCallback((delta: number) => {
		setTerminalFontSize((current) => {
			const next = clampTerminalFontSize(current + delta);
			window.localStorage?.setItem(terminalFontSizeStorageKey, String(next));
			return next;
		});
	}, []);

	const handleWheelZoom = useCallback(
		(event: ReactWheelEvent<HTMLDivElement>) => {
			if (!event.ctrlKey && !event.metaKey) return;
			event.preventDefault();
			event.stopPropagation();

			if (event.timeStamp - lastWheelZoomAtRef.current > WHEEL_ZOOM_RESET_MS) {
				wheelZoomRemainderRef.current = 0;
			}
			lastWheelZoomAtRef.current = event.timeStamp;
			wheelZoomRemainderRef.current += event.deltaY;

			const steps = Math.floor(Math.abs(wheelZoomRemainderRef.current) / WHEEL_ZOOM_THRESHOLD);
			if (steps === 0) return;

			const direction = wheelZoomRemainderRef.current > 0 ? -1 : 1;
			updateTerminalFontSize(direction * steps);
			wheelZoomRemainderRef.current -=
				Math.sign(wheelZoomRemainderRef.current) * steps * WHEEL_ZOOM_THRESHOLD;
		},
		[updateTerminalFontSize],
	);

	const toggleFullscreen = useCallback(async () => {
		const surface = surfaceRef.current;
		if (!surface) return;
		try {
			if (document.fullscreenElement === surface) {
				await document.exitFullscreen();
				return;
			}
			await surface.requestFullscreen();
		} catch (error) {
			console.warn("Unable to toggle chat reviewer fullscreen", error);
		}
	}, []);

	return {
		terminalFontSize,
		isFullscreen,
		updateTerminalFontSize,
		handleWheelZoom,
		toggleFullscreen,
	};
}
