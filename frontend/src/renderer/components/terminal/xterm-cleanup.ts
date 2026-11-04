import { tallpineBridge } from "../../lib/bridge";
import type { XtermEnv, XtermState } from "./xterm-types";
import { installSetup } from "./xterm-setup";
import { installScrollbar } from "./xterm-scrollbar";
import { installInput } from "./xterm-input";
import { installFit } from "./xterm-fit";
import { installActivation } from "./xterm-activation";
import { DevXtermHost } from "./xterm-types";

export function installCleanup(env: XtermEnv, st: XtermState, setupOut: NonNullable<ReturnType<typeof installSetup>>, scrollbarOut: ReturnType<typeof installScrollbar>, inputOut: ReturnType<typeof installInput>, fitOut: ReturnType<typeof installFit>, activationOut: ReturnType<typeof installActivation>) {
	const {
		announcedCursorSchemeRef,
		colorSchemeReporterRef,
		contextMenuActionsRef,
		fitRef,
		notifyCursorSchemeRef,
		searchAddonRef,
		termRef,
		webglLeaseRef,
	} = env;
	const {
		disposeFontSizeShortcut,
		handleFocusIn,
		handleFocusOut,
		host,
		searchAddon,
		shell,
		term,
	} = setupOut;
	const {
		scrollPositionChange,
		scrollbarPointerDown,
		scrollbarPointerMove,
		scrollbarPointerUp,
		scrollbarResize,
		scrollbarTrack,
		visibleContentRender,
		webglLease,
	} = scrollbarOut;
	const {
		clearSuppressNativePaste,
		copyInput,
		copyShortcut,
		disarmPointerSelection,
		openContextMenu,
		pointerDown,
		pointerUp,
		queryColorScheme,
		queryColorSchemeCapability,
		reportColorScheme,
		resetColorSchemeUpdates,
		setColorSchemeUpdates,
		userInputListeners,
	} = inputOut;
	const {
		cancelResizeCover,
		cursorPositionForwarder,
		dragStateObserver,
		fitSettledListeners,
		keyInput,
		observer,
		oscColorForwarder,
		paintedResizeFrame,
		protocolInput,
		raf,
		resizeCover,
		scheduleLiveFit,
		settleTimers,
		stabilizer,
		synchronizedCapability,
		synchronizedFrames,
	} = fitOut;
	const {
		compositionInput,
		dragOverInput,
		dropInput,
		pasteInput,
	} = activationOut;
	return () => {
		st.disposed = true;
		webglLease.release();
		webglLeaseRef.current = null;
		if (st.reportedFocused) tallpineBridge.terminal.setFocused(false);
		disposeFontSizeShortcut();
		host.removeEventListener("focusin", handleFocusIn);
		host.removeEventListener("focusout", handleFocusOut);
		delete (host as DevXtermHost).__tallpineXtermForTest;
		termRef.current = null;
		if (searchAddonRef.current === searchAddon) searchAddonRef.current = null;
		fitRef.current = null;
		cancelAnimationFrame(raf);
		if (st.liveFitFrame !== null) cancelAnimationFrame(st.liveFitFrame);
		for (const timer of settleTimers) window.clearTimeout(timer);
		if (st.fitQuietTimer !== null) clearTimeout(st.fitQuietTimer);
		if (st.fitCapTimer !== null) clearTimeout(st.fitCapTimer);
		fitSettledListeners.clear();
		observer.disconnect();
		dragStateObserver.disconnect();
		synchronizedFrames.dispose();
		synchronizedCapability.dispose();
		paintedResizeFrame.dispose();
		host.removeEventListener("wheel", cancelResizeCover);
		host.removeEventListener("keydown", cancelResizeCover);
		host.removeEventListener("pointerdown", cancelResizeCover);
		resizeCover.remove();
		stabilizer.dispose();
		scrollPositionChange?.dispose();
		scrollbarResize?.dispose();
		if (st.scrollbarFrame !== null) cancelAnimationFrame(st.scrollbarFrame);
		if (st.scrollbarHideTimer !== null) window.clearTimeout(st.scrollbarHideTimer);
		scrollbarTrack?.removeEventListener("pointerdown", scrollbarPointerDown);
		scrollbarTrack?.removeEventListener("pointermove", scrollbarPointerMove);
		scrollbarTrack?.removeEventListener("pointerup", scrollbarPointerUp);
		scrollbarTrack?.removeEventListener("pointercancel", scrollbarPointerUp);
		window.removeEventListener("resize", scheduleLiveFit);
		shell.removeEventListener("copy", copyInput);
		window.removeEventListener("keydown", copyShortcut, true);
		host.removeEventListener("pointerdown", pointerDown);
		document.removeEventListener("pointerup", pointerUp);
		document.removeEventListener("pointercancel", disarmPointerSelection);
		window.removeEventListener("blur", disarmPointerSelection);
		shell.removeEventListener("contextmenu", openContextMenu);
		shell.removeEventListener("paste", pasteInput, true);
		shell.removeEventListener("compositionend", compositionInput, true);
		shell.removeEventListener("dragover", dragOverInput);
		shell.removeEventListener("drop", dropInput);
		contextMenuActionsRef.current = null;
		visibleContentRender.dispose();
		st.cancelActivationPreparation?.();
		clearSuppressNativePaste();
		if (colorSchemeReporterRef.current === reportColorScheme) colorSchemeReporterRef.current = null;
		setColorSchemeUpdates.dispose();
		resetColorSchemeUpdates.dispose();
		queryColorSchemeCapability.dispose();
		queryColorScheme.dispose();
		for (const timer of st.schemeRetryTimers) window.clearTimeout(timer);
		st.schemeRetryTimers = [];
		oscColorForwarder.dispose();
		cursorPositionForwarder.dispose();
		protocolInput.dispose();
		keyInput.dispose();
		notifyCursorSchemeRef.current = () => {};
		announcedCursorSchemeRef.current = null;
		userInputListeners.clear();
		// xterm's Viewport queues an untracked zero-delay scroll-area sync during
		// open(). React StrictMode immediately runs this cleanup once after mount;
		// disposing the renderer before that queued sync runs makes xterm read the
		// now-missing renderer dimensions. Queue disposal behind xterm's task so
		// the terminal remains internally valid until its own initialization work
		// has drained. All Tallpine listeners and attachment state are already detached.
		window.setTimeout(() => {
			try {
				term.dispose();
			} catch {
				// Some renderer addons can throw during dispose in certain GPU
				// environments; the terminal is being torn down regardless.
			}
		}, 0);
	};
}
