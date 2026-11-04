import { useCallback, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { useTranslation } from "react-i18next";
import { SearchAddon } from "@xterm/addon-search";
import { isMacPlatform } from "../../lib/platform";
import { type WebglLease } from "../../lib/terminal-webgl-pool";
import { useUiStore, type Theme, type ThemeStyle } from "../../stores/ui-store";
import { useLinkPreview } from "../../hooks/useLinkPreview";
import type { XtermTerminalProps } from "./xterm-types";
import { TerminalContextMenuActions, TerminalContextMenuState, TerminalContextMenuAction } from "./xterm-types";
import { canAutoFocusTerminal } from "./xterm-shortcuts";
import { COPY_TOAST_MS, LINK_PREVIEW_OPEN_MS, LINK_PREVIEW_CLOSE_MS } from "./xterm-paste";


export function useXtermState(props: XtermTerminalProps) {
	const { t } = useTranslation();
	const themeStyle = useUiStore((state) => state.themeStyle);
	const windowTranslucency = useUiStore((state) => state.windowTranslucency);
	const macPlatform = isMacPlatform();
	const translucent = macPlatform && windowTranslucency;
	const shellRef = useRef<HTMLDivElement | null>(null);
	const hostRef = useRef<HTMLDivElement | null>(null);
	const scrollbarTrackRef = useRef<HTMLDivElement | null>(null);
	const scrollbarThumbRef = useRef<HTMLDivElement | null>(null);
	const termRef = useRef<Terminal | null>(null);
	const webglLeaseRef = useRef<WebglLease | null>(null);
	// Whether the live terminal's grid has been measured from its laid-out slot.
	const gridMeasuredRef = useRef(false);
	const notifyCursorSchemeRef = useRef<(scheme: Theme, force?: boolean, retry?: boolean) => void>(() => {});
	const announcedCursorSchemeRef = useRef<Theme | null>(null);
	const searchAddonRef = useRef<SearchAddon | null>(null);
	const fitRef = useRef<(() => void) | null>(null);
	const colorSchemeReporterRef = useRef<
		((theme: Theme, themeStyle: ThemeStyle, force?: boolean) => void) | null
	>(null);
	const contextMenuActionsRef = useRef<TerminalContextMenuActions | null>(null);
	const [contextMenu, setContextMenu] = useState<TerminalContextMenuState>({
		canCopy: false,
		open: false,
		x: 0,
		y: 0,
		link: null,
	});
	const [copiedToast, setCopiedToast] = useState(false);
	const [searchOpen, setSearchOpen] = useState(false);
	const copiedToastTimerRef = useRef<number | undefined>(undefined);
	const showCopiedToastRef = useRef<() => void>(() => undefined);
	// The web link currently under the cursor, tracked via the link providers'
	// hover/leave callbacks so the right-click menu can offer "Open in system
	// browser" for it.
	const hoveredLinkRef = useRef<string | null>(null);
	// Link preview card anchored at the hover position. The trigger is a
	// zero-size fixed element, mirroring the context menu below; open/close
	// delays are manual because the card is controlled (no real anchor hover).
	const [linkPreview, setLinkPreview] = useState<{ url: string; x: number; y: number } | null>(null);
	const linkPreviewTimerRef = useRef<number | undefined>(undefined);
	const linkPreviewControlsRef = useRef<{
		show: (url: string, x: number, y: number) => void;
		hide: (immediately?: boolean) => void;
		cancelHide: () => void;
	}>({ show: () => undefined, hide: () => undefined, cancelHide: () => undefined });
	const linkPreviewQuery = useLinkPreview(linkPreview?.url ?? "", linkPreview !== null);
	// Latest callbacks in a ref so the mount effect stays dependency-free — we
	// never tear down and recreate the terminal because a handler identity
	// changed between renders.
	const callbacksRef = useRef(props);

	const setContextMenuOpen = useCallback((open: boolean) => {
		setContextMenu((current) => ({ ...current, open }));
	}, []);

	const runContextMenuAction = useCallback(
		(action: TerminalContextMenuAction) => {
			contextMenuActionsRef.current?.[action]();
			setContextMenuOpen(false);
		},
		[setContextMenuOpen],
	);
	const focusTerminal = useCallback(() => {
		try {
			termRef.current?.focus();
		} catch {
			// A retained terminal can be parked between closing search and this frame.
		}
	}, []);
	const restoreFocusFrameIdsRef = useRef<number[]>([]);
	const cancelPendingFocusRestore = useCallback(() => {
		for (const id of restoreFocusFrameIdsRef.current) cancelAnimationFrame(id);
		restoreFocusFrameIdsRef.current = [];
	}, []);
	const restoreTerminalFocus = useCallback(() => {
		cancelPendingFocusRestore();
		const frameA = requestAnimationFrame(() => {
			const frameB = requestAnimationFrame(() => {
				restoreFocusFrameIdsRef.current = [];
				// The terminal may have been hidden or reassigned to another
				// session during these two frames (e.g. navigation away from
				// this pane); re-check before stealing focus back from
				// whatever now legitimately owns it.
				const host = hostRef.current;
				if (!host || callbacksRef.current.isVisible === false || !canAutoFocusTerminal(host)) return;
				focusTerminal();
			});
			restoreFocusFrameIdsRef.current = [frameB];
		});
		restoreFocusFrameIdsRef.current = [frameA];
	}, [cancelPendingFocusRestore, focusTerminal]);
	const toggleFullscreenAndRestoreFocus = useCallback(async () => {
		try {
			await callbacksRef.current.onToggleFullscreen?.();
		} finally {
			restoreTerminalFocus();
		}
	}, [restoreTerminalFocus]);

	callbacksRef.current = props;
	showCopiedToastRef.current = () => {
		// Hidden retained terminals keep parsing output but expose no UI overlays.
		if (callbacksRef.current.isVisible === false) return;
		setCopiedToast(true);
		if (copiedToastTimerRef.current !== undefined) window.clearTimeout(copiedToastTimerRef.current);
		copiedToastTimerRef.current = window.setTimeout(() => {
			setCopiedToast(false);
			copiedToastTimerRef.current = undefined;
		}, COPY_TOAST_MS);
	};
	linkPreviewControlsRef.current = {
		show: (url, x, y) => {
			// Same overlay rule as the copy toast: hidden retained terminals show nothing.
			if (callbacksRef.current.isVisible === false) return;
			if (linkPreviewTimerRef.current !== undefined) window.clearTimeout(linkPreviewTimerRef.current);
			linkPreviewTimerRef.current = window.setTimeout(() => {
				linkPreviewTimerRef.current = undefined;
				setLinkPreview({ url, x, y });
			}, LINK_PREVIEW_OPEN_MS);
		},
		hide: (immediately = false) => {
			if (linkPreviewTimerRef.current !== undefined) window.clearTimeout(linkPreviewTimerRef.current);
			if (immediately) {
				linkPreviewTimerRef.current = undefined;
				setLinkPreview(null);
				return;
			}
			linkPreviewTimerRef.current = window.setTimeout(() => {
				linkPreviewTimerRef.current = undefined;
				setLinkPreview(null);
			}, LINK_PREVIEW_CLOSE_MS);
		},
		cancelHide: () => {
			if (linkPreviewTimerRef.current !== undefined) {
				window.clearTimeout(linkPreviewTimerRef.current);
				linkPreviewTimerRef.current = undefined;
			}
		},
	};
	return {
		announcedCursorSchemeRef,
		callbacksRef,
		cancelPendingFocusRestore,
		colorSchemeReporterRef,
		contextMenu,
		contextMenuActionsRef,
		copiedToast,
		copiedToastTimerRef,
		fitRef,
		focusTerminal,
		gridMeasuredRef,
		hostRef,
		hoveredLinkRef,
		linkPreview,
		linkPreviewControlsRef,
		linkPreviewQuery,
		linkPreviewTimerRef,
		macPlatform,
		notifyCursorSchemeRef,
		runContextMenuAction,
		scrollbarThumbRef,
		scrollbarTrackRef,
		searchAddonRef,
		searchOpen,
		setContextMenu,
		setContextMenuOpen,
		setCopiedToast,
		setSearchOpen,
		shellRef,
		showCopiedToastRef,
		t,
		termRef,
		themeStyle,
		toggleFullscreenAndRestoreFocus,
		translucent,
		webglLeaseRef,
	};
}
