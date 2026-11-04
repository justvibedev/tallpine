import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { SearchAddon } from "@xterm/addon-search";
import { Unicode11Addon } from "@xterm/addon-unicode11";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { hasInAppLinkScheme } from "../../../shared/session-link-parse";
import { tallpineBridge } from "../../lib/bridge";
import { TERMINAL_FONT_SIZE_DEFAULT } from "../../lib/design-tokens";
import { isWebLink, openLinkInSystemBrowser } from "../../lib/external-link-policy";
import { isMacPlatform } from "../../lib/platform";
import { buildTerminalThemes } from "../../lib/terminal-themes";
import type { XtermEnv, XtermState } from "./xterm-types";
import { terminalHasFocus } from "./xterm-shortcuts";
import { MAC_TERMINAL_SCROLLBAR_WIDTH } from "./xterm-scroll";
import { sessionLinkProvider } from "./xterm-links";

export function installSetup(env: XtermEnv, st: XtermState) {
	const {
		callbacksRef,
		gridMeasuredRef,
		hostRef,
		hoveredLinkRef,
		linkPreviewControlsRef,
		macPlatform,
		props,
		searchAddonRef,
		shellRef,
		termRef,
		translucent,
	} = env;
	const shell = shellRef.current;
	const host = hostRef.current;
	if (!shell || !host) return undefined;
	st.reportedFocused = false;
	let reportedFontSize = false;
	// Every terminal reports focus so Ctrl+K/W/B/T reach the shell; only ones
	// that resize their own font also claim the zoom chords.
	const reportFocused = (focused: boolean) => {
		const handlesFontSize = focused && Boolean(callbacksRef.current.onChangeFontSize);
		if (focused === st.reportedFocused && handlesFontSize === reportedFontSize) return;
		st.reportedFocused = focused;
		reportedFontSize = handlesFontSize;
		tallpineBridge.terminal.setFocused(focused, handlesFontSize);
	};
	const handleFocusIn = () => reportFocused(true);
	const handleFocusOut = (event: FocusEvent) => {
		const next = event.relatedTarget;
		if (next instanceof Node && host.contains(next)) return;
		reportFocused(false);
	};
	host.addEventListener("focusin", handleFocusIn);
	host.addEventListener("focusout", handleFocusOut);
	const disposeFontSizeShortcut = tallpineBridge.terminal.onFontSizeShortcut((delta) => {
		if (!terminalHasFocus(host)) return;
		callbacksRef.current.onChangeFontSize?.(delta);
	});
	const activateLink = (event: MouseEvent, uri: string) => {
		// Open on a plain click even while the pane tracks the mouse, like web
		// links: tmux (mouse on) and fullscreen TUIs keep tracking on, so a
		// modifier gate here made plain clicks do nothing.
		if (hasInAppLinkScheme(uri)) {
			callbacksRef.current.onSessionLinkOpen?.(uri);
			return;
		}
		// Left-click on a web link opens it inside the Tallpine Browser panel (the
		// parent decides how). Non-web schemes (mailto:, etc.) still go to the OS
		// via the main process's window-open handler. Right-click to open a web
		// link in the system browser instead — see the context menu below. Cmd-click
		// follows the same escape hatch as links in the Chat surface.
		if (isWebLink(uri)) {
			if (event.altKey || event.metaKey) {
				void openLinkInSystemBrowser(uri);
				return;
			}
			callbacksRef.current.onLinkOpen?.(uri);
			return;
		}
		window.open(uri, "_blank", "noopener");
	};
	const trackHover = (event: MouseEvent, uri: string) => {
		const webUri = isWebLink(uri) ? uri : null;
		hoveredLinkRef.current = webUri;
		if (webUri) linkPreviewControlsRef.current.show(webUri, event.clientX, event.clientY);
		else linkPreviewControlsRef.current.hide(true);
	};
	const clearHover = () => {
		hoveredLinkRef.current = null;
		linkPreviewControlsRef.current.hide();
	};

	let term: Terminal;
	try {
		const { dark, light } = buildTerminalThemes(translucent);
		term = new Terminal({
			// xterm requires this before open; keep macOS terminals ready for
			// live transparency toggles without recreating their buffers.
			allowTransparency: macPlatform,
			// Required for the Unicode 11 width addon below.
			allowProposedApi: true,
			cursorBlink: true,
			// Resolve the Nerd Font stack from --font-mono (styles.css) at
			// construction so terminal glyphs follow the app's font tokens. The
			// box-drawing grid is rasterized by the WebGL renderer itself,
			// but powerline separators and file-type icons are real PUA codepoints
			// that must come from a system-installed Nerd Font.
			fontFamily:
				getComputedStyle(shell).getPropertyValue("--font-mono").trim() ||
				'ui-monospace, Menlo, Monaco, "Courier New", monospace',
			fontSize: props.fontSize ?? TERMINAL_FONT_SIZE_DEFAULT,
			lineHeight: 1.35,
			linkHandler: { activate: activateLink, hover: trackHover, leave: clearHover },
			// Preserve standard terminal semantics: many agent TUIs use bold ANSI
			// colors specifically to select the bright palette.
			drawBoldTextInBrightColors: true,
			// Agent TUIs already choose foreground/background pairs. A forced
			// contrast transform changes their RGB values and makes syntax and diff
			// colors diverge from the same CLI in a native terminal.
			minimumContrastRatio: 1,
			// Alt-buffer panes (tmux attach, mouse-tracking agent TUIs) never feed
			// this buffer — the alt screen doesn't accumulate scrollback — so this
			// only matters for normal-buffer panes that print their transcript and
			// rely on the terminal's scrollback (codex, a plain shell). Keep it > 0
			// so that history survives to be scrolled locally (see the wheel
			// handler's normal-buffer branch). macOS exposes that history through a
			// slim draggable scrollbar; other platforms retain the existing hidden
			// scrollbar behavior for now.
			scrollback: 5000,
			// xterm 6's FitAddon reads the public scrollbar options. Reserve
			// only the app-owned macOS gutter, matching the live resize path.
			scrollbar: { showScrollbar: isMacPlatform(), width: MAC_TERMINAL_SCROLLBAR_WIDTH },
			// This component answers color-scheme queries itself so the reply
			// follows the app theme, including theme style. xterm 6.1 also
			// answers them from palette luminance; leave that off.
			vtExtensions: { colorSchemeQuery: false },
			theme: props.theme === "dark" ? dark : light,
		});
	} catch (error) {
		callbacksRef.current.onError?.(error);
		return undefined;
	}

	termRef.current = term;

	const fit = new FitAddon();
	term.loadAddon(fit);
	const unicode = new Unicode11Addon();
	term.loadAddon(unicode);
	term.unicode.activeVersion = "11";
	// Open plain and OSC 8 links in the OS browser. The default handlers call
	// window.open() with no URL and then assigns location.href, but the
	// Electron main process denies every window.open and only forwards the URL
	// passed to it (main.ts setWindowOpenHandler), so the default handlers'
	// empty open is dropped and clicks silently no-op. Pass the matched URL to
	// window.open directly so the main process routes it to shell.openExternal.
	term.loadAddon(new WebLinksAddon(activateLink, { hover: trackHover, leave: clearHover }));
	term.registerLinkProvider(sessionLinkProvider(term, activateLink));
	const searchAddon = new SearchAddon();
	searchAddonRef.current = searchAddon;
	term.loadAddon(searchAddon);

	term.open(host);
	gridMeasuredRef.current = false;
	return {
		disposeFontSizeShortcut,
		fit,
		handleFocusIn,
		handleFocusOut,
		host,
		searchAddon,
		shell,
		term,
	};
}
