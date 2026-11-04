import type { TerminalUserInputSource } from "../../hooks/useTerminalSession";
import { tallpineBridge } from "../../lib/bridge";
import { isMacPlatform } from "../../lib/platform";
import { buildCursorColorSchemeNotification } from "../../lib/cursor-color-scheme";
import { buildOscColorReports, type OscTerminalColors } from "../../lib/osc-color-report";
import { buildTerminalThemes } from "../../lib/terminal-themes";
import { useUiStore, type Theme, type ThemeStyle } from "../../stores/ui-store";
import type { XtermEnv, XtermState } from "./xterm-types";
import { installSetup } from "./xterm-setup";
import { joinVisuallyContinuousLines, COLOR_SCHEME_UPDATE_MODE, COLOR_SCHEME_QUERY, preparePastedText, bracketPastedText, SUPPRESS_NATIVE_PASTE_MS } from "./xterm-paste";
import { isTerminalSearchShortcut, consumeTerminalShortcut, terminalFontSizeDelta, isTerminalCopyShortcut, isTerminalPasteShortcut, normalizedTerminalShortcut, terminalHasFocus } from "./xterm-shortcuts";

export function installInput(env: XtermEnv, st: XtermState, setupOut: NonNullable<ReturnType<typeof installSetup>>) {
	const {
		announcedCursorSchemeRef,
		callbacksRef,
		colorSchemeReporterRef,
		contextMenuActionsRef,
		hoveredLinkRef,
		notifyCursorSchemeRef,
		props,
		setContextMenu,
		setSearchOpen,
		showCopiedToastRef,
		themeStyle,
	} = env;
	const {
		host,
		shell,
		term,
	} = setupOut;
	st.columnSelectionActive = false;
	st.pendingColumnSelection = null;
	const copySelection = (options?: { clipboardData?: DataTransfer | null }) => {
		const selection = joinVisuallyContinuousLines(term, term.getSelection(), st.columnSelectionActive);
		if (!selection) return false;
		options?.clipboardData?.setData("text/plain", selection);
		void tallpineBridge.clipboard
			.writeText(selection)
			.then(() => {
				showCopiedToastRef.current();
			})
			.catch((error) => {
				console.warn("Unable to copy terminal selection", error);
			});
		return true;
	};
	const userInputListeners = new Set<(data: string, source: TerminalUserInputSource) => boolean | void>();
	const emitUserInput = (data: string, source: TerminalUserInputSource) => {
		if (data.length === 0) return false;
		let accepted = false;
		userInputListeners.forEach((listener) => {
			if (listener(data, source) === true) accepted = true;
		});
		return accepted;
	};
	// OpenTUI clients use the color-scheme protocol to receive live light/dark
	// changes after startup. The replies follow the app theme, so they stay
	// here rather than using xterm's luminance-based replies.
	let colorSchemeUpdatesEnabled = false;
	let currentColorScheme = props.theme;
	let currentThemeStyle = themeStyle;
	const reportColorScheme = (theme: Theme, nextThemeStyle: ThemeStyle, force = false) => {
		const changed = theme !== currentColorScheme || nextThemeStyle !== currentThemeStyle;
		currentColorScheme = theme;
		currentThemeStyle = nextThemeStyle;
		if (!force && (!colorSchemeUpdatesEnabled || !changed)) return;
		emitUserInput(`\x1b[?997;${theme === "dark" ? 1 : 2}n`, "protocol");
	};
	const hasCsiMode = (params: (number | number[])[], mode: number) =>
		params.some((param) => param === mode);
	colorSchemeReporterRef.current = reportColorScheme;
	const setColorSchemeUpdates = term.parser.registerCsiHandler(
		{ prefix: "?", final: "h" },
		(params) => {
			if (!hasCsiMode(params, COLOR_SCHEME_UPDATE_MODE)) return false;
			colorSchemeUpdatesEnabled = true;
			currentColorScheme = callbacksRef.current.theme;
			currentThemeStyle = useUiStore.getState().themeStyle;
			return params.length === 1;
		},
	);
	const resetColorSchemeUpdates = term.parser.registerCsiHandler(
		{ prefix: "?", final: "l" },
		(params) => {
			if (!hasCsiMode(params, COLOR_SCHEME_UPDATE_MODE)) return false;
			colorSchemeUpdatesEnabled = false;
			return params.length === 1;
		},
	);
	const queryColorSchemeCapability = term.parser.registerCsiHandler(
		{ prefix: "?", intermediates: "$", final: "p" },
		(params) => {
			if (!hasCsiMode(params, COLOR_SCHEME_UPDATE_MODE)) return false;
			emitUserInput(`\x1b[?${COLOR_SCHEME_UPDATE_MODE};${colorSchemeUpdatesEnabled ? 1 : 2}$y`, "protocol");
			return params.length === 1;
		},
	);
	const queryColorScheme = term.parser.registerCsiHandler(
		{ prefix: "?", final: "n" },
		(params) => {
			if (!hasCsiMode(params, COLOR_SCHEME_QUERY)) return false;
			reportColorScheme(
				callbacksRef.current.theme,
				useUiStore.getState().themeStyle,
				true,
			);
			return params.length === 1;
		},
	);
	const terminalColorsForScheme = (scheme: Theme): OscTerminalColors => {
		const palette = buildTerminalThemes()[scheme];
		return {
			foreground: palette.foreground ?? "",
			background: palette.background ?? "",
			cursor: palette.cursor ?? palette.foreground ?? "",
		};
	};
	const notifyCursorScheme = (scheme: Theme, force = false, retry = false) => {
		if (!force && announcedCursorSchemeRef.current === scheme) return;
		announcedCursorSchemeRef.current = scheme;
		const bytes =
			buildOscColorReports(terminalColorsForScheme(scheme)) +
			buildCursorColorSchemeNotification(scheme);
		const send = () => emitUserInput(bytes, "protocol");
		send();
		if (!retry) return;
		for (const timer of st.schemeRetryTimers) window.clearTimeout(timer);
		st.schemeRetryTimers = [50, 200].map((delayMs) => window.setTimeout(send, delayMs));
	};
	st.schemeRetryTimers = [];
	notifyCursorSchemeRef.current = notifyCursorScheme;
	const pasteText = (text: string) => {
		const prepared = preparePastedText(text);
		const bracketed = term.modes.bracketedPasteMode && term.options.ignoreBracketedPasteMode !== true;
		emitUserInput(bracketPastedText(prepared, bracketed), "paste");
	};
	st.suppressNextNativePaste = false;
	let suppressPasteTimer: number | null = null;
	const clearSuppressNativePaste = () => {
		st.suppressNextNativePaste = false;
		if (suppressPasteTimer !== null) {
			window.clearTimeout(suppressPasteTimer);
			suppressPasteTimer = null;
		}
	};
	const suppressNativePasteOnce = () => {
		st.suppressNextNativePaste = true;
		if (suppressPasteTimer !== null) window.clearTimeout(suppressPasteTimer);
		suppressPasteTimer = window.setTimeout(clearSuppressNativePaste, SUPPRESS_NATIVE_PASTE_MS);
	};
	const pasteFromClipboard = () => {
		void tallpineBridge.clipboard
			.readText()
			.then(pasteText)
			.catch((error) => {
				console.warn("Unable to paste terminal clipboard text", error);
			});
	};
	const focusTerminal = () => {
		try {
			term.focus();
		} catch {
			// Terminal is being torn down or its hidden textarea is unavailable.
		}
	};
	contextMenuActionsRef.current = {
		copy: () => {
			copySelection();
			focusTerminal();
		},
		paste: () => {
			pasteFromClipboard();
			focusTerminal();
		},
		selectAll: () => {
			st.columnSelectionActive = false;
			st.pendingColumnSelection = null;
			term.selectAll();
			focusTerminal();
		},
	};
	const openContextMenu = (event: MouseEvent) => {
		event.preventDefault();
		event.stopPropagation();
		setContextMenu({
			canCopy: term.hasSelection(),
			open: true,
			x: event.clientX,
			y: event.clientY,
			link: hoveredLinkRef.current,
		});
	};
	shell.addEventListener("contextmenu", openContextMenu);
	term.attachCustomKeyEventHandler((event) => {
		// xterm invokes this same handler on keydown, keyup, AND keypress (see
		// Terminal.ts _keyDown/_keyUp/_keyPress). Only keydown should trigger our
		// shortcut actions (copy/paste/word-nav) — otherwise releasing the key
		// re-matches the same combo and fires the action a second time (double
		// paste, double word-delete, etc). keyup/keypress fall through to
		// xterm's own default handling for that event type.
		if (event.type === "keyup" || event.type === "keypress") return true;
		if (isTerminalSearchShortcut(event)) {
			consumeTerminalShortcut(event);
			setSearchOpen(true);
			return false;
		}
		// Shift+Enter → newline without submitting, matching Claude Code / Codex.
		// A terminal normally sends the same CR for Enter and Shift+Enter, so the
		// agent can't distinguish them; emit the meta-return (ESC+CR) that
		// readline/Ink-based TUIs interpret as "insert a newline" rather than
		// "submit". Plain Enter still falls through to xterm's default CR.
		//
		// SCOPE: this meta-return is applied to every pane intentionally for now.
		// It is correct for agent TUIs but untested and unintended for plain login
		// shells, where ESC+CR is not a "newline" affordance. The correct fix is to
		// scope it by pane kind — TerminalPane already branches on
		// `terminalTarget?.kind === "shell"` at the XtermTerminal call site — once
		// this branch is rebased onto main, which brings that discriminator (and
		// ShellTerminalsView) that does not yet exist here. Until then the behavior
		// is left unchanged and the emitted bytes are identical for all panes.
		if (event.key === "Enter" && event.shiftKey && !event.ctrlKey && !event.altKey && !event.metaKey) {
			consumeTerminalShortcut(event);
			emitUserInput("\x1b\r", "keyboard");
			return false;
		}
		const fontSizeDelta = terminalFontSizeDelta(event);
		if (fontSizeDelta !== 0 && callbacksRef.current.onChangeFontSize) {
			consumeTerminalShortcut(event);
			callbacksRef.current.onChangeFontSize(fontSizeDelta);
			return false;
		}
		if (isTerminalCopyShortcut(event)) {
			if (copySelection()) {
				consumeTerminalShortcut(event);
				return false;
			}
			if ((event.ctrlKey && event.shiftKey) || (event.key === "Insert" && event.ctrlKey)) {
				consumeTerminalShortcut(event);
				return false;
			}
			return true;
		}
		if (isTerminalPasteShortcut(event)) {
			consumeTerminalShortcut(event);
			suppressNativePasteOnce();
			pasteFromClipboard();
			return false;
		}
		const normalized = normalizedTerminalShortcut(event);
		if (!normalized) return true;
		consumeTerminalShortcut(event);
		emitUserInput(normalized, "shortcut");
		return false;
	});
	const copyInput = (event: ClipboardEvent) => {
		if (!copySelection({ clipboardData: event.clipboardData })) return;
		event.preventDefault();
	};
	const copyShortcut = (event: KeyboardEvent) => {
		if (!isTerminalCopyShortcut(event) || !terminalHasFocus(shell) || !copySelection()) return;
		event.preventDefault();
		event.stopPropagation();
	};
	shell.addEventListener("copy", copyInput);
	window.addEventListener("keydown", copyShortcut, true);

	// Copy on select: releasing the mouse button after a selection copies it,
	// like every native terminal (issue #5785). xterm computes the selection
	// continuously during mousemove and its own mouseup handler does not touch
	// the selection, so the model is already final when pointerup fires —
	// copying here cannot race the TUI repaint that later drops the highlight.
	// Arming on pointerdown inside the terminal (left button only) keeps
	// releases elsewhere in the app — context menu items, the scrollbar — from
	// re-copying a lingering selection. Only a left-button release consumes
	// the armed flag (an in-between right-button release must not eat the
	// drag), and pointercancel or window blur disarms it: those never deliver
	// the pointerup, and a stale armed flag would copy on the next unrelated
	// click anywhere in the app.
	let pointerSelectionArmed = false;
	const disarmPointerSelection = () => {
		pointerSelectionArmed = false;
		st.pendingColumnSelection = null;
	};
	const pointerDown = (event: PointerEvent) => {
		if (event.button !== 0) return;
		pointerSelectionArmed = true;
		// xterm uses Alt-drag for column selection on Windows and Linux. On
		// macOS, Option is configured to force regular selection instead.
		st.pendingColumnSelection = event.altKey && !isMacPlatform();
	};
	const pointerUp = (event: PointerEvent) => {
		if (event.button !== 0) return;
		if (!pointerSelectionArmed) return;
		pointerSelectionArmed = false;
		st.columnSelectionActive = st.pendingColumnSelection ?? false;
		st.pendingColumnSelection = null;
		if (!useUiStore.getState().terminalCopyOnSelect) return;
		copySelection();
	};
	host.addEventListener("pointerdown", pointerDown);
	document.addEventListener("pointerup", pointerUp);
	document.addEventListener("pointercancel", disarmPointerSelection);
	window.addEventListener("blur", disarmPointerSelection);

	// FitAddon falls back to its 2-column minimum for a host with no layout
	// box (a parked tab, or one not laid out yet). That grid would reach the
	// PTY as real, so a fit only proposes from a laid-out host.
	return {
		clearSuppressNativePaste,
		copyInput,
		copyShortcut,
		disarmPointerSelection,
		emitUserInput,
		focusTerminal,
		notifyCursorScheme,
		openContextMenu,
		pasteText,
		pointerDown,
		pointerUp,
		queryColorScheme,
		queryColorSchemeCapability,
		reportColorScheme,
		resetColorSchemeUpdates,
		setColorSchemeUpdates,
		userInputListeners,
	};
}
