/** Portal that keeps one cached terminal mounted while parked or shown. */
import { useCallback, useLayoutEffect } from "react";
import { createPortal } from "react-dom";
import { type AttachableTerminal } from "../../hooks/useTerminalSession";
import { CachedTerminalEntry } from "./terminal-pane-types";
import { AttachedTerminal } from "./AttachedTerminal";

export function CachedTerminalPortal({
	active,
	entry,
	onFatal,
	onTerminalReady,
}: {
	active: boolean;
	entry: CachedTerminalEntry;
	onFatal: (cacheKey: string, message: string) => void;
	onTerminalReady: (cacheKey: string, terminal: AttachableTerminal) => void;
}) {
	const handleFatal = useCallback(
		(message: string) => onFatal(entry.cacheKey, message),
		[entry.cacheKey, onFatal],
	);
	const handleTerminalReady = useCallback(
		(terminal: AttachableTerminal) => {
			onTerminalReady(entry.cacheKey, terminal);
		},
		[entry.cacheKey, onTerminalReady],
	);
	useLayoutEffect(() => {
		const terminal = entry.terminal;
		if (!active || entry.activationPhase !== "visible" || !terminal) return;
		// Fit and scroll after the host is visible. This retains the cache's
		// settled viewport behavior without putting a blank frame in front of it.
		void terminal.prepareForActivation();
		// Returning to a tab is a focus handoff: the terminal was blurred when it
		// was parked, so ask it to take the caret back (issue #6140). The guard
		// inside keeps this from stealing focus from dialogs or other controls.
		terminal.requestActivationFocus();
	}, [
		active,
		entry,
		entry.activationId,
		entry.activationPhase,
		entry.terminal,
	]);
	return createPortal(
		<AttachedTerminal
			{...entry.props}
			isVisible={active && entry.activationPhase === "visible"}
			onFatal={handleFatal}
			onTerminalReady={handleTerminalReady}
		/>,
		entry.container,
		entry.cacheKey,
	);
}

/**
 * Owns retained terminal renderers above the routed SessionView. Portal
 * containers never change; only the containers themselves move between the
 * active pane slot and an off-screen parking lot, so React, xterm, and the
 * terminal attachment all keep the same lifetime.
 */
