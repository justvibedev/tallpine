import { useEffect, useLayoutEffect, useRef } from "react";
import type { XtermTerminalProps } from "./xterm-types";
import type { useXtermState } from "./useXtermState";
import { AUTOFOCUS_RETRY_FRAMES } from "./xterm-paste";
import { canAutoFocusTerminal } from "./xterm-shortcuts";


export function useXtermFocusEffects(props: XtermTerminalProps, state: ReturnType<typeof useXtermState>) {
	const {
		callbacksRef,
		cancelPendingFocusRestore,
		copiedToastTimerRef,
		focusTerminal,
		gridMeasuredRef,
		hostRef,
		searchAddonRef,
		setContextMenuOpen,
		setCopiedToast,
		setSearchOpen,
		termRef,
		webglLeaseRef,
	} = state;
	useEffect(() => {
		if (!props.focusRequested || props.isVisible === false) return undefined;
		let initialFocusTimer: number | null = null;
		let retryFrame: number | null = null;
		let retriesRemaining = AUTOFOCUS_RETRY_FRAMES;
		let cancelled = false;
		const focusIfAllowed = () => {
			if (cancelled) return;
			const host = hostRef.current;
			if (!host || !canAutoFocusTerminal(host)) {
				if (retriesRemaining === 0) return;
				retriesRemaining -= 1;
				retryFrame = requestAnimationFrame(() => {
					retryFrame = null;
					focusIfAllowed();
				});
				return;
			}
			focusTerminal();
		};

		// A terminal tab/session click has already made the retained xterm visible.
		// Calling `focus()` in this same discrete React effect can synchronously
		// trigger browser focus/layout work while the click is still being handled.
		// Defer only an already-permitted focus to a later task. A blocked focus
		// keeps the existing rAF retry path so a closing dialog is handled promptly.
		const initialHost = hostRef.current;
		if (initialHost && canAutoFocusTerminal(initialHost)) {
			initialFocusTimer = window.setTimeout(() => {
				initialFocusTimer = null;
				focusIfAllowed();
			}, 0);
		} else {
			focusIfAllowed();
		}

		return () => {
			cancelled = true;
			if (initialFocusTimer !== null) window.clearTimeout(initialFocusTimer);
			if (retryFrame !== null) cancelAnimationFrame(retryFrame);
		};
	}, [focusTerminal, props.focusRequested, props.isVisible]);

	useLayoutEffect(() => {
		if (props.isVisible === false) {
			setSearchOpen(false);
			searchAddonRef.current?.clearDecorations();
			setContextMenuOpen(false);
			setCopiedToast(false);
			if (copiedToastTimerRef.current !== undefined) {
				window.clearTimeout(copiedToastTimerRef.current);
				copiedToastTimerRef.current = undefined;
			}
			cancelPendingFocusRestore();
		}
	}, [props.isVisible, setContextMenuOpen, cancelPendingFocusRestore]);

	useEffect(() => cancelPendingFocusRestore, [cancelPendingFocusRestore]);

	const wasVisibleRef = useRef(props.isVisible !== false);
	useEffect(() => {
		const visible = props.isVisible !== false;
		const becameVisible = visible && !wasVisibleRef.current;
		wasVisibleRef.current = visible;
		if (!becameVisible) return;
		webglLeaseRef.current?.activate();
		// Activation preparation already fitted the terminal after the slot became
		// stable. Publish that grid without fitting a second time after reveal.
		// A terminal that has never measured its slot has no grid to publish; its
		// first measurement publishes it.
		const term = termRef.current;
		if (term && gridMeasuredRef.current) callbacksRef.current.onVisibleSize?.(term.cols, term.rows);
	}, [props.isVisible]);
}
