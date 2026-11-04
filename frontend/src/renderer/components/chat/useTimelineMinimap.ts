import { useEffect, useRef, useState } from "react";

/**
 * Minimap scrollbar state and its reveal timing.
 */
export function useTimelineMinimap() {
	const [scrollbar, setScrollbar] = useState({
		visible: false,
		top: 0,
		height: 40,
		percent: 0,
		markers: [] as Array<{
			top: number;
			scrollTop: number;
			visible: boolean;
		}>,
	});

	// Show turn ticks whenever the inspector is closed and there are human
	// prompts — not only when the transcript overflows. Closing the rail
	// widens chat; shorter histories (common on non-Codex harnesses) often
	// stop overflowing and used to lose the minimap exactly then.
	const minimapEnabled = scrollbar.markers.length > 0;

	// Replies rendered with the chat's first commit were already on screen
	// before the switch; streamed text in them must not fade in again.
	const chatOpening = useRef(true);

	useEffect(() => {
		chatOpening.current = false;
	}, []);

	// The minimap appears once the opened chat is measured. That first reveal is
	// part of opening the chat, so it is instant; only later changes (the inspector
	// covering it) fade.
	const [minimapRevealed, setMinimapRevealed] = useState(false);

	useEffect(() => {
		if (!minimapEnabled || minimapRevealed) return;
		const frame = window.requestAnimationFrame(() => setMinimapRevealed(true));
		return () => window.cancelAnimationFrame(frame);
	}, [minimapEnabled, minimapRevealed]);

	return {
		scrollbar,
		setScrollbar,
		minimapEnabled,
		chatOpening,
		minimapRevealed,
	};
}
