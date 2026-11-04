/** Shared types for the xterm terminal component and its mount-effect installers. */
import { Terminal } from "@xterm/xterm";
import type { AttachableTerminal } from "../../hooks/useTerminalSession";
import { type Theme } from "../../stores/ui-store";
import type { useXtermState } from "./useXtermState";

export type XtermTerminalProps = {
	ariaLabel?: string;
	className?: string;
	fontSize?: number;
	isFullscreen?: boolean;
	theme: Theme;
	/** Resize this terminal without changing application zoom. */
	onChangeFontSize?: (delta: number) => void;
	/** Enter or exit fullscreen for the terminal pane that owns this xterm. */
	onToggleFullscreen?: () => void | Promise<void>;
	/**
	 * The pane app scrolls its transcript by keyboard (PageUp/PageDown) rather
	 * than acting on SGR wheel reports — e.g. opencode, which enables mouse
	 * tracking but never scrolls on wheel reports. Routes the wheel to page keys
	 * on every platform (see the wheel handler), fixing it under a mux too.
	 */
	paneScrollsByKeyboard?: boolean;
	/** Terminal construction failed; the owner decides how to surface it. */
	onError?: (error: unknown) => void;
	/** Called once the visible xterm viewport has painted nonblank content. */
	onVisibleContent?: () => void;
	/** Called after a terminal hyperlink is opened in the OS browser. */
	onLinkOpen?: (uri: string) => void;
	/** Navigate a canonical tallpine:// session link inside the current Tallpine window. */
	onSessionLinkOpen?: (uri: string) => void;
	/** Publish the positive grid after a retained terminal becomes visible. */
	onVisibleSize?: (cols: number, rows: number) => void;
	/** Hidden retained terminals keep parsing output but expose no UI overlays. */
	isVisible?: boolean;
	/** Cursor Agent understands Tallpine's terminal color protocol; generic terminals do not. */
	supportsCursorColorScheme?: boolean;
	/** Move keyboard focus into xterm when a controller needs human input. */
	focusRequested?: boolean;
	/**
	 * The terminal is open in the DOM and ready to be attached to a PTY. The
	 * handle stays valid until unmount; cols/rows are live getters.
	 */
	onReady?: (terminal: AttachableTerminal) => void;
};


export type XtermInternal = Terminal & {
	_core?: {
		element?: HTMLElement;
		_selectionService?: {
			enable: () => void;
			shouldForceSelection: (event: MouseEvent) => boolean;
			// xterm installs this listener on document while a drag selection is
			// active. It is private, but xterm exposes no public hook for changing
			// the document-wide drag behavior.
			_mouseMoveListener?: EventListener;
			_dragScrollAmount?: number;
		};
	};
};

export type DevXtermHost = HTMLDivElement & {
	__tallpineXtermForTest?: Terminal;
};

export type TerminalContextMenuState = {
	canCopy: boolean;
	open: boolean;
	x: number;
	y: number;
	// The web link under the cursor when the menu opened, if any — enables the
	// "Open in system browser" item (left-click opens it in the Tallpine Browser).
	link: string | null;
};

export type TerminalContextMenuAction = "copy" | "paste" | "selectAll";

export type TerminalContextMenuActions = Record<TerminalContextMenuAction, () => void>;

// For mouse-tracking panes we synthesize SGR mouse-wheel reports and write them
// to the pane; tmux (with `mouse on`, set by the runtime adapter) acts on them
// and scrolls its scrollback via copy-mode. Left to itself xterm would convert
// the wheel into cursor-arrow keys (its alt-buffer fallback), which move the
// agent's cursor rather than scrolling. SGR button 64 = wheel up, 65 = down;
// reports are 1-based; coordinates must identify the actual transcript cell.

type XtermViewState = ReturnType<typeof useXtermState>;

/** Component-scope refs, state and callbacks the mount-effect installers read (first-render snapshot). */
export type XtermEnv = Pick<
	XtermViewState,
	| "announcedCursorSchemeRef"
	| "callbacksRef"
	| "colorSchemeReporterRef"
	| "contextMenuActionsRef"
	| "fitRef"
	| "gridMeasuredRef"
	| "hostRef"
	| "hoveredLinkRef"
	| "linkPreviewControlsRef"
	| "macPlatform"
	| "notifyCursorSchemeRef"
	| "scrollbarThumbRef"
	| "scrollbarTrackRef"
	| "searchAddonRef"
	| "setContextMenu"
	| "setSearchOpen"
	| "shellRef"
	| "showCopiedToastRef"
	| "t"
	| "termRef"
	| "themeStyle"
	| "translucent"
	| "webglLeaseRef"
> & { props: XtermTerminalProps };

/** Mutable values the mount-effect installers share (one object per mount). */
export type XtermState = {
	reportedFocused: boolean;
	scrollbarFrame: number | null;
	scrollbarHideTimer: number | null;
	columnSelectionActive: boolean;
	pendingColumnSelection: boolean | null;
	schemeRetryTimers: number[];
	suppressNextNativePaste: boolean;
	pendingReplayWrites: number;
	resizeGeneration: number;
	fitQuietTimer: ReturnType<typeof setTimeout> | null;
	fitCapTimer: ReturnType<typeof setTimeout> | null;
	disposed: boolean;
	liveFitFrame: number | null;
	cancelActivationPreparation: (() => void) | null;
};
